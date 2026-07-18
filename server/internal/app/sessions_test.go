package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionRejectsForgedAndExpiredCookies(t *testing.T) {
	store := newSessionStore(bytes.Repeat([]byte{9}, 32), true)
	recorder := httptest.NewRecorder()
	store.create(recorder, RoleViewer, "room-key")
	cookie := recorder.Result().Cookies()[0]

	validRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	validRequest.AddCookie(cookie)
	if _, current, ok := store.current(validRequest); !ok || current.Role != RoleViewer || current.PairingKey != "room-key" {
		t.Fatal("valid session was rejected")
	}

	forgedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	forged := *cookie
	forged.Value += "x"
	forgedRequest.AddCookie(&forged)
	if _, ok := store.role(forgedRequest); ok {
		t.Fatal("forged session was accepted")
	}

	store.mu.Lock()
	for id, current := range store.sessions {
		current.ExpiresAt = time.Now().Add(-time.Second)
		store.sessions[id] = current
	}
	store.mu.Unlock()
	if _, ok := store.role(validRequest); ok {
		t.Fatal("expired session was accepted")
	}
}

func TestAccountSessionCanAuthorizeViewerPairing(t *testing.T) {
	store := newSessionStore(bytes.Repeat([]byte{5}, 32), true)
	recorder := httptest.NewRecorder()
	store.createAccount(recorder, "flyingrtx-user")
	cookie := recorder.Result().Cookies()[0]
	request := httptest.NewRequest(http.MethodPost, "/api/viewer/session", nil)
	request.AddCookie(cookie)

	if store.viewerAuthorized("room-key") {
		t.Fatal("room was authorized before the account selected it")
	}
	if !store.bindViewer(request, "room-key") || !store.viewerAuthorized("room-key") {
		t.Fatal("account session did not authorize its selected pairing room")
	}
	_, current, ok := store.current(request)
	if !ok || current.Role != RoleViewer || current.Username != "flyingrtx-user" {
		t.Fatalf("unexpected bound viewer session: %#v", current)
	}
}
