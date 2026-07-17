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
	store.create(recorder, RoleViewer)
	cookie := recorder.Result().Cookies()[0]

	validRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	validRequest.AddCookie(cookie)
	if role, ok := store.role(validRequest); !ok || role != RoleViewer {
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
