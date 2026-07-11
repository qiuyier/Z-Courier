package downlink

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
)

const (
	failureReasonMaxAttempts = "max_attempts_exceeded"
	failureReasonMaxAge      = "max_age_exceeded"
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
	sessions         SessionFinder
	connections      ConnectionFinder
	store            Store
	now              func() time.Time
	retryDelay       time.Duration
	retryJitter      time.Duration
	retryJitterFunc  func(time.Duration) time.Duration
	ackTimeout       time.Duration
	retryClaimOwner  string
	retryClaimLease  time.Duration
	maxAttempts      int
	deliveryPolicies *DeliveryPolicySet

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

func WithRetryJitter(jitter time.Duration) ServiceOption {
	return func(s *Service) {
		if jitter > 0 {
			s.retryJitter = jitter
		}
	}
}

func WithAckTimeout(timeout time.Duration) ServiceOption {
	return func(s *Service) {
		if timeout > 0 {
			s.ackTimeout = timeout
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

func WithDeliveryPolicies(policies *DeliveryPolicySet) ServiceOption {
	return func(s *Service) {
		if policies != nil {
			s.deliveryPolicies = policies
		}
	}
}

func WithRetryClaim(owner string, lease time.Duration) ServiceOption {
	return func(s *Service) {
		s.retryClaimOwner = owner
		if lease > 0 {
			s.retryClaimLease = lease
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
		sessions:        sessions,
		connections:     connections,
		now:             time.Now,
		retryDelay:      30 * time.Second,
		retryJitterFunc: randomRetryJitter,
		ackTimeout:      30 * time.Second,
		retryClaimLease: 30 * time.Second,
		maxAttempts:     5,
	}
	for _, option := range options {
		option(service)
	}

	return service
}

func (s *Service) HasStore() bool {
	return s.store != nil
}

func (s *Service) Store() Store {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *Service) ConnectionFinder() ConnectionFinder {
	if s == nil {
		return nil
	}
	return s.connections
}

// DeliveryPolicy resolves the configured policy contract for a MsgID. Policy
// execution and persistence are integrated in the next V12 delivery slice.
func (s *Service) DeliveryPolicy(msgID uint32) DeliveryPolicy {
	if s != nil && s.deliveryPolicies != nil {
		return s.deliveryPolicies.Resolve(msgID)
	}
	if s == nil {
		return DeliveryPolicy{}
	}
	return DeliveryPolicy{
		Name:              DefaultDeliveryPolicyName,
		MaxAttempts:       s.maxAttempts,
		AckTimeout:        s.ackTimeout,
		InitialRetryDelay: s.retryDelay,
		BackoffMultiplier: 1,
		MaxRetryDelay:     s.retryDelay,
		RetryJitter:       s.retryJitter,
	}
}

func (s *Service) policyForMessage(message Message) DeliveryPolicy {
	if message.Policy.Name != "" && validateDeliveryPolicy(message.Policy) == nil {
		return message.Policy
	}
	return s.DeliveryPolicy(message.MsgID)
}

func (s *Service) attachResolvedPolicy(message Message) Message {
	message.Policy = s.policyForMessage(message)
	return message
}

func (s *Service) MessageStatus(ctx context.Context, messageID string) (Message, bool, error) {
	if messageID == "" {
		return Message{}, false, ErrMissingMessageID
	}
	if s.store == nil {
		return Message{}, false, ErrStoreNotConfigured
	}

	message, ok, err := s.store.Get(ctx, messageID)
	if err != nil {
		return Message{}, false, fmt.Errorf("%w: %v", ErrStore, err)
	}
	if ok {
		message = s.attachResolvedPolicy(message)
	}
	return message, ok, nil
}

func (s *Service) ListMessages(ctx context.Context, status MessageStatus, limit int) ([]Message, error) {
	result, err := s.ListMessagesPage(ctx, MessageListQuery{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func (s *Service) ListMessagesPage(ctx context.Context, query MessageListQuery) (MessageListResult, error) {
	if s.store == nil {
		return MessageListResult{}, ErrStoreNotConfigured
	}
	if !validMessageStatus(query.Status) {
		return MessageListResult{}, ErrInvalidStatus
	}

	result, err := s.store.ListByStatusPage(ctx, query)
	if err != nil {
		return MessageListResult{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	for index := range result.Messages {
		result.Messages[index] = s.attachResolvedPolicy(result.Messages[index])
	}
	return result, nil
}

func (s *Service) Requeue(ctx context.Context, messageID string) (Message, error) {
	if s.store == nil {
		return Message{}, ErrStoreNotConfigured
	}
	if messageID == "" {
		return Message{}, ErrMissingMessageID
	}

	message, ok, err := s.store.Get(ctx, messageID)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	if !ok {
		return Message{}, ErrMessageNotFound
	}
	if message.Status == MessageStatusDelivered || message.Status == MessageStatusDiscarded {
		return Message{}, ErrInvalidTransition
	}

	if err := s.store.Requeue(ctx, messageID, s.now()); err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	message, ok, err = s.store.Get(ctx, messageID)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	if !ok {
		return Message{}, ErrMessageNotFound
	}
	return s.attachResolvedPolicy(message), nil
}

func (s *Service) Discard(ctx context.Context, messageID, reason string) (Message, error) {
	if s.store == nil {
		return Message{}, ErrStoreNotConfigured
	}
	if messageID == "" {
		return Message{}, ErrMissingMessageID
	}

	message, ok, err := s.store.Get(ctx, messageID)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	if !ok {
		return Message{}, ErrMessageNotFound
	}
	if message.Status == MessageStatusDelivered {
		return Message{}, ErrInvalidTransition
	}

	if err := s.store.Discard(ctx, messageID, reason, s.now()); err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	message, ok, err = s.store.Get(ctx, messageID)
	if err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	if !ok {
		return Message{}, ErrMessageNotFound
	}
	return s.attachResolvedPolicy(message), nil
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
	message := messageFromPushRequest(req, s.now())
	message.Policy = s.DeliveryPolicy(req.MsgID)
	if s.retryClaimOwner != "" && s.retryClaimLease > 0 {
		message.ClaimOwner = s.retryClaimOwner
		message.ClaimUntil = message.CreatedAt.Add(s.retryClaimLease)
	}

	saveResult, err := s.store.Save(ctx, message)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStore, err)
	}
	message = saveResult.Message
	switch saveResult.Outcome {
	case SaveOutcomeExisting:
		return pushResponseFromStoredMessage(message, SubmissionStateExisting), nil
	case SaveOutcomeConflict:
		return nil, fmt.Errorf("%w: %s", ErrMessageIDConflict, message.MessageID)
	case SaveOutcomeCreated:
	default:
		return nil, fmt.Errorf("%w: unknown save outcome %q", ErrStore, saveResult.Outcome)
	}

	resp := &PushResponse{
		Code:            "ok",
		SubmissionState: SubmissionStateCreated,
		MessageStatus:   MessageStatusPending,
		DeliveryState:   DeliveryStateQueued,
		ClientID:        message.ClientID,
		DeviceID:        message.DeviceID,
		MessageID:       message.MessageID,
		TraceID:         message.TraceID,
	}

	sentResp, err := s.deliverOnline(ctx, pushRequestFromMessage(message))
	if err != nil {
		failedAt := s.now()
		if err := s.store.MarkAttemptFailed(
			ctx,
			message.MessageID,
			err.Error(),
			s.nextRetryAt(failedAt, message.Policy, message.Attempts),
		); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStore, err)
		}
		resp.Reason = err.Error()
		annotatePushResponseFailure(resp, err)
		return resp, nil
	}

	sentAt := s.now()
	if err := s.store.MarkSent(
		ctx,
		message.MessageID,
		sentResp.SessionID,
		sentAt,
		ackDeadline(message, message.Policy, sentAt),
	); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStore, err)
	}

	sentResp.SubmissionState = SubmissionStateCreated
	sentResp.MessageStatus = MessageStatusSent
	sentResp.DeliveryState = DeliveryStateSent
	return sentResp, nil
}

func pushResponseFromStoredMessage(message Message, submissionState string) *PushResponse {
	deliveryState := ""
	switch message.Status {
	case MessageStatusPending:
		deliveryState = DeliveryStateQueued
	case MessageStatusSent, MessageStatusDelivered:
		deliveryState = DeliveryStateSent
	}

	return &PushResponse{
		Code:            "ok",
		SubmissionState: submissionState,
		MessageStatus:   message.Status,
		DeliveryState:   deliveryState,
		ClientID:        message.ClientID,
		DeviceID:        message.DeviceID,
		SessionID:       message.SessionID,
		MessageID:       message.MessageID,
		TraceID:         message.TraceID,
	}
}

func (s *Service) RetryDue(ctx context.Context, limit int) (RetryResult, error) {
	if s.store == nil {
		return RetryResult{}, nil
	}

	startedAt := time.Now()
	now := s.now()
	messages, err := s.listRetryMessages(ctx, now, limit)
	if err != nil {
		metrics.RecordDownlinkRetryScan("failure", time.Since(startedAt))
		return RetryResult{}, fmt.Errorf("%w: %v", ErrStore, err)
	}

	result, err := s.retryMessages(ctx, messages)
	if err != nil {
		metrics.RecordDownlinkRetryMessages(result.Scanned, result.Sent, result.Queued, result.Failed)
		metrics.RecordDownlinkRetryScan("failure", time.Since(startedAt))
		return result, err
	}

	metrics.RecordDownlinkRetryMessages(result.Scanned, result.Sent, result.Queued, result.Failed)
	metrics.RecordDownlinkRetryScan("success", time.Since(startedAt))
	return result, nil
}

func (s *Service) CleanupExpired(ctx context.Context, policy RetentionPolicy) (CleanupResult, error) {
	if s.store == nil {
		return CleanupResult{}, nil
	}
	if policy.Limit <= 0 {
		policy.Limit = 1000
	}

	startedAt := time.Now()
	now := s.now()
	result := CleanupResult{}

	for _, target := range []struct {
		status MessageStatus
		ttl    time.Duration
		assign func(int)
	}{
		{
			status: MessageStatusDelivered,
			ttl:    policy.DeliveredTTL,
			assign: func(count int) { result.Delivered = count },
		},
		{
			status: MessageStatusFailed,
			ttl:    policy.FailedTTL,
			assign: func(count int) { result.Failed = count },
		},
		{
			status: MessageStatusDiscarded,
			ttl:    policy.DiscardedTTL,
			assign: func(count int) { result.Discarded = count },
		},
	} {
		if target.ttl <= 0 {
			continue
		}

		deleted, err := s.store.DeleteExpired(ctx, target.status, now.Add(-target.ttl), policy.Limit)
		if err != nil {
			metrics.RecordDownlinkCleanupStatus(string(target.status), "failure", 0)
			metrics.RecordDownlinkCleanupDuration("failure", time.Since(startedAt))
			return result, fmt.Errorf("%w: %v", ErrStore, err)
		}
		target.assign(deleted)
		metrics.RecordDownlinkCleanupStatus(string(target.status), "success", deleted)
	}

	metrics.RecordDownlinkCleanupDuration("success", time.Since(startedAt))
	return result, nil
}

func (s *Service) listRetryMessages(ctx context.Context, now time.Time, limit int) ([]Message, error) {
	claimStore, ok := s.store.(ClaimStore)
	if ok && s.retryClaimOwner != "" {
		startedAt := time.Now()
		messages, err := claimStore.ClaimDueRetry(ctx, now, s.ackTimeout, limit, s.retryClaimOwner, s.retryClaimLease)
		result := "success"
		if err != nil {
			result = "failure"
		}
		metrics.RecordDownlinkRetryClaim(s.retryClaimOwner, result, len(messages), time.Since(startedAt))
		return messages, err
	}

	return s.store.ListDueRetry(ctx, now, s.ackTimeout, limit)
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
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.DeliveredAt = deliveredAt
	message.UpdatedAt = deliveredAt
	return s.attachResolvedPolicy(message), nil
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
	policy := s.policyForMessage(message)
	now := s.now()
	if policyMaxAgeExceeded(message, policy, now) {
		if err := s.store.MarkFailed(ctx, message.MessageID, failureReasonMaxAge, now, false); err != nil {
			return "", fmt.Errorf("%w: %v", ErrStore, err)
		}
		return MessageStatusFailed, nil
	}
	if message.Attempts >= policy.MaxAttempts {
		if err := s.store.MarkFailed(ctx, message.MessageID, failureReasonMaxAttempts, now, false); err != nil {
			return "", fmt.Errorf("%w: %v", ErrStore, err)
		}
		return MessageStatusFailed, nil
	}
	if message.Status == MessageStatusSent && !s.ackTimedOut(message, policy, now) {
		return MessageStatusSent, nil
	}

	sentResp, err := s.deliverOnline(ctx, pushRequestFromMessage(message))
	if err != nil {
		failedAt := s.now()
		if policyMaxAgeExceeded(message, policy, failedAt) {
			if err := s.store.MarkFailed(ctx, message.MessageID, failureReasonMaxAge, failedAt, true); err != nil {
				return "", fmt.Errorf("%w: %v", ErrStore, err)
			}
			return MessageStatusFailed, nil
		}
		if message.Attempts+1 >= policy.MaxAttempts {
			if err := s.store.MarkFailed(ctx, message.MessageID, failureReasonMaxAttempts, failedAt, true); err != nil {
				return "", fmt.Errorf("%w: %v", ErrStore, err)
			}
			return MessageStatusFailed, nil
		}

		if err := s.store.MarkAttemptFailed(
			ctx,
			message.MessageID,
			err.Error(),
			s.nextRetryAt(failedAt, policy, message.Attempts),
		); err != nil {
			return "", fmt.Errorf("%w: %v", ErrStore, err)
		}
		return MessageStatusPending, nil
	}

	sentAt := s.now()
	if err := s.store.MarkSent(
		ctx,
		message.MessageID,
		sentResp.SessionID,
		sentAt,
		ackDeadline(message, policy, sentAt),
	); err != nil {
		return "", fmt.Errorf("%w: %v", ErrStore, err)
	}

	return MessageStatusSent, nil
}

func (s *Service) ackTimedOut(message Message, policy DeliveryPolicy, now time.Time) bool {
	if message.Status != MessageStatusSent || !message.AckRequired {
		return false
	}
	deadline := message.NextRetryAt
	if deadline.IsZero() {
		if message.SentAt.IsZero() || policy.AckTimeout <= 0 {
			return false
		}
		deadline = message.SentAt.Add(policy.AckTimeout)
	}
	return !deadline.After(now)
}

func (s *Service) nextRetryAt(now time.Time, policy DeliveryPolicy, attempts int) time.Time {
	return now.Add(retryDelayForAttempt(policy, attempts) + s.nextRetryJitter(policy.RetryJitter))
}

func retryDelayForAttempt(policy DeliveryPolicy, attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	delay := float64(policy.InitialRetryDelay) * math.Pow(policy.BackoffMultiplier, float64(attempts))
	if math.IsInf(delay, 0) || math.IsNaN(delay) || delay >= float64(policy.MaxRetryDelay) {
		return policy.MaxRetryDelay
	}
	if delay <= 0 {
		return policy.InitialRetryDelay
	}
	return time.Duration(delay)
}

func (s *Service) nextRetryJitter(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}
	jitterFunc := s.retryJitterFunc
	if jitterFunc == nil {
		jitterFunc = randomRetryJitter
	}
	jitter := jitterFunc(maxJitter)
	if jitter < 0 {
		return 0
	}
	if jitter > maxJitter {
		return maxJitter
	}
	return jitter
}

func policyMaxAgeExceeded(message Message, policy DeliveryPolicy, now time.Time) bool {
	if policy.MaxAge <= 0 || message.CreatedAt.IsZero() {
		return false
	}
	return !message.CreatedAt.Add(policy.MaxAge).After(now)
}

func ackDeadline(message Message, policy DeliveryPolicy, sentAt time.Time) time.Time {
	if !message.AckRequired || policy.AckTimeout <= 0 || sentAt.IsZero() {
		return time.Time{}
	}
	return sentAt.Add(policy.AckTimeout)
}

func randomRetryJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(max)))
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
		return nil, newDeliveryFailure(DeliveryFailureStageSession, "session_not_found", cluster.RouteEntry{
			ClientID: req.ClientID,
			DeviceID: req.DeviceID,
		}, ErrSessionNotFound)
	}

	key := cluster.RouteKey{
		ClientID: req.ClientID,
		DeviceID: req.DeviceID,
	}
	entry, ok, err := s.onlineRegistry.Lookup(ctx, key)
	if err != nil {
		return nil, newDeliveryFailure(DeliveryFailureStageRouteLookup, "registry_error", cluster.RouteEntry{
			ClientID: req.ClientID,
			DeviceID: req.DeviceID,
		}, fmt.Errorf("%w: %w", ErrRegistry, err))
	}
	if !ok {
		return nil, newDeliveryFailure(DeliveryFailureStageRouteLookup, "route_not_found", cluster.RouteEntry{
			ClientID: req.ClientID,
			DeviceID: req.DeviceID,
		}, ErrSessionNotFound)
	}

	if entry.GatewayNode == s.gatewayNode || entry.InternalAddr == "" {
		metrics.RecordClusterStaleRoute("local_or_empty_peer")
		_ = s.onlineRegistry.Unbind(ctx, key, entry.SessionID)
		return nil, newDeliveryFailure(DeliveryFailureStageRouteLookup, "stale_route", entry, ErrSessionNotFound)
	}
	if s.peerDispatcher == nil {
		metrics.RecordClusterPeerPush(entry.GatewayNode, "not_configured", 0)
		return nil, newDeliveryFailure(DeliveryFailureStagePeerDispatch, "peer_not_configured", entry, ErrPeerNotConfigured)
	}

	startedAt := time.Now()
	peerResp, err := s.peerDispatcher.Push(ctx, entry, peerPushRequestFromPushRequest(req, s.gatewayNode, entry.SessionID))
	if err != nil {
		if isStalePeerRouteError(err) {
			metrics.RecordClusterPeerPush(entry.GatewayNode, "stale_route", time.Since(startedAt))
			metrics.RecordClusterStaleRoute("peer_error")
			_ = s.onlineRegistry.Unbind(ctx, key, entry.SessionID)
		} else {
			metrics.RecordClusterPeerPush(entry.GatewayNode, "failure", time.Since(startedAt))
		}
		return nil, newDeliveryFailure(DeliveryFailureStagePeerDispatch, classifyPeerFailureCode(err), entry, fmt.Errorf("%w: %w", ErrPeerDispatch, err))
	}
	metrics.RecordClusterPeerPush(entry.GatewayNode, "success", time.Since(startedAt))

	sessionID := peerResp.SessionID
	if sessionID == "" {
		sessionID = entry.SessionID
	}

	return &PushResponse{
		Code:               nonEmpty(peerResp.Code, "ok"),
		DeliveryState:      DeliveryStateSent,
		DeliveryPath:       DeliveryPathClusterPeer,
		OriginGatewayNode:  s.gatewayNode,
		TargetGatewayNode:  entry.GatewayNode,
		TargetInternalAddr: entry.InternalAddr,
		ClientID:           nonEmpty(peerResp.ClientID, req.ClientID),
		DeviceID:           nonEmpty(peerResp.DeviceID, req.DeviceID),
		SessionID:          sessionID,
		ConnID:             peerResp.ConnID,
		MessageID:          req.MessageID,
		TraceID:            req.TraceID,
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
		Code:              "ok",
		DeliveryState:     DeliveryStateSent,
		DeliveryPath:      DeliveryPathLocal,
		TargetGatewayNode: nonEmpty(found.GatewayNode, s.gatewayNode),
		ClientID:          found.ClientID,
		DeviceID:          found.DeviceID,
		SessionID:         found.SessionID,
		ConnID:            found.ConnID,
		MessageID:         req.MessageID,
		TraceID:           req.TraceID,
	}, nil
}

type DeliveryFailure struct {
	Stage              string
	Code               string
	TargetGatewayNode  string
	TargetInternalAddr string
	cause              error
}

func (e *DeliveryFailure) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *DeliveryFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newDeliveryFailure(stage, code string, target cluster.RouteEntry, cause error) *DeliveryFailure {
	return &DeliveryFailure{
		Stage:              stage,
		Code:               code,
		TargetGatewayNode:  target.GatewayNode,
		TargetInternalAddr: target.InternalAddr,
		cause:              cause,
	}
}

func annotatePushResponseFailure(resp *PushResponse, err error) {
	if resp == nil || err == nil {
		return
	}
	failure := deliveryFailureFromError(err)
	resp.FailureStage = failure.Stage
	resp.FailureCode = failure.Code
	resp.TargetGatewayNode = nonEmpty(resp.TargetGatewayNode, failure.TargetGatewayNode)
	resp.TargetInternalAddr = nonEmpty(resp.TargetInternalAddr, failure.TargetInternalAddr)
}

func deliveryFailureFromError(err error) DeliveryFailure {
	var failure *DeliveryFailure
	if errors.As(err, &failure) && failure != nil {
		return *failure
	}
	switch {
	case errors.Is(err, ErrSessionNotFound):
		return DeliveryFailure{Stage: DeliveryFailureStageSession, Code: "session_not_found"}
	case errors.Is(err, ErrSessionMismatch):
		return DeliveryFailure{Stage: DeliveryFailureStageSession, Code: "session_mismatch"}
	case errors.Is(err, ErrConnectionNotFound):
		return DeliveryFailure{Stage: DeliveryFailureStageSession, Code: "connection_not_found"}
	case errors.Is(err, ErrPeerNotConfigured):
		return DeliveryFailure{Stage: DeliveryFailureStagePeerDispatch, Code: "peer_not_configured"}
	case errors.Is(err, ErrPeerDispatch):
		return DeliveryFailure{Stage: DeliveryFailureStagePeerDispatch, Code: "peer_error"}
	case errors.Is(err, ErrRegistry):
		return DeliveryFailure{Stage: DeliveryFailureStageRouteLookup, Code: "registry_error"}
	default:
		return DeliveryFailure{Stage: "unknown", Code: "error"}
	}
}

func classifyPeerFailureCode(err error) string {
	var httpErr *PeerPushHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "peer_auth_failed"
		case http.StatusNotFound:
			if httpErr.Code == "session_not_found" || httpErr.Code == "session_mismatch" {
				return "peer_target_not_found"
			}
			return "peer_not_found"
		case http.StatusConflict:
			return "peer_target_conflict"
		default:
			return "peer_http_error"
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "peer_timeout"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "peer_timeout"
	}
	return "peer_error"
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

func validMessageStatus(status MessageStatus) bool {
	return status.Valid()
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
