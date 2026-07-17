package app

import (
	"bytes"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAuthenticationAndAssets(t *testing.T) {
	const deviceToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	passwordHash, err := HashPassword("viewer password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		DeviceTokenHash:    sha256.Sum256([]byte(deviceToken)),
		ViewerPasswordHash: passwordHash,
		SessionSecret:      bytes.Repeat([]byte{7}, 32),
		TURNSharedSecret:   strings.Repeat("a", 32),
		TURNHost:           "screen.example.com",
		TURNPort:           3479,
		TURNSTLSPort:       5349,
		SecureCookies:      true,
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

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/viewer/session", strings.NewReader(`{"password":"viewer password"}`)))
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
	agentRequest.Header.Set("Authorization", "Bearer "+deviceToken)
	agent := httptest.NewRecorder()
	handler.ServeHTTP(agent, agentRequest)
	if agent.Code != http.StatusOK {
		t.Fatalf("agent login returned %d: %s", agent.Code, agent.Body.String())
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
