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

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Username != "test-user" || input.Password != "test-password" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "invalid credentials"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"access_token": "test", "refresh_token": "test", "token_type": "bearer"})
	}))
	t.Cleanup(auth.Close)
	cfg := testConfig()
	cfg.FlyingRTXAuthURL = auth.URL + "/api/v1/auth/login"
	handler, err := NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAccountLoginGatesPairingAndAgent(t *testing.T) {
	handler := newTestHandler(t)

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/styles.css", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "--accent") {
		t.Fatalf("embedded asset unavailable: status=%d", asset.Code)
	}

	legacyAgent := httptest.NewRecorder()
	handler.ServeHTTP(legacyAgent, httptest.NewRequest(http.MethodGet, "/agent", nil))
	if legacyAgent.Code != http.StatusNotFound {
		t.Fatalf("legacy browser capture endpoint returned %d", legacyAgent.Code)
	}

	unauthorizedPairing := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedPairing, httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"anything-I-choose"}`)))
	if unauthorizedPairing.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous viewer pairing returned %d", unauthorizedPairing.Code)
	}

	badLogin := httptest.NewRecorder()
	handler.ServeHTTP(badLogin, httptest.NewRequest(http.MethodPost, "/api/account/session", strings.NewReader(`{"username":"test-user","password":"wrong"}`)))
	if badLogin.Code != http.StatusUnauthorized {
		t.Fatalf("bad unified account login returned %d", badLogin.Code)
	}

	accountLogin := httptest.NewRecorder()
	handler.ServeHTTP(accountLogin, httptest.NewRequest(http.MethodPost, "/api/account/session", strings.NewReader(`{"username":"test-user","password":"test-password"}`)))
	if accountLogin.Code != http.StatusOK {
		t.Fatalf("unified account login returned %d: %s", accountLogin.Code, accountLogin.Body.String())
	}
	cookies := accountLogin.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("account cookie is missing required security attributes")
	}

	accountCheckRequest := httptest.NewRequest(http.MethodGet, "/api/account/check", nil)
	accountCheckRequest.AddCookie(cookies[0])
	accountCheck := httptest.NewRecorder()
	handler.ServeHTTP(accountCheck, accountCheckRequest)
	if accountCheck.Code != http.StatusNoContent {
		t.Fatalf("authenticated download check returned %d", accountCheck.Code)
	}

	viewerRequest := httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"anything-I-choose"}`))
	viewerRequest.AddCookie(cookies[0])
	viewer := httptest.NewRecorder()
	handler.ServeHTTP(viewer, viewerRequest)
	if viewer.Code != http.StatusOK {
		t.Fatalf("viewer pairing returned %d: %s", viewer.Code, viewer.Body.String())
	}

	agentRequest := httptest.NewRequest(http.MethodPost, "/api/agent/session", nil)
	agentRequest.Header.Set("Authorization", "Bearer anything-I-choose")
	agent := httptest.NewRecorder()
	handler.ServeHTTP(agent, agentRequest)
	if agent.Code != http.StatusOK {
		t.Fatalf("agent pairing returned %d: %s", agent.Code, agent.Body.String())
	}

	jsonAgent := httptest.NewRecorder()
	handler.ServeHTTP(jsonAgent, httptest.NewRequest(http.MethodPost, "/api/agent/session", strings.NewReader(`{"token":"native agent room"}`)))
	if jsonAgent.Code != http.StatusForbidden {
		t.Fatalf("unapproved native agent pairing returned %d: %s", jsonAgent.Code, jsonAgent.Body.String())
	}

	emptyRequest := httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"  "}`))
	emptyRequest.AddCookie(cookies[0])
	empty := httptest.NewRecorder()
	handler.ServeHTTP(empty, emptyRequest)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty pairing token returned %d", empty.Code)
	}
}

func TestWebSocketRoomsAreIsolatedByPairingToken(t *testing.T) {
	handler := newTestHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	viewerCookie := loginForTest(t, server.URL, RoleViewer, "room-a")
	viewerConn := dialForTest(t, server.URL, viewerCookie)
	defer viewerConn.CloseNow()

	viewerBCookie := loginForTest(t, server.URL, RoleViewer, "room-b")
	viewerBConn := dialForTest(t, server.URL, viewerBCookie)
	defer viewerBConn.CloseNow()

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
		accountBody, _ := json.Marshal(map[string]string{"username": "test-user", "password": "test-password"})
		accountRequest, requestErr := http.NewRequest(http.MethodPost, baseURL+"/api/account/session", bytes.NewReader(accountBody))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		accountRequest.Header.Set("Content-Type", "application/json")
		accountResponse, requestErr := http.DefaultClient.Do(accountRequest)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer accountResponse.Body.Close()
		if accountResponse.StatusCode != http.StatusOK || len(accountResponse.Cookies()) != 1 {
			t.Fatalf("account login returned %d", accountResponse.StatusCode)
		}
		body, _ := json.Marshal(map[string]string{"token": token})
		request, err = http.NewRequest(http.MethodPost, baseURL+"/api/viewer/session", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(accountResponse.Cookies()[0])
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
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s login returned %d", role, response.StatusCode)
	}
	if role == RoleViewer {
		return request.Cookies()[0]
	}
	if len(response.Cookies()) != 1 {
		t.Fatalf("%s login did not return a session cookie", role)
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
