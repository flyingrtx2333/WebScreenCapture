package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type signalMessage struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type peer struct {
	role Role
	conn *websocket.Conn
	mu   sync.Mutex
}

func (p *peer) send(message signalMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return wsjsonWrite(ctx, p.conn, message)
}

func (p *peer) close(code websocket.StatusCode, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.conn.Close(code, reason)
}

type hub struct {
	mu             sync.Mutex
	agent          *peer
	viewer         *peer
	currentSession string
}

func newHub() *hub { return &hub{} }

func (h *hub) register(p *peer) {
	var replaced *peer
	var counterpart *peer
	var sessionID string

	h.mu.Lock()
	if p.role == RoleAgent {
		replaced = h.agent
		h.agent = p
		counterpart = h.viewer
		sessionID = h.currentSession
	} else {
		replaced = h.viewer
		h.viewer = p
		h.currentSession = randomID(18)
		sessionID = h.currentSession
		counterpart = h.agent
	}
	h.mu.Unlock()

	if replaced != nil && replaced != p {
		replaced.close(websocket.StatusPolicyViolation, "replaced by a newer authenticated connection")
	}
	rolePayload, _ := json.Marshal(map[string]string{"role": string(p.role)})
	_ = p.send(signalMessage{Type: "hello", SessionID: sessionID, Payload: rolePayload})
	if counterpart != nil && sessionID != "" {
		_ = counterpart.send(signalMessage{Type: "peer.start", SessionID: sessionID})
	}
}

func (h *hub) unregister(p *peer) {
	var counterpart *peer
	var sessionID string

	h.mu.Lock()
	if p.role == RoleAgent && h.agent == p {
		h.agent = nil
		counterpart = h.viewer
		sessionID = h.currentSession
	} else if p.role == RoleViewer && h.viewer == p {
		h.viewer = nil
		counterpart = h.agent
		sessionID = h.currentSession
		h.currentSession = ""
	}
	h.mu.Unlock()

	if counterpart != nil && sessionID != "" {
		payload, _ := json.Marshal(map[string]string{"reason": string(p.role) + " disconnected"})
		_ = counterpart.send(signalMessage{Type: "peer.stop", SessionID: sessionID, Payload: payload})
	}
}

func (h *hub) route(from *peer, message signalMessage) error {
	if !allowedSignal(from.role, message.Type) {
		return errors.New("message type is not allowed for this role")
	}

	h.mu.Lock()
	sessionID := h.currentSession
	var target *peer
	if from.role == RoleAgent && h.agent == from {
		target = h.viewer
	} else if from.role == RoleViewer && h.viewer == from {
		target = h.agent
	}
	h.mu.Unlock()

	if target == nil || sessionID == "" {
		return nil
	}
	if message.SessionID != sessionID {
		return errors.New("stale or invalid session")
	}
	return target.send(message)
}

func (h *hub) status() (agentOnline, viewerActive bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agent != nil, h.viewer != nil
}

func (h *hub) disconnectAgent(reason string) {
	var agent *peer
	var viewer *peer
	var sessionID string
	h.mu.Lock()
	agent = h.agent
	viewer = h.viewer
	sessionID = h.currentSession
	h.agent = nil
	h.mu.Unlock()

	if agent != nil && viewer != nil && sessionID != "" {
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		_ = viewer.send(signalMessage{Type: "peer.stop", SessionID: sessionID, Payload: payload})
	}
	if agent != nil {
		agent.close(websocket.StatusPolicyViolation, reason)
	}
}

func allowedSignal(role Role, messageType string) bool {
	if role == RoleAgent {
		switch messageType {
		case "sdp.offer", "ice.candidate", "peer.stop", "status":
			return true
		}
	}
	if role == RoleViewer {
		switch messageType {
		case "sdp.answer", "ice.candidate", "peer.stop":
			return true
		}
	}
	return false
}

func randomID(size int) string {
	data := make([]byte, size)
	_, _ = rand.Read(data)
	return base64.RawURLEncoding.EncodeToString(data)
}

func wsjsonWrite(ctx context.Context, conn *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
