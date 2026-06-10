package downlink

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/qiuyier/Z-Courier/internal/cluster"
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
	maxAttempts int

	gatewayNode    string
	onlineRegistry cluster.OnlineRegistry
	peerDispatcher PeerDispatcher
}

type ServiceOption func(*Service)

type ClusterDeliveryConfig struct {
	GatewayNode    string
	Registry       cluster.OnlineRegistry
	PeerDispatcher PeerDispatcher
}

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

func WithMaxAttempts(maxAttempts int) ServiceOption {
	return func(s *Service) {
		if maxAttempts > 0 {
			s.maxAttempts = maxAttempts
		}
	}
}

func WithClusterDelivery(config ClusterDeliveryConfig) ServiceOption {
	return func(s *Service) {
		s.gatewayNode = config.GatewayNode
		s.onlineRegistry = config.Registry
		s.peerDispatcher = config.PeerDispatcher
	}
}

func NewService(sessions SessionFinder, connections ConnectionFinder, options ...ServiceOption) *Service {
	service := &Service{
		sessions:    sessions,
		connections: connections,
		now:         time.Now,
		retryDelay:  30 * time.Second,
		maxAttempts: 5,
	}
	for _, option := range options {
		option(service)
	}

	return service
}

func (s *Service) HasStore() bool {
	return s.store != nil
}

func (s *Service) Push(ctx context.Context, req PushRequest) (*PushResponse, error) {
	if err := validatePushRequest(req); err != nil {
		return nil, err
	}

	if s.store != nil {
		return s.pushReliable(ctx, req)
	}

	return s.deliverOnline(ctx, req)
}

func (s *Service) PushPeer(ctx context.Context, req PeerPushRequest, gatewayNode string) (*PeerPushResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePeerPushRequest(req); err != nil {
		return nil, err
	}

	resp, err := s.pushOnlineWithSession(pushRequestFromPeerPushRequest(req), req.SessionID)
	if err != nil {
		return nil, err
	}

	return &PeerPushResponse{
		Code:          "ok",
		DeliveryState: DeliveryStateSent,
		GatewayNode:   gatewayNode,
		ClientID:      resp.ClientID,
		DeviceID:      resp.DeviceID,
		SessionID:     resp.SessionID,
		ConnID:        resp.ConnID,
		MessageID:     resp.MessageID,
		TraceID:       resp.TraceID,
	}, nil
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

	sentResp, err := s.deliverOnline(ctx, pushRequestFromMessage(message))
	if err != nil {
		if err := s.store.MarkAttemptFailed(ctx, message.MessageID, err.Error(), s.now().Add(s.retryDelay)); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStore, err)
		}
		return resp, nil
	}

	if err := s.store.MarkSent(ctx, message.MessageID, sentResp.SessionID, s.now()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStore, err)
	}

	sentResp.DeliveryState = DeliveryStateSent
	return sentResp, nil
}

func (s *Service) RetryDue(ctx context.Context, limit int) (RetryResult, error) {
	if s.store == nil {
		return RetryResult{}, nil
	}

	messages, err := s.store.ListDuePending(ctx, s.now(), limit)
	if err != nil {
		return RetryResult{}, fmt.Errorf("%w: %v", ErrStore, err)
	}

	return s.retryMessages(ctx, messages)
}

func (s *Service) FlushClientDevice(ctx context.Context, clientID, deviceID string, limit int) (RetryResult, error) {
	if s.store == nil {
		return RetryResult{}, nil
	}
	if clientID == "" {
		return RetryResult{}, ErrMissingClientID
	}
	if deviceID == "" {
		return RetryResult{}, ErrMissingDeviceID
	}

	messages, err := s.store.ListPendingByClientDevice(ctx, clientID, deviceID, limit)
	if err != nil {
		return RetryResult{}, fmt.Errorf("%w: %v", ErrStore, err)
	}

	return s.retryMessages(ctx, messages)
}

func (s *Service) Ack(ctx context.Context, clientID, deviceID string, req ClientAckRequest) (Message, error) {
	if s.store == nil {
		return Message{}, ErrStoreNotConfigured
	}
	if clientID == "" {
		return Message{}, ErrMissingClientID
	}
	if deviceID == "" {
		return Message{}, ErrMissingDeviceID
	}
	if req.MessageID == "" {
		return Message{}, ErrMissingMessageID
	}
	if req.Code != ClientAckCodeDelivered {
		return Message{}, ErrInvalidAckCode
	}

	message, ok, err := s.store.Get(ctx, req.MessageID)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	if !ok || message.ClientID != clientID || message.DeviceID != deviceID {
		return Message{}, ErrMessageNotFound
	}

	deliveredAt := s.now()
	if err := s.store.MarkDelivered(ctx, req.MessageID, clientID, deviceID, deliveredAt); err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}

	message.Status = MessageStatusDelivered
	message.LastError = ""
	message.NextRetryAt = time.Time{}
	message.DeliveredAt = deliveredAt
	message.UpdatedAt = deliveredAt
	return message, nil
}

func (s *Service) retryMessages(ctx context.Context, messages []Message) (RetryResult, error) {
	var result RetryResult
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		result.Scanned++
		status, err := s.retryMessage(ctx, message)
		if err != nil {
			return result, err
		}
		switch status {
		case MessageStatusSent:
			result.Sent++
		case MessageStatusFailed:
			result.Failed++
		default:
			result.Queued++
		}
	}

	return result, nil
}

func (s *Service) retryMessage(ctx context.Context, message Message) (MessageStatus, error) {
	sentResp, err := s.deliverOnline(ctx, pushRequestFromMessage(message))
	if err != nil {
		if s.maxAttempts > 0 && message.Attempts+1 >= s.maxAttempts {
			if err := s.store.MarkFailed(ctx, message.MessageID, err.Error(), s.now()); err != nil {
				return "", fmt.Errorf("%w: %v", ErrStore, err)
			}
			return MessageStatusFailed, nil
		}

		if err := s.store.MarkAttemptFailed(ctx, message.MessageID, err.Error(), s.now().Add(s.retryDelay)); err != nil {
			return "", fmt.Errorf("%w: %v", ErrStore, err)
		}
		return MessageStatusPending, nil
	}

	if err := s.store.MarkSent(ctx, message.MessageID, sentResp.SessionID, s.now()); err != nil {
		return "", fmt.Errorf("%w: %v", ErrStore, err)
	}

	return MessageStatusSent, nil
}

func (s *Service) pushOnline(req PushRequest) (*PushResponse, error) {
	return s.pushOnlineWithSession(req, "")
}

func (s *Service) deliverOnline(ctx context.Context, req PushRequest) (*PushResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	resp, err := s.pushOnline(req)
	if err == nil {
		return resp, nil
	}
	if !errors.Is(err, ErrSessionNotFound) {
		return nil, err
	}

	return s.pushCluster(ctx, req)
}

func (s *Service) pushCluster(ctx context.Context, req PushRequest) (*PushResponse, error) {
	if s.onlineRegistry == nil {
		return nil, ErrSessionNotFound
	}

	key := cluster.RouteKey{
		ClientID: req.ClientID,
		DeviceID: req.DeviceID,
	}
	entry, ok, err := s.onlineRegistry.Lookup(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRegistry, err)
	}
	if !ok {
		return nil, ErrSessionNotFound
	}

	if entry.GatewayNode == s.gatewayNode || entry.InternalAddr == "" {
		_ = s.onlineRegistry.Unbind(ctx, key, entry.SessionID)
		return nil, ErrSessionNotFound
	}
	if s.peerDispatcher == nil {
		return nil, ErrPeerNotConfigured
	}

	peerResp, err := s.peerDispatcher.Push(ctx, entry, peerPushRequestFromPushRequest(req, s.gatewayNode, entry.SessionID))
	if err != nil {
		if isStalePeerRouteError(err) {
			_ = s.onlineRegistry.Unbind(ctx, key, entry.SessionID)
		}
		return nil, fmt.Errorf("%w: %w", ErrPeerDispatch, err)
	}

	sessionID := peerResp.SessionID
	if sessionID == "" {
		sessionID = entry.SessionID
	}

	return &PushResponse{
		Code:          nonEmpty(peerResp.Code, "ok"),
		DeliveryState: DeliveryStateSent,
		ClientID:      nonEmpty(peerResp.ClientID, req.ClientID),
		DeviceID:      nonEmpty(peerResp.DeviceID, req.DeviceID),
		SessionID:     sessionID,
		ConnID:        peerResp.ConnID,
		MessageID:     req.MessageID,
		TraceID:       req.TraceID,
	}, nil
}

func (s *Service) pushOnlineWithSession(req PushRequest, expectedSessionID string) (*PushResponse, error) {
	found, ok := s.sessions.GetByClientDevice(req.ClientID, req.DeviceID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if expectedSessionID != "" && found.SessionID != expectedSessionID {
		return nil, ErrSessionMismatch
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

func pushRequestFromPeerPushRequest(req PeerPushRequest) PushRequest {
	return PushRequest{
		ClientID:    req.ClientID,
		DeviceID:    req.DeviceID,
		MsgID:       req.MsgID,
		MessageID:   req.MessageID,
		TraceID:     req.TraceID,
		AckRequired: req.AckRequired,
		Body:        append([]byte(nil), req.Body...),
	}
}

func peerPushRequestFromPushRequest(req PushRequest, originNode, sessionID string) PeerPushRequest {
	return PeerPushRequest{
		OriginNode:  originNode,
		ClientID:    req.ClientID,
		DeviceID:    req.DeviceID,
		SessionID:   sessionID,
		MsgID:       req.MsgID,
		MessageID:   req.MessageID,
		TraceID:     req.TraceID,
		AckRequired: req.AckRequired,
		Body:        append([]byte(nil), req.Body...),
	}
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

func isStalePeerRouteError(err error) bool {
	if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionMismatch) {
		return true
	}

	var httpErr *PeerPushHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode != http.StatusNotFound {
		return false
	}

	return httpErr.Code == "session_not_found" || httpErr.Code == "session_mismatch"
}

func validatePeerPushRequest(req PeerPushRequest) error {
	if err := validatePushRequest(pushRequestFromPeerPushRequest(req)); err != nil {
		return err
	}
	if req.SessionID == "" {
		return ErrMissingSessionID
	}

	return nil
}
