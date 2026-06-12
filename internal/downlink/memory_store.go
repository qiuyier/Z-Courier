package downlink

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu       sync.RWMutex
	messages map[string]Message
	now      func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		messages: make(map[string]Message),
		now:      time.Now,
	}
}

func (s *MemoryStore) Save(ctx context.Context, message Message) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	if message.MessageID == "" {
		message.MessageID = NewMessageID()
	}
	now := s.now()
	if message.Status == "" {
		message.Status = MessageStatusPending
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	if message.UpdatedAt.IsZero() {
		message.UpdatedAt = now
	}
	message.Body = bytes.Clone(message.Body)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.messages[message.MessageID]; ok {
		return existing.Clone(), nil
	}

	s.messages[message.MessageID] = message
	return message.Clone(), nil
}

func (s *MemoryStore) Get(ctx context.Context, messageID string) (Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	message, ok := s.messages[messageID]
	if !ok {
		return Message{}, false, nil
	}

	return message.Clone(), true, nil
}

func (s *MemoryStore) ListDuePending(ctx context.Context, now time.Time, limit int) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = s.now()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	messages := make([]Message, 0)
	for _, message := range s.messages {
		if message.Status != MessageStatusPending {
			continue
		}
		if !message.NextRetryAt.IsZero() && message.NextRetryAt.After(now) {
			continue
		}
		messages = append(messages, message.Clone())
	}

	return limitMessages(messages, limit), nil
}

func (s *MemoryStore) ListPendingByClientDevice(ctx context.Context, clientID, deviceID string, limit int) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	messages := make([]Message, 0)
	for _, message := range s.messages {
		if message.Status != MessageStatusPending {
			continue
		}
		if message.ClientID != clientID || message.DeviceID != deviceID {
			continue
		}
		messages = append(messages, message.Clone())
	}

	return limitMessages(messages, limit), nil
}

func (s *MemoryStore) MarkSent(ctx context.Context, messageID, sessionID string, sentAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sentAt.IsZero() {
		sentAt = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	if !ok {
		return ErrMessageNotFound
	}
	if message.Status == MessageStatusDelivered {
		return nil
	}
	message.SessionID = sessionID
	message.Status = MessageStatusSent
	message.Attempts++
	message.LastError = ""
	message.NextRetryAt = time.Time{}
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.SentAt = sentAt
	message.UpdatedAt = s.now()
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) MarkDelivered(ctx context.Context, messageID, clientID, deviceID string, deliveredAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deliveredAt.IsZero() {
		deliveredAt = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	if !ok || message.ClientID != clientID || message.DeviceID != deviceID {
		return ErrMessageNotFound
	}
	message.Status = MessageStatusDelivered
	message.LastError = ""
	message.NextRetryAt = time.Time{}
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.DeliveredAt = deliveredAt
	message.UpdatedAt = deliveredAt
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) MarkAttemptFailed(ctx context.Context, messageID, reason string, nextRetryAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	if !ok {
		return ErrMessageNotFound
	}
	message.Status = MessageStatusPending
	message.Attempts++
	message.LastError = reason
	message.NextRetryAt = nextRetryAt
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.UpdatedAt = s.now()
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) MarkFailed(ctx context.Context, messageID, reason string, failedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if failedAt.IsZero() {
		failedAt = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	if !ok {
		return ErrMessageNotFound
	}
	message.Status = MessageStatusFailed
	message.Attempts++
	message.LastError = reason
	message.NextRetryAt = time.Time{}
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.UpdatedAt = failedAt
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func limitMessages(messages []Message, limit int) []Message {
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].CreatedAt.Equal(messages[j].CreatedAt) {
			return messages[i].MessageID < messages[j].MessageID
		}

		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
	}

	return messages
}
