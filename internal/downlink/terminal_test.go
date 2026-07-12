package downlink

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

func TestTerminalEventEnvelopeExcludesBody(t *testing.T) {
	event := newTerminalEvent(Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		TraceID:   "trace-1",
		Body:      []byte("secret business body"),
		Policy:    testDeliveryPolicy("critical"),
		Attempts:  3,
		CreatedAt: time.Unix(1, 0),
	}, MessageStatusFailed, TerminalTransition{
		Reason:      TerminalReasonMaxAttempts,
		GatewayNode: "gateway-a",
		At:          time.Unix(2, 0),
	})

	body, err := sonic.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if bytes.Contains(body, []byte("secret business body")) || bytes.Contains(body, []byte(`"body"`)) {
		t.Fatalf("terminal event leaked body: %s", body)
	}
	if event.EventID != "message-1:failed" || event.PolicyName != "critical" {
		t.Fatalf("event = %+v", event)
	}
}

func TestMemoryStoreTerminalOutboxKeepsDistinctTerminalStates(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Policy:    testDeliveryPolicy("critical"),
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := store.MarkFailed(context.Background(), "message-1", TerminalTransition{
		Reason:      TerminalReasonMaxAttempts,
		GatewayNode: "gateway-a",
		At:          now,
		Attempted:   true,
		Publish:     true,
	}); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}
	if err := store.Discard(context.Background(), "message-1", "operator decision", TerminalTransition{
		Reason:      TerminalReasonOperatorDiscard,
		GatewayNode: "gateway-b",
		At:          now.Add(time.Second),
		Publish:     true,
	}); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}

	records, err := store.ClaimDueTerminal(context.Background(), now.Add(time.Second), 10, "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueTerminal() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %+v, want failed and discarded events", records)
	}
	if records[0].Event.TerminalStatus != MessageStatusFailed || records[1].Event.TerminalStatus != MessageStatusDiscarded {
		t.Fatalf("terminal statuses = %q/%q", records[0].Event.TerminalStatus, records[1].Event.TerminalStatus)
	}

	message, ok, err := store.Get(context.Background(), "message-1")
	if err != nil || !ok {
		t.Fatalf("Get() = ok:%v err:%v", ok, err)
	}
	if message.TerminalReason != TerminalReasonOperatorDiscard || message.TerminalPublishStatus != TerminalPublicationPending {
		t.Fatalf("message terminal summary = %+v", message)
	}
}

func TestServiceTerminalPublisherRetriesIndependently(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }
	publisher := &fakeTerminalPublisher{err: errors.New("nsq unavailable")}
	service := NewService(fakeSessions{}, fakeConnections{},
		WithStore(store),
		WithTerminalPublishing(TerminalPublishingConfig{
			Publisher:         publisher,
			GatewayNode:       "gateway-a",
			RetryDelay:        time.Second,
			BackoffMultiplier: 2,
			MaxRetryDelay:     5 * time.Second,
			ClaimOwner:        "gateway-a",
			ClaimLease:        time.Minute,
		}),
	)
	service.now = func() time.Time { return now }
	service.retryJitterFunc = func(time.Duration) time.Duration { return 0 }

	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Policy:    testDeliveryPolicy("critical"),
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkFailed(context.Background(), "message-1", service.terminalTransition(TerminalReasonMaxAttempts, now, true)); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}

	result, err := service.PublishTerminalDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("PublishTerminalDue(failure) error = %v", err)
	}
	if result.Scanned != 1 || result.Published != 0 || result.Failed != 1 {
		t.Fatalf("failure result = %+v", result)
	}
	message, _, _ := store.Get(context.Background(), "message-1")
	if message.Status != MessageStatusFailed || message.TerminalPublishStatus != TerminalPublicationFailed ||
		message.TerminalPublishAttempts != 1 || !message.TerminalNextPublishAt.Equal(now.Add(time.Second)) {
		t.Fatalf("message after publisher failure = %+v", message)
	}

	service.now = func() time.Time { return now.Add(time.Second) }
	store.now = service.now
	publisher.err = nil
	result, err = service.PublishTerminalDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("PublishTerminalDue(success) error = %v", err)
	}
	if result.Scanned != 1 || result.Published != 1 || result.Failed != 0 {
		t.Fatalf("success result = %+v", result)
	}
	message, _, _ = store.Get(context.Background(), "message-1")
	if message.TerminalPublishStatus != TerminalPublicationPublished || message.TerminalPublishAttempts != 2 || message.TerminalPublishedAt.IsZero() {
		t.Fatalf("message after publisher success = %+v", message)
	}
	if len(publisher.events) != 2 || publisher.events[0].MessageID != "message-1" {
		t.Fatalf("published events = %+v", publisher.events)
	}
}

func TestMemoryStoreTerminalPublicationDoesNotRegressAfterPublished(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkFailed(context.Background(), "message-1", TerminalTransition{
		Reason:  TerminalReasonMaxAttempts,
		At:      now,
		Publish: true,
	}); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}
	if err := store.MarkTerminalPublished(context.Background(), "message-1", MessageStatusFailed, now.Add(time.Second)); err != nil {
		t.Fatalf("MarkTerminalPublished() error = %v", err)
	}
	if err := store.MarkTerminalPublishFailed(
		context.Background(),
		"message-1",
		MessageStatusFailed,
		"stale worker failure",
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("MarkTerminalPublishFailed() error = %v", err)
	}

	message, _, _ := store.Get(context.Background(), "message-1")
	if message.TerminalPublishStatus != TerminalPublicationPublished ||
		message.TerminalPublishAttempts != 1 ||
		message.TerminalPublishError != "" {
		t.Fatalf("message terminal publication regressed = %+v", message)
	}
}

func TestMemoryStoreRepeatedTerminalStateReusesPublishedEvent(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	transition := TerminalTransition{Reason: TerminalReasonMaxAttempts, At: now, Publish: true}
	if err := store.MarkFailed(context.Background(), "message-1", transition); err != nil {
		t.Fatalf("MarkFailed(first) error = %v", err)
	}
	if err := store.MarkTerminalPublished(context.Background(), "message-1", MessageStatusFailed, now.Add(time.Second)); err != nil {
		t.Fatalf("MarkTerminalPublished() error = %v", err)
	}
	if err := store.Requeue(context.Background(), "message-1", now.Add(2*time.Second)); err != nil {
		t.Fatalf("Requeue() error = %v", err)
	}
	transition.At = now.Add(3 * time.Second)
	if err := store.MarkFailed(context.Background(), "message-1", transition); err != nil {
		t.Fatalf("MarkFailed(second) error = %v", err)
	}

	message, _, _ := store.Get(context.Background(), "message-1")
	if message.TerminalPublishStatus != TerminalPublicationPublished || message.TerminalPublishAttempts != 1 {
		t.Fatalf("reused publication summary = %+v", message)
	}
	records, err := store.ClaimDueTerminal(context.Background(), now.Add(time.Hour), 10, "worker", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueTerminal() error = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v, want no duplicate failed event", records)
	}
}

func TestMemoryStoreRetentionWaitsForEveryTerminalEvent(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	createdAt := now.Add(-3 * time.Hour)
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkFailed(context.Background(), "message-1", TerminalTransition{
		Reason:  TerminalReasonMaxAttempts,
		At:      now.Add(-2 * time.Hour),
		Publish: true,
	}); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}
	if err := store.Discard(context.Background(), "message-1", "operator", TerminalTransition{
		Reason:  TerminalReasonOperatorDiscard,
		At:      now.Add(-2*time.Hour + time.Second),
		Publish: true,
	}); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	if err := store.MarkTerminalPublished(context.Background(), "message-1", MessageStatusDiscarded, now); err != nil {
		t.Fatalf("publish discarded event: %v", err)
	}

	deleted, err := store.DeleteExpired(context.Background(), MessageStatusDiscarded, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("DeleteExpired(blocked) error = %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 while failed event is pending", deleted)
	}
	if err := store.MarkTerminalPublished(context.Background(), "message-1", MessageStatusFailed, now); err != nil {
		t.Fatalf("publish failed event: %v", err)
	}
	deleted, err = store.DeleteExpired(context.Background(), MessageStatusDiscarded, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("DeleteExpired() error = %v", err)
	}
	if deleted != 1 || len(store.terminalEvents) != 0 {
		t.Fatalf("deleted = %d terminal_events = %d, want message and events removed", deleted, len(store.terminalEvents))
	}
}

type fakeTerminalPublisher struct {
	events []TerminalEvent
	err    error
}

func (p *fakeTerminalPublisher) Publish(_ context.Context, event TerminalEvent) error {
	p.events = append(p.events, event)
	return p.err
}
