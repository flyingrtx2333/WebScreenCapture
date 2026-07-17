package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Role string

const (
	RoleAgent  Role = "agent"
	RoleViewer Role = "viewer"
	cookieName      = "wsc_session"
)

type session struct {
	Role      Role
	ExpiresAt time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
	secret   []byte
	secure   bool
	ttl      time.Duration
}

func newSessionStore(secret []byte, secure bool) *sessionStore {
	return &sessionStore{sessions: make(map[string]session), secret: secret, secure: secure, ttl: 12 * time.Hour}
}

func (s *sessionStore) create(w http.ResponseWriter, role Role) {
	idBytes := make([]byte, 32)
	_, _ = rand.Read(idBytes)
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	expires := time.Now().Add(s.ttl)

	s.mu.Lock()
	for existingID, existing := range s.sessions {
		if existing.Role == role || time.Now().After(existing.ExpiresAt) {
			delete(s.sessions, existingID)
		}
	}
	s.sessions[id] = session{Role: role, ExpiresAt: expires}
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id + "." + s.sign(id),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(s.ttl.Seconds()),
		Secure:   s.secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *sessionStore) clear(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		id, _, ok := strings.Cut(cookie.Value, ".")
		if ok {
			s.mu.Lock()
			delete(s.sessions, id)
			s.mu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   s.secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *sessionStore) role(r *http.Request) (Role, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	id, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(s.sign(id))) {
		return "", false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[id]
	if !ok || time.Now().After(current.ExpiresAt) {
		delete(s.sessions, id)
		return "", false
	}
	return current.Role, true
}

func (s *sessionStore) sign(id string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
