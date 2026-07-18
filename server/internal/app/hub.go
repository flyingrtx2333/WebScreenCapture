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
	role       Role
	pairingKey string
	conn       *websocket.Conn
	mu         sync.Mutex
}

func (p *peer) send(message signalMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return wsjsonWrite(ctx, p.conn, message)
}

func (p *peer) ping() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.conn.Ping(ctx)
}

func (p *peer) close(code websocket.StatusCode, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.conn.Close(code, reason)
}

type signalRoom struct {
	agent          *peer
	viewer         *peer
	currentSession string
}

type hub struct {
	mu    sync.Mutex
	rooms map[string]*signalRoom
}

func newHub() *hub { return &hub{rooms: make(map[string]*signalRoom)} }

func (h *hub) register(p *peer) {
	var replaced *peer
	var counterpart *peer
	var sessionID string

	h.mu.Lock()
	room := h.rooms[p.pairingKey]
	if room == nil {
		room = &signalRoom{}
		h.rooms[p.pairingKey] = room
	}
	if p.role == RoleAgent {
		replaced = room.agent
		room.agent = p
		counterpart = room.viewer
		sessionID = room.currentSession
	} else {
		replaced = room.viewer
		room.viewer = p
		room.currentSession = randomID(18)
		sessionID = room.currentSession
		counterpart = room.agent
	}
	h.mu.Unlock()

	if replaced != nil && replaced != p {
		replaced.close(websocket.StatusPolicyViolation, "replaced by a newer connection in the same room")
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
	room := h.rooms[p.pairingKey]
	if room != nil {
		if p.role == RoleAgent && room.agent == p {
			room.agent = nil
			counterpart = room.viewer
			sessionID = room.currentSession
		} else if p.role == RoleViewer && room.viewer == p {
			room.viewer = nil
			counterpart = room.agent
			sessionID = room.currentSession
			room.currentSession = ""
		}
		if room.agent == nil && room.viewer == nil {
			delete(h.rooms, p.pairingKey)
		}
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
	room := h.rooms[from.pairingKey]
	var target *peer
	var sessionID string
	if room != nil {
		sessionID = room.currentSession
		if from.role == RoleAgent && room.agent == from {
			target = room.viewer
		} else if from.role == RoleViewer && room.viewer == from {
			target = room.agent
		}
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

func (h *hub) status(pairingKey string) (agentOnline, viewerActive bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[pairingKey]
	return room != nil && room.agent != nil, room != nil && room.viewer != nil
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
		case "sdp.answer", "ice.candidate", "peer.stop", "ice.restart", "keyframe.request":
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
