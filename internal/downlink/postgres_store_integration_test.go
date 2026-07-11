package downlink

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresStoreIdempotentSaveIntegration(t *testing.T) {
	dsn := os.Getenv("ZCOURIER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ZCOURIER_TEST_POSTGRES_DSN to run PostgreSQL downlink store integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := NewPostgresStore(ctx, PostgresStoreConfig{
		DSN:          dsn,
		AutoMigrate:  true,
		MaxOpenConns: 10,
	})
	if err != nil {
		t.Fatalf("NewPostgresStore() error = %v", err)
	}
	defer store.Close()

	messageID := fmt.Sprintf("idempotency-integration-%d", time.Now().UnixNano())
	legacyMessageID := messageID + "-legacy"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM z_courier_downlink_messages WHERE message_id IN ($1, $2)", messageID, legacyMessageID)
	})

	policy := testDeliveryPolicy("critical")
	policy.AckTimeout = 7 * time.Second
	message := Message{
		MessageID:   messageID,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		AckRequired: true,
		TraceID:     "trace-1",
		Body:        []byte("hello"),
		Policy:      policy,
	}

	const workers = 8
	results := make(chan SaveResult, workers)
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for i := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			candidate := message.Clone()
			candidate.TraceID = fmt.Sprintf("trace-%d", index)
			result, err := store.Save(ctx, candidate)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}(i)
	}
	wait.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Fatalf("concurrent Save() error = %v", err)
	}
	created := 0
	existing := 0
	for result := range results {
		switch result.Outcome {
		case SaveOutcomeCreated:
			created++
		case SaveOutcomeExisting:
			existing++
		default:
			t.Fatalf("concurrent Save() outcome = %q", result.Outcome)
		}
	}
	if created != 1 || existing != workers-1 {
		t.Fatalf("save outcomes created=%d existing=%d, want 1/%d", created, existing, workers-1)
	}

	conflicting := message.Clone()
	conflicting.Body = []byte("different")
	conflict, err := store.Save(ctx, conflicting)
	if err != nil {
		t.Fatalf("conflicting Save() error = %v", err)
	}
	if conflict.Outcome != SaveOutcomeConflict || string(conflict.Message.Body) != "hello" {
		t.Fatalf("conflicting Save() result = %+v", conflict)
	}

	stored, ok, err := store.Get(ctx, messageID)
	if err != nil || !ok {
		t.Fatalf("Get() = ok:%v err:%v", ok, err)
	}
	if stored.Policy != policy {
		t.Fatalf("stored Policy = %+v, want %+v", stored.Policy, policy)
	}

	sentAt := time.Now().UTC().Truncate(time.Microsecond)
	ackDeadline := sentAt.Add(policy.AckTimeout)
	if err := store.MarkSent(ctx, messageID, "session-1", sentAt, ackDeadline); err != nil {
		t.Fatalf("MarkSent() error = %v", err)
	}
	before, err := store.ListDueRetry(ctx, ackDeadline.Add(-time.Millisecond), time.Millisecond, 100)
	if err != nil {
		t.Fatalf("ListDueRetry(before deadline) error = %v", err)
	}
	if containsMessageID(before, messageID) {
		t.Fatalf("message %s was due before stored ACK deadline", messageID)
	}
	due, err := store.ListDueRetry(ctx, ackDeadline, time.Hour, 100)
	if err != nil {
		t.Fatalf("ListDueRetry(at deadline) error = %v", err)
	}
	if !containsMessageID(due, messageID) {
		t.Fatalf("message %s was not due at stored ACK deadline", messageID)
	}

	legacySentAt := sentAt.Add(-2 * time.Second)
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO z_courier_downlink_messages (
  message_id, client_id, device_id, msg_id, status, ack_required,
  created_at, updated_at, sent_at
) VALUES ($1, $2, $3, $4, $5, true, $6, $6, $6)
`, legacyMessageID, "legacy-client", "legacy-device", 2001, string(MessageStatusSent), legacySentAt); err != nil {
		t.Fatalf("insert legacy message: %v", err)
	}
	legacyDue, err := store.ListDueRetry(ctx, sentAt, time.Second, 100)
	if err != nil {
		t.Fatalf("ListDueRetry(legacy) error = %v", err)
	}
	if !containsMessageID(legacyDue, legacyMessageID) {
		t.Fatalf("legacy message %s did not use fallback ACK timeout", legacyMessageID)
	}

	failedAt := sentAt.Add(time.Second)
	if err := store.MarkFailed(ctx, messageID, failureReasonMaxAge, failedAt, false); err != nil {
		t.Fatalf("MarkFailed(not attempted) error = %v", err)
	}
	failed, ok, err := store.Get(ctx, messageID)
	if err != nil || !ok {
		t.Fatalf("Get(failed) = ok:%v err:%v", ok, err)
	}
	if failed.Status != MessageStatusFailed || failed.Attempts != 1 || failed.LastError != failureReasonMaxAge {
		t.Fatalf("failed message = status:%q attempts:%d last_error:%q", failed.Status, failed.Attempts, failed.LastError)
	}

	if err := store.MarkFailed(ctx, messageID, failureReasonMaxAttempts, failedAt.Add(time.Second), true); err != nil {
		t.Fatalf("MarkFailed(attempted) error = %v", err)
	}
	failed, ok, err = store.Get(ctx, messageID)
	if err != nil || !ok {
		t.Fatalf("Get(failed after attempt) = ok:%v err:%v", ok, err)
	}
	if failed.Attempts != 2 || failed.LastError != failureReasonMaxAttempts {
		t.Fatalf("failed after attempt = attempts:%d last_error:%q", failed.Attempts, failed.LastError)
	}
}

func containsMessageID(messages []Message, messageID string) bool {
	for _, message := range messages {
		if message.MessageID == messageID {
			return true
		}
	}
	return false
}
