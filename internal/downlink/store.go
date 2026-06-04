package downlink

import (
	"bytes"
	"context"
	"time"
)

const (
	DeliveryStateSent   = "sent"
	DeliveryStateQueued = "queued"
)

type MessageStatus string

const (
	MessageStatusPending   MessageStatus = "pending"
	MessageStatusSent      MessageStatus = "sent"
	MessageStatusDelivered MessageStatus = "delivered"
	MessageStatusFailed    MessageStatus = "failed"
)

type Message struct {
	MessageID   string
	ClientID    string
	DeviceID    string
	MsgID       uint32
	Body        []byte
	AckRequired bool
	TraceID     string
	SessionID   string
	Status      MessageStatus
	Attempts    int
	NextRetryAt time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	SentAt      time.Time
	DeliveredAt time.Time
}

func (m Message) Clone() Message {
	m.Body = bytes.Clone(m.Body)
	return m
}

type Store interface {
	Save(context.Context, Message) (Message, error)
	Get(context.Context, string) (Message, bool, error)
	MarkSent(context.Context, string, string, time.Time) error
	MarkAttemptFailed(context.Context, string, string, time.Time) error
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
