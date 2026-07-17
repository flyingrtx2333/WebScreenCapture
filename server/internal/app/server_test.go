package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAuthenticationAndAssets(t *testing.T) {
	const accessToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cfg := Config{
		AccessTokenHash:  sha256.Sum256([]byte(accessToken)),
		SessionSecret:    bytes.Repeat([]byte{7}, 32),
		TURNSharedSecret: strings.Repeat("a", 32),
		TURNHost:         "screen.example.com",
		TURNPort:         3479,
		TURNSTLSPort:     5349,
		SecureCookies:    true,
	}
	handler, err := NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/styles.css", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "--accent") {
		t.Fatalf("embedded asset unavailable: status=%d", asset.Code)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status returned %d", unauthorized.Code)
	}
	unauthorizedRotate := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRotate, httptest.NewRequest(http.MethodPost, "/api/access-token/rotate", strings.NewReader(`{}`)))
	if unauthorizedRotate.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated token rotation returned %d", unauthorizedRotate.Code)
	}

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"`+accessToken+`"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("viewer login returned %d: %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("viewer cookie is missing required security attributes")
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRequest.AddCookie(cookies[0])
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, statusRequest)
	if status.Code != http.StatusOK {
		t.Fatalf("authenticated status returned %d", status.Code)
	}

	agentRequest := httptest.NewRequest(http.MethodPost, "/api/agent/session", nil)
	agentRequest.Header.Set("Authorization", "Bearer "+accessToken)
	agent := httptest.NewRecorder()
	handler.ServeHTTP(agent, agentRequest)
	if agent.Code != http.StatusOK {
		t.Fatalf("agent login returned %d: %s", agent.Code, agent.Body.String())
	}
}

func TestAccessTokenRotationUsesOneTokenForViewerAndAgent(t *testing.T) {
	const oldToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfg := Config{
		AccessTokenHash:  sha256.Sum256([]byte(oldToken)),
		AccessTokenFile:  t.TempDir() + "/access-token.sha256",
		SessionSecret:    bytes.Repeat([]byte{8}, 32),
		TURNSharedSecret: strings.Repeat("a", 32),
		TURNHost:         "screen.example.com",
		TURNPort:         3479,
		TURNSTLSPort:     5349,
	}
	handler, err := NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	agentLogin := httptest.NewRecorder()
	agentRequest := httptest.NewRequest(http.MethodPost, "/api/agent/session", nil)
	agentRequest.Header.Set("Authorization", "Bearer "+oldToken)
	handler.ServeHTTP(agentLogin, agentRequest)
	agentCookie := agentLogin.Result().Cookies()[0]

	viewerLogin := httptest.NewRecorder()
	handler.ServeHTTP(viewerLogin, httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"`+oldToken+`"}`)))
	viewerCookie := viewerLogin.Result().Cookies()[0]

	rotateRequest := httptest.NewRequest(http.MethodPost, "/api/access-token/rotate", strings.NewReader(`{}`))
	rotateRequest.AddCookie(viewerCookie)
	rotate := httptest.NewRecorder()
	handler.ServeHTTP(rotate, rotateRequest)
	if rotate.Code != http.StatusOK || rotate.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate returned %d: %s", rotate.Code, rotate.Body.String())
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rotate.Body).Decode(&payload); err != nil || len(payload.Token) != 64 {
		t.Fatalf("invalid rotate response: %q, %v", rotate.Body.String(), err)
	}

	oldAgentRequest := httptest.NewRequest(http.MethodPost, "/api/agent/session", nil)
	oldAgentRequest.Header.Set("Authorization", "Bearer "+oldToken)
	oldAgent := httptest.NewRecorder()
	handler.ServeHTTP(oldAgent, oldAgentRequest)
	if oldAgent.Code != http.StatusUnauthorized {
		t.Fatalf("old token still authenticated agent: %d", oldAgent.Code)
	}

	newAgentRequest := httptest.NewRequest(http.MethodPost, "/api/agent/session", nil)
	newAgentRequest.Header.Set("Authorization", "Bearer "+payload.Token)
	newAgent := httptest.NewRecorder()
	handler.ServeHTTP(newAgent, newAgentRequest)
	if newAgent.Code != http.StatusOK {
		t.Fatalf("new token rejected for agent: %d", newAgent.Code)
	}

	newViewer := httptest.NewRecorder()
	handler.ServeHTTP(newViewer, httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"token":"`+payload.Token+`"}`)))
	if newViewer.Code != http.StatusOK {
		t.Fatalf("new token rejected for viewer: %d", newViewer.Code)
	}

	staleSessionRequest := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	staleSessionRequest.AddCookie(agentCookie)
	staleSession := httptest.NewRecorder()
	handler.ServeHTTP(staleSession, staleSessionRequest)
	if staleSession.Code != http.StatusUnauthorized {
		t.Fatalf("agent session survived rotation: %d", staleSession.Code)
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
