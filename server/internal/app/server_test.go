package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testConfig() Config {
	return Config{
		SessionSecret:    bytes.Repeat([]byte{7}, 32),
		TURNSharedSecret: strings.Repeat("a", 32),
		TURNHost:         "screen.example.com",
		TURNPort:         3479,
		TURNSTLSPort:     5349,
	}
}

func TestHTTPAcceptsArbitraryPairingToken(t *testing.T) {
	handler, err := NewServer(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/styles.css", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "--accent") {
		t.Fatalf("embedded asset unavailable: status=%d", asset.Code)
	}

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"anything-I-choose"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("viewer pairing returned %d: %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("viewer cookie is missing required security attributes")
	}

	agentRequest := httptest.NewRequest(http.MethodPost, "/api/agent/session", nil)
	agentRequest.Header.Set("Authorization", "Bearer anything-I-choose")
	agent := httptest.NewRecorder()
	handler.ServeHTTP(agent, agentRequest)
	if agent.Code != http.StatusOK {
		t.Fatalf("agent pairing returned %d: %s", agent.Code, agent.Body.String())
	}

	empty := httptest.NewRecorder()
	handler.ServeHTTP(empty, httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"  "}`)))
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty pairing token returned %d", empty.Code)
	}
}

func TestWebSocketRoomsAreIsolatedByPairingToken(t *testing.T) {
	cfg := testConfig()
	handler, err := NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	viewerCookie := loginForTest(t, server.URL, RoleViewer, "room-a")
	viewerConn := dialForTest(t, server.URL, viewerCookie)
	defer viewerConn.CloseNow()

	agentBCookie := loginForTest(t, server.URL, RoleAgent, "room-b")
	agentBConn := dialForTest(t, server.URL, agentBCookie)
	defer agentBConn.CloseNow()
	assertRoomStatus(t, server.URL, viewerCookie, false, true)

	agentACookie := loginForTest(t, server.URL, RoleAgent, "room-a")
	agentAConn := dialForTest(t, server.URL, agentACookie)
	defer agentAConn.CloseNow()
	assertRoomStatus(t, server.URL, viewerCookie, true, true)
}

func loginForTest(t *testing.T, baseURL string, role Role, token string) *http.Cookie {
	t.Helper()
	var request *http.Request
	var err error
	if role == RoleViewer {
		body, _ := json.Marshal(map[string]string{"token": token})
		request, err = http.NewRequest(http.MethodPost, baseURL+"/api/viewer/session", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	} else {
		request, err = http.NewRequest(http.MethodPost, baseURL+"/api/agent/session", nil)
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("%s login returned %d", role, response.StatusCode)
	}
	return response.Cookies()[0]
}

func dialForTest(t *testing.T, baseURL string, cookie *http.Cookie) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Cookie", cookie.String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(baseURL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		conn.CloseNow()
		t.Fatal(err)
	}
	return conn
}

func assertRoomStatus(t *testing.T, baseURL string, cookie *http.Cookie, wantAgent, wantViewer bool) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, baseURL+"/api/status", nil)
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var status struct {
		AgentOnline  bool `json:"agentOnline"`
		ViewerActive bool `json:"viewerActive"`
	}
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.AgentOnline != wantAgent || status.ViewerActive != wantViewer {
		t.Fatalf("room status=%+v, want agent=%v viewer=%v", status, wantAgent, wantViewer)
	}
}

func TestPairingKeyNormalization(t *testing.T) {
	left, ok := makePairingKey("  same-room  ")
	if !ok {
		t.Fatal("valid pairing token was rejected")
	}
	right, _ := makePairingKey("same-room")
	other, _ := makePairingKey("other-room")
	if left != right || left == other {
		t.Fatal("pairing token hashing did not isolate rooms")
	}
}

func TestLoginLimiterAndProxyAddress(t *testing.T) {
	limiter := newLoginLimiter(2, time.Minute)
	if !limiter.allow("client") || !limiter.allow("client") || limiter.allow("client") {
		t.Fatal("login limiter did not enforce its configured window")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Real-IP", "203.0.113.25")
	if got := clientIP(request); got != "203.0.113.25" {
		t.Fatalf("clientIP=%q", got)
	}
}
