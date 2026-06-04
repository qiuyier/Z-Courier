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
	store       Store
	now         func() time.Time
	retryDelay  time.Duration
}

type ServiceOption func(*Service)

func WithStore(store Store) ServiceOption {
	return func(s *Service) {
		s.store = store
	}
}

func WithRetryDelay(delay time.Duration) ServiceOption {
	return func(s *Service) {
		if delay > 0 {
			s.retryDelay = delay
		}
	}
}

func NewService(sessions SessionFinder, connections ConnectionFinder, options ...ServiceOption) *Service {
	service := &Service{
		sessions:    sessions,
		connections: connections,
		now:         time.Now,
		retryDelay:  30 * time.Second,
	}
	for _, option := range options {
		option(service)
	}

	return service
}

func (s *Service) Push(ctx context.Context, req PushRequest) (*PushResponse, error) {
	if err := validatePushRequest(req); err != nil {
		return nil, err
	}

	if s.store != nil {
		return s.pushReliable(ctx, req)
	}

	return s.pushOnline(req)
}

func (s *Service) pushReliable(ctx context.Context, req PushRequest) (*PushResponse, error) {
	message, err := s.store.Save(ctx, messageFromPushRequest(req, s.now()))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStore, err)
	}

	resp := &PushResponse{
		Code:          "ok",
		DeliveryState: DeliveryStateQueued,
		ClientID:      message.ClientID,
		DeviceID:      message.DeviceID,
		MessageID:     message.MessageID,
		TraceID:       message.TraceID,
	}

	sentResp, err := s.pushOnline(pushRequestFromMessage(message))
	if err != nil {
		_ = s.store.MarkAttemptFailed(ctx, message.MessageID, err.Error(), s.now().Add(s.retryDelay))
		return resp, nil
	}

	if err := s.store.MarkSent(ctx, message.MessageID, sentResp.SessionID, s.now()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStore, err)
	}

	sentResp.DeliveryState = DeliveryStateSent
	return sentResp, nil
}

func (s *Service) pushOnline(req PushRequest) (*PushResponse, error) {
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
		Code:          "ok",
		DeliveryState: DeliveryStateSent,
		ClientID:      found.ClientID,
		DeviceID:      found.DeviceID,
		SessionID:     found.SessionID,
		ConnID:        found.ConnID,
		MessageID:     req.MessageID,
		TraceID:       req.TraceID,
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
