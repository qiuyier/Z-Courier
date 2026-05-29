package downlink

import (
	"context"
	"fmt"
	"time"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
)

type SessionFinder interface {
	GetByClientDevice(clientID, deviceID string) (*session.Session, bool)
}

type Connection interface {
	SendMsg(msgID uint32, data []byte) error
}

type ConnectionFinder interface {
	Get(connID uint64) (Connection, error)
}

type Service struct {
	sessions    SessionFinder
	connections ConnectionFinder
	now         func() time.Time
}

func NewService(sessions SessionFinder, connections ConnectionFinder) *Service {
	return &Service{
		sessions:    sessions,
		connections: connections,
		now:         time.Now,
	}
}

func (s *Service) Push(_ context.Context, req PushRequest) (*PushResponse, error) {
	if err := validatePushRequest(req); err != nil {
		return nil, err
	}

	found, ok := s.sessions.GetByClientDevice(req.ClientID, req.DeviceID)
	if !ok {
		return nil, ErrSessionNotFound
	}

	conn, err := s.connections.Get(found.ConnID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnectionNotFound, err)
	}

	packet := protocol.NewPacket(req.MsgID, req.Body)
	packet.ClientID = found.ClientID
	packet.DeviceID = found.DeviceID
	packet.SessionID = found.SessionID
	packet.MessageID = req.MessageID
	packet.TraceID = req.TraceID
	packet.Timestamp = s.now().UnixMilli()
	if req.AckRequired {
		packet.Flags |= protocol.FlagAckRequired
	}

	data, err := protocol.Encode(packet)
	if err != nil {
		return nil, err
	}

	if err := conn.SendMsg(packet.MsgID, data); err != nil {
		return nil, err
	}

	return &PushResponse{
		Code:      "ok",
		ClientID:  found.ClientID,
		DeviceID:  found.DeviceID,
		SessionID: found.SessionID,
		ConnID:    found.ConnID,
		MessageID: req.MessageID,
		TraceID:   req.TraceID,
	}, nil
}

func validatePushRequest(req PushRequest) error {
	if req.ClientID == "" {
		return ErrMissingClientID
	}
	if req.DeviceID == "" {
		return ErrMissingDeviceID
	}
	if req.MsgID == 0 {
		return ErrInvalidMsgID
	}

	return nil
}
