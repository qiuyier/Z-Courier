package session

import "time"

type Session struct {
	SessionID   string
	ConnID      uint64
	ClientID    string
	DeviceID    string
	TokenID     string
	GatewayNode string
	ConnectedAt time.Time
	LastSeenAt  time.Time
}

func (s Session) Clone() *Session {
	return &s
}
