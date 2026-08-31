package control

import (
	"bytes"
	"errors"
	"sync"
)

var (
	ErrCapacity     = errors.New("agent control capacity reached")
	ErrOffline      = errors.New("agent control session is offline")
	ErrBackpressure = errors.New("agent control send queue is full")
)

type Session interface {
	SessionID() string
	Outbound() chan<- []byte
	Close(reason string)
}

type Hub struct {
	mu        sync.RWMutex
	sessions  map[string]Session
	maxAgents int
	queueSize int
}

func NewHub(maxAgents, queueSize int) *Hub {
	if maxAgents <= 0 {
		maxAgents = 1000
	}
	if queueSize <= 0 {
		queueSize = 32
	}
	return &Hub{sessions: make(map[string]Session), maxAgents: maxAgents, queueSize: queueSize}
}

func (h *Hub) Register(agentID string, session Session) error {
	if agentID == "" || session == nil || session.SessionID() == "" {
		return errors.New("agent ID and control session are required")
	}
	h.mu.Lock()
	old := h.sessions[agentID]
	if old == nil && len(h.sessions) >= h.maxAgents {
		h.mu.Unlock()
		return ErrCapacity
	}
	h.sessions[agentID] = session
	h.mu.Unlock()
	if old != nil && old.SessionID() != session.SessionID() {
		old.Close("replaced")
	}
	return nil
}

func (h *Hub) Unregister(agentID, sessionID string) {
	h.mu.Lock()
	current := h.sessions[agentID]
	if current != nil && current.SessionID() == sessionID {
		delete(h.sessions, agentID)
	}
	h.mu.Unlock()
}

func (h *Hub) Disconnect(agentID, reason string) bool {
	h.mu.Lock()
	session := h.sessions[agentID]
	if session != nil {
		delete(h.sessions, agentID)
	}
	h.mu.Unlock()
	if session == nil {
		return false
	}
	session.Close(reason)
	return true
}

func (h *Hub) Send(agentID string, message []byte) error {
	h.mu.RLock()
	session := h.sessions[agentID]
	h.mu.RUnlock()
	if session == nil {
		return ErrOffline
	}
	outbound := session.Outbound()
	copyOfMessage := bytes.Clone(message)
	select {
	case outbound <- copyOfMessage:
		return nil
	default:
		return ErrBackpressure
	}
}

func (h *Hub) IsOnline(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[agentID] != nil
}

func (h *Hub) SessionID(agentID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if session := h.sessions[agentID]; session != nil {
		return session.SessionID()
	}
	return ""
}

func (h *Hub) Snapshot() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]string, len(h.sessions))
	for agentID, session := range h.sessions {
		out[agentID] = session.SessionID()
	}
	return out
}

func (h *Hub) QueueSize() int { return h.queueSize }
