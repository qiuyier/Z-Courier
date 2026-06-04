package downlink

import (
	"bytes"
	"context"
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
	message.SessionID = sessionID
	message.Status = MessageStatusSent
	message.Attempts++
	message.LastError = ""
	message.NextRetryAt = time.Time{}
	message.SentAt = sentAt
	message.UpdatedAt = s.now()
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
	message.UpdatedAt = s.now()
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) Close() error {
	return nil
}
