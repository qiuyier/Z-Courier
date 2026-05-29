package session

import (
	"sync"
	"time"
)

type BindInput struct {
	SessionID   string
	ConnID      uint64
	ClientID    string
	DeviceID    string
	TokenID     string
	GatewayNode string
	Now         time.Time
}

type BindResult struct {
	Session  *Session
	Replaced *Session
}

type Manager struct {
	mu                 sync.RWMutex
	byConnID           map[uint64]*Session
	connIDBySessionID  map[string]uint64
	connIDByClientPair map[string]map[string]uint64
}

func NewManager() *Manager {
	return &Manager{
		byConnID:           make(map[uint64]*Session),
		connIDBySessionID:  make(map[string]uint64),
		connIDByClientPair: make(map[string]map[string]uint64),
	}
}

func (m *Manager) Bind(input BindInput) (*BindResult, error) {
	if input.ConnID == 0 {
		return nil, ErrInvalidConnID
	}
	if input.ClientID == "" {
		return nil, ErrEmptyClientID
	}
	if input.DeviceID == "" {
		return nil, ErrEmptyDeviceID
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing := m.byConnID[input.ConnID]; existing != nil &&
		existing.ClientID == input.ClientID &&
		existing.DeviceID == input.DeviceID {
		existing.TokenID = input.TokenID
		existing.GatewayNode = input.GatewayNode
		existing.LastSeenAt = now

		return &BindResult{
			Session: existing.Clone(),
		}, nil
	}

	m.removeConnLocked(input.ConnID)

	var replaced *Session
	if deviceIndex, ok := m.connIDByClientPair[input.ClientID]; ok {
		if oldConnID, ok := deviceIndex[input.DeviceID]; ok && oldConnID != input.ConnID {
			replaced = m.removeConnLocked(oldConnID)
		}
	}

	sessionID := input.SessionID
	if sessionID == "" {
		sessionID = NewID()
	}

	session := &Session{
		SessionID:   sessionID,
		ConnID:      input.ConnID,
		ClientID:    input.ClientID,
		DeviceID:    input.DeviceID,
		TokenID:     input.TokenID,
		GatewayNode: input.GatewayNode,
		ConnectedAt: now,
		LastSeenAt:  now,
	}

	m.byConnID[input.ConnID] = session
	m.connIDBySessionID[sessionID] = input.ConnID
	if _, ok := m.connIDByClientPair[input.ClientID]; !ok {
		m.connIDByClientPair[input.ClientID] = make(map[string]uint64)
	}
	m.connIDByClientPair[input.ClientID][input.DeviceID] = input.ConnID

	return &BindResult{
		Session:  session.Clone(),
		Replaced: cloneSession(replaced),
	}, nil
}

func (m *Manager) TouchByConnID(connID uint64, now time.Time) (*Session, error) {
	if now.IsZero() {
		now = time.Now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	found, ok := m.byConnID[connID]
	if !ok {
		return nil, ErrNotFound
	}

	found.LastSeenAt = now
	return found.Clone(), nil
}

func (m *Manager) UnbindByConnID(connID uint64) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := m.removeConnLocked(connID)
	if removed == nil {
		return nil, false
	}

	return removed.Clone(), true
}

func (m *Manager) GetByConnID(connID uint64) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	found, ok := m.byConnID[connID]
	return cloneSession(found), ok
}

func (m *Manager) GetBySessionID(sessionID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	connID, ok := m.connIDBySessionID[sessionID]
	if !ok {
		return nil, false
	}

	return cloneSession(m.byConnID[connID]), true
}

func (m *Manager) GetByClientDevice(clientID, deviceID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	deviceIndex, ok := m.connIDByClientPair[clientID]
	if !ok {
		return nil, false
	}

	connID, ok := deviceIndex[deviceID]
	if !ok {
		return nil, false
	}

	return cloneSession(m.byConnID[connID]), true
}

func (m *Manager) ListByClientID(clientID string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	deviceIndex, ok := m.connIDByClientPair[clientID]
	if !ok {
		return nil
	}

	sessions := make([]*Session, 0, len(deviceIndex))
	for _, connID := range deviceIndex {
		if found := m.byConnID[connID]; found != nil {
			sessions = append(sessions, found.Clone())
		}
	}

	return sessions
}

func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.byConnID)
}

func (m *Manager) removeConnLocked(connID uint64) *Session {
	found, ok := m.byConnID[connID]
	if !ok {
		return nil
	}

	delete(m.byConnID, connID)
	delete(m.connIDBySessionID, found.SessionID)

	if deviceIndex, ok := m.connIDByClientPair[found.ClientID]; ok {
		delete(deviceIndex, found.DeviceID)
		if len(deviceIndex) == 0 {
			delete(m.connIDByClientPair, found.ClientID)
		}
	}

	return found.Clone()
}

func cloneSession(in *Session) *Session {
	if in == nil {
		return nil
	}

	return in.Clone()
}
