package downlink

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu             sync.RWMutex
	messages       map[string]Message
	terminalEvents map[string]TerminalRecord
	now            func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		messages:       make(map[string]Message),
		terminalEvents: make(map[string]TerminalRecord),
		now:            time.Now,
	}
}

func (s *MemoryStore) Save(ctx context.Context, message Message) (SaveResult, error) {
	return s.SaveWithCapacity(ctx, message, QueueCapacity{})
}

func (s *MemoryStore) SaveWithCapacity(ctx context.Context, message Message, capacity QueueCapacity) (SaveResult, error) {
	if err := ctx.Err(); err != nil {
		return SaveResult{}, err
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
	message.IdentityFingerprint = messageIdentityFingerprint(message)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.messages[message.MessageID]; ok {
		if len(existing.IdentityFingerprint) != len(message.IdentityFingerprint) {
			existing.IdentityFingerprint = messageIdentityFingerprint(existing)
			s.messages[message.MessageID] = existing
		}
		outcome := SaveOutcomeExisting
		if !messagesHaveSameIdentity(existing, message) {
			outcome = SaveOutcomeConflict
		}
		return SaveResult{Message: existing.Clone(), Outcome: outcome}, nil
	}
	if message.Status == MessageStatusPending {
		if err := s.checkQueueCapacityLocked(message.ClientID, message.DeviceID, capacity); err != nil {
			return SaveResult{}, err
		}
	}

	s.messages[message.MessageID] = message
	return SaveResult{Message: message.Clone(), Outcome: SaveOutcomeCreated}, nil
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

func (s *MemoryStore) ListByStatus(ctx context.Context, status MessageStatus, limit int) ([]Message, error) {
	result, err := s.ListByStatusPage(ctx, MessageListQuery{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func (s *MemoryStore) ListByStatusPage(ctx context.Context, query MessageListQuery) (MessageListResult, error) {
	if err := ctx.Err(); err != nil {
		return MessageListResult{}, err
	}
	query = normalizeMessageListQuery(query)

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := MessageListResult{
		Status:   query.Status,
		Limit:    query.Limit,
		Cursor:   query.Cursor,
		Messages: make([]Message, 0, query.Limit),
	}
	messages := make([]Message, 0, query.Limit+1)
	for _, message := range s.messages {
		if message.Status != query.Status {
			continue
		}
		result.Total++
		if !messageAfterListCursor(message, query.Cursor) {
			continue
		}
		messages = append(messages, message.Clone())
	}

	messages = limitMessagesByUpdatedDesc(messages, query.Limit+1)
	if len(messages) > query.Limit {
		result.HasMore = true
		messages = messages[:query.Limit]
	}
	result.Messages = messages
	if result.HasMore && len(result.Messages) > 0 {
		result.NextCursor = messageListCursorFromMessage(result.Messages[len(result.Messages)-1])
	}
	return result, nil
}

func (s *MemoryStore) ListDuePending(ctx context.Context, now time.Time, limit int) ([]Message, error) {
	return s.ListDueRetry(ctx, now, 0, limit)
}

func (s *MemoryStore) ListDueRetry(ctx context.Context, now time.Time, ackTimeout time.Duration, limit int) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = s.now()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return limitMessages(s.retryCandidatesLocked(now, ackTimeout), limit), nil
}

func (s *MemoryStore) ListDueRetryFair(
	ctx context.Context,
	now time.Time,
	ackTimeout time.Duration,
	limit int,
	candidateLimit int,
) (RetrySelection, error) {
	if err := ctx.Err(); err != nil {
		return RetrySelection{}, err
	}
	if now.IsZero() {
		now = s.now()
	}
	limit, candidateLimit = normalizeRetrySelectionLimits(limit, candidateLimit)

	s.mu.RLock()
	defer s.mu.RUnlock()
	candidates := limitMessages(s.retryCandidatesLocked(now, ackTimeout), candidateLimit)
	return fairRetrySelection(candidates, limit), nil
}

func (s *MemoryStore) ClaimDueRetryFair(
	ctx context.Context,
	now time.Time,
	ackTimeout time.Duration,
	limit int,
	candidateLimit int,
	owner string,
	lease time.Duration,
) (RetrySelection, error) {
	if err := ctx.Err(); err != nil {
		return RetrySelection{}, err
	}
	if owner == "" {
		return s.ListDueRetryFair(ctx, now, ackTimeout, limit, candidateLimit)
	}
	if now.IsZero() {
		now = s.now()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	limit, candidateLimit = normalizeRetrySelectionLimits(limit, candidateLimit)

	s.mu.Lock()
	defer s.mu.Unlock()
	candidates := limitMessages(s.retryCandidatesLocked(now, ackTimeout), candidateLimit)
	selection := fairRetrySelection(candidates, limit)
	for index := range selection.Messages {
		message := selection.Messages[index]
		stored := s.messages[message.MessageID]
		stored.ClaimOwner = owner
		stored.ClaimUntil = now.Add(lease)
		stored.UpdatedAt = now
		s.messages[message.MessageID] = stored
		selection.Messages[index] = stored.Clone()
	}
	return selection, nil
}

func (s *MemoryStore) retryCandidatesLocked(now time.Time, ackTimeout time.Duration) []Message {
	messages := make([]Message, 0)
	for _, message := range s.messages {
		if messageDueForRetry(message, now, ackTimeout) {
			messages = append(messages, message.Clone())
		}
	}
	return messages
}

func messageDueForRetry(message Message, now time.Time, ackTimeout time.Duration) bool {
	if claimActive(message, now) {
		return false
	}

	switch message.Status {
	case MessageStatusPending:
		return message.NextRetryAt.IsZero() || !message.NextRetryAt.After(now)
	case MessageStatusSent:
		if !message.AckRequired {
			return false
		}
		if !message.NextRetryAt.IsZero() {
			return !message.NextRetryAt.After(now)
		}
		if ackTimeout <= 0 || message.SentAt.IsZero() {
			return false
		}
		return !message.SentAt.Add(ackTimeout).After(now)
	default:
		return false
	}
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
		if claimActive(message, s.now()) {
			continue
		}
		if message.ClientID != clientID || message.DeviceID != deviceID {
			continue
		}
		messages = append(messages, message.Clone())
	}

	return limitMessages(messages, limit), nil
}

func claimActive(message Message, now time.Time) bool {
	return !message.ClaimUntil.IsZero() && message.ClaimUntil.After(now)
}

func (s *MemoryStore) MarkSent(ctx context.Context, messageID, sessionID string, sentAt, nextRetryAt time.Time) error {
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
	if message.Status == MessageStatusDiscarded {
		return nil
	}
	message.SessionID = sessionID
	message.Status = MessageStatusSent
	message.Attempts++
	message.LastError = ""
	message.NextRetryAt = nextRetryAt
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
	if message.Status == MessageStatusDelivered || message.Status == MessageStatusDiscarded {
		return nil
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

func (s *MemoryStore) MarkFailed(ctx context.Context, messageID string, transition TerminalTransition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transition.At.IsZero() {
		transition.At = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	if !ok {
		return ErrMessageNotFound
	}
	if message.Status == MessageStatusDelivered || message.Status == MessageStatusDiscarded {
		return nil
	}
	firstTransition := message.Status != MessageStatusFailed || message.TerminalReason == ""
	message.Status = MessageStatusFailed
	if transition.Attempted {
		message.Attempts++
	}
	message.LastError = transition.Reason
	message.NextRetryAt = time.Time{}
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.UpdatedAt = transition.At
	if firstTransition {
		message.TerminalReason = transition.Reason
		message.TerminalAt = transition.At
		message.TerminalPublishStatus = terminalPublicationStatus(transition.Publish)
		message.TerminalPublishAttempts = 0
		message.TerminalNextPublishAt = time.Time{}
		message.TerminalPublishError = ""
		message.TerminalPublishedAt = time.Time{}
		if transition.Publish {
			event := newTerminalEvent(message, MessageStatusFailed, transition)
			record, exists := s.terminalEvents[event.EventID]
			if !exists {
				record = TerminalRecord{
					Event:  event,
					Status: TerminalPublicationPending,
				}
				s.terminalEvents[event.EventID] = record
			}
			applyTerminalRecord(&message, record)
		}
	}
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) Requeue(ctx context.Context, messageID string, requeuedAt time.Time) error {
	return s.RequeueWithCapacity(ctx, messageID, requeuedAt, QueueCapacity{})
}

func (s *MemoryStore) RequeueWithCapacity(
	ctx context.Context,
	messageID string,
	requeuedAt time.Time,
	capacity QueueCapacity,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if requeuedAt.IsZero() {
		requeuedAt = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	if !ok {
		return ErrMessageNotFound
	}
	if message.Status != MessageStatusPending {
		if err := s.checkQueueCapacityLocked(message.ClientID, message.DeviceID, capacity); err != nil {
			return err
		}
	}
	message.Status = MessageStatusPending
	message.Attempts = 0
	message.LastError = ""
	message.NextRetryAt = time.Time{}
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.SessionID = ""
	message.SentAt = time.Time{}
	message.DeliveredAt = time.Time{}
	message.TerminalReason = ""
	message.TerminalAt = time.Time{}
	message.TerminalPublishStatus = ""
	message.TerminalPublishAttempts = 0
	message.TerminalNextPublishAt = time.Time{}
	message.TerminalPublishError = ""
	message.TerminalPublishedAt = time.Time{}
	message.UpdatedAt = requeuedAt
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) checkQueueCapacityLocked(clientID, deviceID string, capacity QueueCapacity) error {
	if !capacity.Enabled() {
		return nil
	}

	globalPending := 0
	devicePending := 0
	for _, message := range s.messages {
		if message.Status != MessageStatusPending {
			continue
		}
		globalPending++
		if message.ClientID == clientID && message.DeviceID == deviceID {
			devicePending++
		}
	}
	if capacity.MaxPendingGlobal > 0 && globalPending >= capacity.MaxPendingGlobal {
		return newQueueCapacityError(QueueCapacityScopeGlobal, globalPending, capacity.MaxPendingGlobal)
	}
	if capacity.MaxPendingPerDevice > 0 && devicePending >= capacity.MaxPendingPerDevice {
		return newQueueCapacityError(QueueCapacityScopeDevice, devicePending, capacity.MaxPendingPerDevice)
	}
	return nil
}

func (s *MemoryStore) Discard(ctx context.Context, messageID, reason string, transition TerminalTransition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transition.At.IsZero() {
		transition.At = s.now()
	}
	if transition.Reason == "" {
		transition.Reason = TerminalReasonOperatorDiscard
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	message, ok := s.messages[messageID]
	if !ok {
		return ErrMessageNotFound
	}
	firstTransition := message.Status != MessageStatusDiscarded || message.TerminalReason == ""
	message.Status = MessageStatusDiscarded
	message.LastError = reason
	message.NextRetryAt = time.Time{}
	message.ClaimOwner = ""
	message.ClaimUntil = time.Time{}
	message.UpdatedAt = transition.At
	if firstTransition {
		message.TerminalReason = transition.Reason
		message.TerminalAt = transition.At
		message.TerminalPublishStatus = terminalPublicationStatus(transition.Publish)
		message.TerminalPublishAttempts = 0
		message.TerminalNextPublishAt = time.Time{}
		message.TerminalPublishError = ""
		message.TerminalPublishedAt = time.Time{}
		if transition.Publish {
			event := newTerminalEvent(message, MessageStatusDiscarded, transition)
			record, exists := s.terminalEvents[event.EventID]
			if !exists {
				record = TerminalRecord{
					Event:  event,
					Status: TerminalPublicationPending,
				}
				s.terminalEvents[event.EventID] = record
			}
			applyTerminalRecord(&message, record)
		}
	}
	s.messages[messageID] = message
	return nil
}

func (s *MemoryStore) ClaimDueTerminal(
	ctx context.Context,
	now time.Time,
	limit int,
	owner string,
	lease time.Duration,
) ([]TerminalRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = s.now()
	}
	if limit <= 0 {
		limit = 100
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]TerminalRecord, 0)
	for _, record := range s.terminalEvents {
		if record.Status != TerminalPublicationPending && record.Status != TerminalPublicationFailed {
			continue
		}
		if !record.NextAttemptAt.IsZero() && record.NextAttemptAt.After(now) {
			continue
		}
		if !record.ClaimUntil.IsZero() && record.ClaimUntil.After(now) {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].Event.TerminalAt.Equal(records[right].Event.TerminalAt) {
			return records[left].Event.EventID < records[right].Event.EventID
		}
		return records[left].Event.TerminalAt.Before(records[right].Event.TerminalAt)
	})
	if len(records) > limit {
		records = records[:limit]
	}
	for index := range records {
		records[index].ClaimOwner = owner
		records[index].ClaimUntil = now.Add(lease)
		s.terminalEvents[records[index].Event.EventID] = records[index]
	}
	return records, nil
}

func (s *MemoryStore) MarkTerminalPublished(
	ctx context.Context,
	messageID string,
	status MessageStatus,
	publishedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if publishedAt.IsZero() {
		publishedAt = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := terminalEventID(messageID, status)
	record, ok := s.terminalEvents[key]
	if !ok {
		return ErrMessageNotFound
	}
	if record.Status == TerminalPublicationPublished {
		return nil
	}
	record.Status = TerminalPublicationPublished
	record.PublishAttempts++
	record.NextAttemptAt = time.Time{}
	record.LastError = ""
	record.PublishedAt = publishedAt
	record.ClaimOwner = ""
	record.ClaimUntil = time.Time{}
	s.terminalEvents[key] = record

	if message, ok := s.messages[messageID]; ok && message.Status == status {
		message.TerminalPublishStatus = TerminalPublicationPublished
		message.TerminalPublishAttempts = record.PublishAttempts
		message.TerminalNextPublishAt = time.Time{}
		message.TerminalPublishError = ""
		message.TerminalPublishedAt = publishedAt
		s.messages[messageID] = message
	}
	return nil
}

func (s *MemoryStore) MarkTerminalPublishFailed(
	ctx context.Context,
	messageID string,
	status MessageStatus,
	reason string,
	nextAttemptAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := terminalEventID(messageID, status)
	record, ok := s.terminalEvents[key]
	if !ok {
		return ErrMessageNotFound
	}
	if record.Status == TerminalPublicationPublished {
		return nil
	}
	record.Status = TerminalPublicationFailed
	record.PublishAttempts++
	record.NextAttemptAt = nextAttemptAt
	record.LastError = reason
	record.ClaimOwner = ""
	record.ClaimUntil = time.Time{}
	s.terminalEvents[key] = record

	if message, ok := s.messages[messageID]; ok && message.Status == status {
		message.TerminalPublishStatus = TerminalPublicationFailed
		message.TerminalPublishAttempts = record.PublishAttempts
		message.TerminalNextPublishAt = nextAttemptAt
		message.TerminalPublishError = reason
		s.messages[messageID] = message
	}
	return nil
}

func (s *MemoryStore) DeleteExpired(ctx context.Context, status MessageStatus, before time.Time, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if before.IsZero() {
		return 0, nil
	}
	if limit <= 0 {
		limit = 1000
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	messages := make([]Message, 0)
	for _, message := range s.messages {
		if message.Status != status {
			continue
		}
		if s.hasUnpublishedTerminalEvent(message.MessageID) {
			continue
		}
		updatedAt := retentionTime(message)
		if updatedAt.IsZero() || !updatedAt.Before(before) {
			continue
		}
		messages = append(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool {
		left := retentionTime(messages[i])
		right := retentionTime(messages[j])
		if left.Equal(right) {
			return messages[i].MessageID < messages[j].MessageID
		}

		return left.Before(right)
	})
	if len(messages) > limit {
		messages = messages[:limit]
	}

	for _, message := range messages {
		delete(s.messages, message.MessageID)
		delete(s.terminalEvents, terminalEventID(message.MessageID, MessageStatusFailed))
		delete(s.terminalEvents, terminalEventID(message.MessageID, MessageStatusDiscarded))
	}

	return len(messages), nil
}

func (s *MemoryStore) hasUnpublishedTerminalEvent(messageID string) bool {
	for _, record := range s.terminalEvents {
		if record.Event.MessageID != messageID {
			continue
		}
		if record.Status == TerminalPublicationPending || record.Status == TerminalPublicationFailed {
			return true
		}
	}
	return false
}

func (s *MemoryStore) Close() error {
	return nil
}

func (s *MemoryStore) Ping(ctx context.Context) error {
	return ctx.Err()
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

func limitMessagesByUpdatedDesc(messages []Message, limit int) []Message {
	sort.Slice(messages, func(i, j int) bool {
		left := retentionTime(messages[i])
		right := retentionTime(messages[j])
		if left.Equal(right) {
			return messages[i].MessageID < messages[j].MessageID
		}

		return left.After(right)
	})
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
	}

	return messages
}

func retentionTime(message Message) time.Time {
	if !message.UpdatedAt.IsZero() {
		return message.UpdatedAt
	}

	return message.CreatedAt
}
