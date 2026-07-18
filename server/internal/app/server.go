package app

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
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
	cfg            Config
	logger         *slog.Logger
	sessions       *sessionStore
	hub            *hub
	limiter        *loginLimiter
	accountLimiter *loginLimiter
	authClient     *http.Client
	assets         http.Handler
}

func NewServer(cfg Config, logger *slog.Logger) (http.Handler, error) {
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:            cfg,
		logger:         logger,
		sessions:       newSessionStore(cfg.SessionSecret, cfg.SecureCookies),
		hub:            newHub(),
		limiter:        newLoginLimiter(5, time.Minute),
		accountLimiter: newLoginLimiter(5, time.Minute),
		authClient:     &http.Client{Timeout: 8 * time.Second},
		assets:         http.FileServer(http.FS(webRoot)),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /api/account/session", s.accountSession)
	mux.HandleFunc("GET /api/account/check", s.accountCheck)
	mux.HandleFunc("POST /api/agent/session", s.agentSession)
	mux.HandleFunc("POST /api/viewer/session", s.viewerSession)
	mux.HandleFunc("GET /api/session", s.sessionInfo)
	mux.HandleFunc("DELETE /api/session", s.deleteSession)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/ice", s.ice)
	mux.HandleFunc("GET /ws", s.websocket)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assets))
	mux.HandleFunc("GET /", s.viewerPage)
	return s.securityHeaders(mux), nil
}

func (s *Server) accountSession(w http.ResponseWriter, r *http.Request) {
	remoteIP := clientIP(r)
	if !s.accountLimiter.allow(remoteIP) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || len(input.Username) > 64 || input.Password == "" || len(input.Password) > 256 {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}

	body, _ := json.Marshal(map[string]string{"username": input.Username, "password": input.Password})
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.FlyingRTXAuthURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, "account service unavailable")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Real-IP", remoteIP)
	response, err := s.authClient.Do(request)
	if err != nil {
		s.logger.Warn("unified account login failed", "error", err)
		writeError(w, http.StatusBadGateway, "account service unavailable")
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		s.accountLimiter.reset(remoteIP)
		s.sessions.createAccount(w, input.Username)
		writeJSON(w, http.StatusOK, map[string]string{"username": input.Username})
	case http.StatusTooManyRequests:
		writeError(w, http.StatusTooManyRequests, "too many attempts")
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity:
		writeError(w, http.StatusUnauthorized, "invalid username or password")
	default:
		s.logger.Warn("unified account service returned an unexpected status", "status", response.StatusCode)
		writeError(w, http.StatusBadGateway, "account service unavailable")
	}
}

func (s *Server) accountCheck(w http.ResponseWriter, r *http.Request) {
	_, current, ok := s.sessions.current(r)
	if !ok || !isAccountSession(current) {
		writeError(w, http.StatusUnauthorized, "account authentication required")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if !ok {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		var input struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err == nil {
			token = input.Token
			ok = true
		}
	}
	pairingKey, valid := makePairingKey(token)
	if !ok || !valid {
		writeError(w, http.StatusBadRequest, "pairing token required")
		return
	}
	if !s.sessions.viewerAuthorized(pairingKey) {
		writeError(w, http.StatusForbidden, "pairing token is not authorized")
		return
	}
	s.limiter.reset(clientIP(r))
	s.sessions.create(w, RoleAgent, pairingKey)
	writeJSON(w, http.StatusOK, map[string]string{"role": string(RoleAgent)})
}

func (s *Server) viewerSession(w http.ResponseWriter, r *http.Request) {
	_, current, ok := s.sessions.current(r)
	if !ok || !isAccountSession(current) {
		writeError(w, http.StatusUnauthorized, "account authentication required")
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
	pairingKey, valid := makePairingKey(input.Token)
	if !valid {
		writeError(w, http.StatusBadRequest, "pairing token required")
		return
	}
	if !s.sessions.bindViewer(r, pairingKey) {
		writeError(w, http.StatusUnauthorized, "account authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"role": string(RoleViewer)})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	s.sessions.clear(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request) {
	_, current, ok := s.sessions.current(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":        ok,
		"accountAuthenticated": ok && isAccountSession(current),
		"paired":               ok && current.Role == RoleViewer && current.PairingKey != "",
		"role":                 current.Role,
		"username":             current.Username,
	})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	_, current, ok := s.sessions.current(r)
	if !ok || (current.Role != RoleViewer && current.Role != RoleAgent) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	agentOnline, viewerActive := s.hub.status(current.PairingKey)
	writeJSON(w, http.StatusOK, map[string]bool{"agentOnline": agentOnline, "viewerActive": viewerActive})
}

func (s *Server) ice(w http.ResponseWriter, r *http.Request) {
	_, current, ok := s.sessions.current(r)
	if !ok || (current.Role != RoleViewer && current.Role != RoleAgent) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if current.Role == RoleAgent && !s.sessions.viewerAuthorized(current.PairingKey) {
		writeError(w, http.StatusForbidden, "pairing token is not authorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"iceServers": makeICEServers(s.cfg, current.Role, time.Now())})
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	_, current, ok := s.sessions.current(r)
	if !ok || (current.Role != RoleViewer && current.Role != RoleAgent) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if current.Role == RoleAgent && !s.sessions.viewerAuthorized(current.PairingKey) {
		writeError(w, http.StatusForbidden, "pairing token is not authorized")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		s.logger.Warn("websocket rejected", "error", err, "role", current.Role)
		return
	}
	conn.SetReadLimit(256 << 10)
	p := &peer{role: current.Role, pairingKey: current.PairingKey, conn: conn}
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
		w.Header().Set("Permissions-Policy", "display-capture=(), fullscreen=(self), microphone=(), geolocation=()")
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

func makePairingKey(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 256 {
		return "", false
	}
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:]), true
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
