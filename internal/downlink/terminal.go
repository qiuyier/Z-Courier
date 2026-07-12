package downlink

import (
	"context"
	"fmt"
	"time"
)

const (
	TerminalEventVersion = 1
	TerminalEventType    = "z_courier.downlink.terminal"

	TerminalReasonMaxAttempts     = "max_attempts_exceeded"
	TerminalReasonMaxAge          = "max_age_exceeded"
	TerminalReasonOperatorDiscard = "operator_discarded"
	TerminalReasonDeliveryFailed  = "delivery_failed"

	TerminalPublisherNone = "none"
	TerminalPublisherNSQ  = "nsq"

	failureReasonMaxAttempts = TerminalReasonMaxAttempts
	failureReasonMaxAge      = TerminalReasonMaxAge
)

type TerminalPublicationStatus string

const (
	TerminalPublicationDisabled  TerminalPublicationStatus = "disabled"
	TerminalPublicationPending   TerminalPublicationStatus = "pending"
	TerminalPublicationFailed    TerminalPublicationStatus = "failed"
	TerminalPublicationPublished TerminalPublicationStatus = "published"
)

// TerminalEvent is the bounded metadata envelope exported after a message
// reaches a terminal state. It intentionally has no Body field.
type TerminalEvent struct {
	Version        int           `json:"version"`
	Type           string        `json:"type"`
	EventID        string        `json:"event_id"`
	MessageID      string        `json:"message_id"`
	ClientID       string        `json:"client_id"`
	DeviceID       string        `json:"device_id"`
	MsgID          uint32        `json:"msg_id"`
	TraceID        string        `json:"trace_id,omitempty"`
	TerminalStatus MessageStatus `json:"terminal_status"`
	TerminalReason string        `json:"terminal_reason"`
	PolicyName     string        `json:"policy_name"`
	Attempts       int           `json:"attempts"`
	MessageCreated time.Time     `json:"message_created_at"`
	TerminalAt     time.Time     `json:"terminal_at"`
	GatewayNode    string        `json:"gateway_node"`
}

type TerminalRecord struct {
	Event           TerminalEvent
	Status          TerminalPublicationStatus
	PublishAttempts int
	NextAttemptAt   time.Time
	LastError       string
	PublishedAt     time.Time
	ClaimOwner      string
	ClaimUntil      time.Time
}

type TerminalTransition struct {
	Reason      string
	GatewayNode string
	At          time.Time
	Attempted   bool
	Publish     bool
}

type TerminalPublisher interface {
	Publish(context.Context, TerminalEvent) error
}

type TerminalStore interface {
	ClaimDueTerminal(context.Context, time.Time, int, string, time.Duration) ([]TerminalRecord, error)
	MarkTerminalPublished(context.Context, string, MessageStatus, time.Time) error
	MarkTerminalPublishFailed(context.Context, string, MessageStatus, string, time.Time) error
}

type TerminalPublishResult struct {
	Scanned   int
	Published int
	Failed    int
}

type TerminalPublishingConfig struct {
	Publisher         TerminalPublisher
	GatewayNode       string
	RetryDelay        time.Duration
	RetryJitter       time.Duration
	BackoffMultiplier float64
	MaxRetryDelay     time.Duration
	ClaimOwner        string
	ClaimLease        time.Duration
}

func newTerminalEvent(message Message, status MessageStatus, transition TerminalTransition) TerminalEvent {
	policyName := message.Policy.Name
	if policyName == "" {
		policyName = DefaultDeliveryPolicyName
	}
	return TerminalEvent{
		Version:        TerminalEventVersion,
		Type:           TerminalEventType,
		EventID:        terminalEventID(message.MessageID, status),
		MessageID:      message.MessageID,
		ClientID:       message.ClientID,
		DeviceID:       message.DeviceID,
		MsgID:          message.MsgID,
		TraceID:        message.TraceID,
		TerminalStatus: status,
		TerminalReason: transition.Reason,
		PolicyName:     policyName,
		Attempts:       message.Attempts,
		MessageCreated: message.CreatedAt,
		TerminalAt:     transition.At,
		GatewayNode:    transition.GatewayNode,
	}
}

func terminalEventID(messageID string, status MessageStatus) string {
	return fmt.Sprintf("%s:%s", messageID, status)
}

func terminalPublicationStatus(publish bool) TerminalPublicationStatus {
	if publish {
		return TerminalPublicationPending
	}
	return TerminalPublicationDisabled
}

func applyTerminalRecord(message *Message, record TerminalRecord) {
	message.TerminalPublishStatus = record.Status
	message.TerminalPublishAttempts = record.PublishAttempts
	message.TerminalNextPublishAt = record.NextAttemptAt
	message.TerminalPublishError = record.LastError
	message.TerminalPublishedAt = record.PublishedAt
}
