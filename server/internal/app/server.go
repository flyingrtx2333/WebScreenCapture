package app

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	cfg      Config
	logger   *slog.Logger
	sessions *sessionStore
	tokens   *accessTokenStore
	hub      *hub
	limiter  *loginLimiter
	assets   http.Handler
}

func NewServer(cfg Config, logger *slog.Logger) (http.Handler, error) {
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	tokens, err := newAccessTokenStore(cfg.AccessTokenHash, cfg.AccessTokenFile)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:      cfg,
		logger:   logger,
		sessions: newSessionStore(cfg.SessionSecret, cfg.SecureCookies),
		tokens:   tokens,
		hub:      newHub(),
		limiter:  newLoginLimiter(5, time.Minute),
		assets:   http.FileServer(http.FS(webRoot)),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /api/agent/session", s.agentSession)
	mux.HandleFunc("POST /api/viewer/session", s.viewerSession)
	mux.HandleFunc("POST /api/access-token/rotate", s.rotateAccessToken)
	mux.HandleFunc("GET /api/session", s.sessionInfo)
	mux.HandleFunc("DELETE /api/session", s.deleteSession)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/ice", s.ice)
	mux.HandleFunc("GET /ws", s.websocket)
	mux.HandleFunc("GET /agent", s.agentPage)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assets))
	mux.HandleFunc("GET /", s.viewerPage)
	return s.securityHeaders(mux), nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) agentSession(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || !s.tokens.verify(token) {
		writeError(w, http.StatusUnauthorized, "invalid access token")
		return
	}
	s.limiter.reset(clientIP(r))
	s.sessions.create(w, RoleAgent)
	writeJSON(w, http.StatusOK, map[string]string{"role": string(RoleAgent)})
}

func (s *Server) viewerSession(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var input struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !s.tokens.verify(input.Token) {
		writeError(w, http.StatusUnauthorized, "invalid access token")
		return
	}
	s.limiter.reset(clientIP(r))
	s.sessions.create(w, RoleViewer)
	writeJSON(w, http.StatusOK, map[string]string{"role": string(RoleViewer)})
}

func (s *Server) rotateAccessToken(w http.ResponseWriter, r *http.Request) {
	sessionID, current, ok := s.sessions.current(r)
	if !ok || current.Role != RoleViewer {
		writeError(w, http.StatusUnauthorized, "viewer authentication required")
		return
	}
	token, err := s.tokens.rotate()
	if err != nil {
		s.logger.Error("rotate access token", "error", err)
		writeError(w, http.StatusInternalServerError, "could not persist access token")
		return
	}
	s.sessions.retain(sessionID)
	s.hub.disconnectAgent("access token rotated")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	s.sessions.clear(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request) {
	role, ok := s.sessions.role(r)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": ok, "role": role})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessions.role(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	agentOnline, viewerActive := s.hub.status()
	writeJSON(w, http.StatusOK, map[string]bool{"agentOnline": agentOnline, "viewerActive": viewerActive})
}

func (s *Server) ice(w http.ResponseWriter, r *http.Request) {
	role, ok := s.sessions.role(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"iceServers": makeICEServers(s.cfg, role, time.Now())})
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	role, ok := s.sessions.role(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		s.logger.Warn("websocket rejected", "error", err, "role", role)
		return
	}
	conn.SetReadLimit(256 << 10)
	p := &peer{role: role, conn: conn}
	s.hub.register(p)
	defer func() {
		s.hub.unregister(p)
		p.close(websocket.StatusNormalClosure, "connection closed")
	}()

	windowStarted := time.Now()
	messageCount := 0
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if time.Since(windowStarted) >= time.Second {
			windowStarted = time.Now()
			messageCount = 0
		}
		messageCount++
		if messageCount > 200 {
			p.close(websocket.StatusPolicyViolation, "message rate exceeded")
			return
		}
		var message signalMessage
		if err := json.Unmarshal(data, &message); err != nil {
			p.close(websocket.StatusUnsupportedData, "invalid JSON")
			return
		}
		if err := s.hub.route(p, message); err != nil {
			p.close(websocket.StatusPolicyViolation, err.Error())
			return
		}
	}
}

func (s *Server) viewerPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveEmbedded(w, r, "web/viewer.html")
}

func (s *Server) agentPage(w http.ResponseWriter, r *http.Request) {
	s.serveEmbedded(w, r, "web/agent.html")
}

func (s *Server) serveEmbedded(w http.ResponseWriter, r *http.Request, name string) {
	data, err := webFiles.ReadFile(name)
	if err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; media-src 'self' blob:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "display-capture=(self), fullscreen=(self), microphone=(), geolocation=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(forwarded) != nil {
		return forwarded
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host
}

type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]loginWindow
	limit   int
	period  time.Duration
}

type loginWindow struct {
	started time.Time
	count   int
}

func newLoginLimiter(limit int, period time.Duration) *loginLimiter {
	return &loginLimiter{entries: make(map[string]loginWindow), limit: limit, period: period}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry := l.entries[key]
	if entry.started.IsZero() || now.Sub(entry.started) >= l.period {
		l.entries[key] = loginWindow{started: now, count: 1}
		return true
	}
	entry.count++
	l.entries[key] = entry
	return entry.count <= l.limit
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}
