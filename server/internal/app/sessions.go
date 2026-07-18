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
	RoleAccount Role = "account"
	RoleAgent   Role = "agent"
	RoleViewer  Role = "viewer"
	cookieName       = "wsc_session"
)

type session struct {
	Role       Role
	PairingKey string
	Username   string
	ExpiresAt  time.Time
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

func (s *sessionStore) create(w http.ResponseWriter, role Role, pairingKey string) {
	s.createSession(w, session{Role: role, PairingKey: pairingKey})
}

func (s *sessionStore) createAccount(w http.ResponseWriter, username string) {
	s.createSession(w, session{Role: RoleAccount, Username: username})
}

func (s *sessionStore) createSession(w http.ResponseWriter, current session) {
	idBytes := make([]byte, 32)
	_, _ = rand.Read(idBytes)
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	expires := time.Now().Add(s.ttl)
	current.ExpiresAt = expires

	s.mu.Lock()
	for existingID, existing := range s.sessions {
		if (current.Role != RoleAccount && existing.Role == current.Role && existing.PairingKey == current.PairingKey) || time.Now().After(existing.ExpiresAt) {
			delete(s.sessions, existingID)
		}
	}
	s.sessions[id] = current
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

func (s *sessionStore) bindViewer(r *http.Request, pairingKey string) bool {
	id, current, ok := s.current(r)
	if !ok || current.Username == "" || (current.Role != RoleAccount && current.Role != RoleViewer) {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	latest, ok := s.sessions[id]
	if !ok || latest.Username == "" || time.Now().After(latest.ExpiresAt) {
		return false
	}
	latest.Role = RoleViewer
	latest.PairingKey = pairingKey
	s.sessions[id] = latest
	return true
}

func (s *sessionStore) viewerAuthorized(pairingKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, current := range s.sessions {
		if now.After(current.ExpiresAt) {
			delete(s.sessions, id)
			continue
		}
		if current.Role == RoleViewer && current.PairingKey == pairingKey && current.Username != "" {
			return true
		}
	}
	return false
}

func isAccountSession(current session) bool {
	return current.Username != "" && (current.Role == RoleAccount || current.Role == RoleViewer)
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
	_, current, ok := s.current(r)
	return current.Role, ok
}

func (s *sessionStore) current(r *http.Request) (string, session, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return "", session{}, false
	}
	id, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(s.sign(id))) {
		return "", session{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[id]
	if !ok || time.Now().After(current.ExpiresAt) {
		delete(s.sessions, id)
		return "", session{}, false
	}
	return id, current, true
}

func (s *sessionStore) sign(id string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
