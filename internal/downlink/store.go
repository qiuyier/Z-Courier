package downlink

import (
	"bytes"
	"context"
	"time"

	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
)

const (
	DeliveryStateSent                = sdkbackend.DeliveryStateSent
	DeliveryStateQueued              = sdkbackend.DeliveryStateQueued
	DeliveryPathLocal                = sdkbackend.DeliveryPathLocal
	DeliveryPathClusterPeer          = sdkbackend.DeliveryPathClusterPeer
	DeliveryFailureStageSession      = sdkbackend.DeliveryFailureStageSessionLookup
	DeliveryFailureStageRouteLookup  = sdkbackend.DeliveryFailureStageRouteLookup
	DeliveryFailureStagePeerDispatch = sdkbackend.DeliveryFailureStagePeerDispatch
	SubmissionStateCreated           = sdkbackend.SubmissionStateCreated
	SubmissionStateExisting          = sdkbackend.SubmissionStateExisting
)

type SaveOutcome string

const (
	SaveOutcomeCreated  SaveOutcome = "created"
	SaveOutcomeExisting SaveOutcome = "existing"
	SaveOutcomeConflict SaveOutcome = "conflict"
)

type SaveResult struct {
	Message Message
	Outcome SaveOutcome
}

type RetryResult struct {
	Scanned int
	Sent    int
	Queued  int
	Failed  int
}

type CleanupResult struct {
	Delivered int
	Failed    int
	Discarded int
}

func (r CleanupResult) Total() int {
	return r.Delivered + r.Failed + r.Discarded
}

type RetentionPolicy struct {
	DeliveredTTL time.Duration
	FailedTTL    time.Duration
	DiscardedTTL time.Duration
	Limit        int
}

type MessageStatus = sdkbackend.MessageStatus

const (
	MessageStatusPending   = sdkbackend.MessageStatusPending
	MessageStatusSent      = sdkbackend.MessageStatusSent
	MessageStatusDelivered = sdkbackend.MessageStatusDelivered
	MessageStatusFailed    = sdkbackend.MessageStatusFailed
	MessageStatusDiscarded = sdkbackend.MessageStatusDiscarded
)

type Message struct {
	MessageID               string
	ClientID                string
	DeviceID                string
	MsgID                   uint32
	Body                    []byte
	IdentityFingerprint     []byte
	AckRequired             bool
	TraceID                 string
	SessionID               string
	Policy                  DeliveryPolicy
	Status                  MessageStatus
	Attempts                int
	NextRetryAt             time.Time
	LastError               string
	TerminalReason          string
	TerminalAt              time.Time
	TerminalPublishStatus   TerminalPublicationStatus
	TerminalPublishAttempts int
	TerminalNextPublishAt   time.Time
	TerminalPublishError    string
	TerminalPublishedAt     time.Time
	ClaimOwner              string
	ClaimUntil              time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
	SentAt                  time.Time
	DeliveredAt             time.Time
}

type MessageListCursor struct {
	UpdatedAt time.Time
	MessageID string
}

type MessageListQuery struct {
	Status MessageStatus
	Limit  int
	Cursor MessageListCursor
}

type MessageListResult struct {
	Status     MessageStatus
	Limit      int
	Cursor     MessageListCursor
	NextCursor MessageListCursor
	HasMore    bool
	Total      int
	Messages   []Message
}

func (m Message) Clone() Message {
	m.Body = bytes.Clone(m.Body)
	m.IdentityFingerprint = bytes.Clone(m.IdentityFingerprint)
	return m
}

type Store interface {
	Save(context.Context, Message) (SaveResult, error)
	Get(context.Context, string) (Message, bool, error)
	ListByStatus(context.Context, MessageStatus, int) ([]Message, error)
	ListByStatusPage(context.Context, MessageListQuery) (MessageListResult, error)
	ListDueRetry(context.Context, time.Time, time.Duration, int) ([]Message, error)
	ListPendingByClientDevice(context.Context, string, string, int) ([]Message, error)
	MarkSent(context.Context, string, string, time.Time, time.Time) error
	MarkDelivered(context.Context, string, string, string, time.Time) error
	MarkAttemptFailed(context.Context, string, string, time.Time) error
	MarkFailed(context.Context, string, TerminalTransition) error
	Requeue(context.Context, string, time.Time) error
	Discard(context.Context, string, string, TerminalTransition) error
	DeleteExpired(context.Context, MessageStatus, time.Time, int) (int, error)
}

type ClaimStore interface {
	ClaimDueRetry(context.Context, time.Time, time.Duration, int, string, time.Duration) ([]Message, error)
}

// CapacityStore atomically applies queue admission limits with message writes.
// Store implementations may keep the legacy methods as unlimited delegates.
type CapacityStore interface {
	SaveWithCapacity(context.Context, Message, QueueCapacity) (SaveResult, error)
	RequeueWithCapacity(context.Context, string, time.Time, QueueCapacity) error
}

func messageFromPushRequest(req PushRequest, now time.Time) Message {
	messageID := req.MessageID
	if messageID == "" {
		messageID = NewMessageID()
	}

	return Message{
		MessageID:   messageID,
		ClientID:    req.ClientID,
		DeviceID:    req.DeviceID,
		MsgID:       req.MsgID,
		Body:        bytes.Clone(req.Body),
		AckRequired: req.AckRequired,
		TraceID:     req.TraceID,
		Status:      MessageStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func pushRequestFromMessage(message Message) PushRequest {
	return PushRequest{
		ClientID:    message.ClientID,
		DeviceID:    message.DeviceID,
		MsgID:       message.MsgID,
		MessageID:   message.MessageID,
		TraceID:     message.TraceID,
		AckRequired: message.AckRequired,
		Body:        bytes.Clone(message.Body),
	}
}
