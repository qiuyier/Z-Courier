package downlink

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresStoreV11SchemaUpgradeAndRollbackCompatibilityIntegration(t *testing.T) {
	dsn := os.Getenv("ZCOURIER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ZCOURIER_TEST_POSTGRES_DSN to run PostgreSQL downlink migration integration test")
	}
	dsn = isolatedPostgresTestDSN(t, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	legacyDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open V11 PostgreSQL schema: %v", err)
	}
	defer legacyDB.Close()

	if _, err := legacyDB.ExecContext(ctx, `
CREATE TABLE z_courier_downlink_messages (
  message_id TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  msg_id INTEGER NOT NULL,
  body BYTEA NOT NULL DEFAULT ''::bytea,
  ack_required BOOLEAN NOT NULL DEFAULT false,
  trace_id TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  sent_at TIMESTAMPTZ,
  delivered_at TIMESTAMPTZ,
  claim_owner TEXT NOT NULL DEFAULT '',
  claim_until TIMESTAMPTZ
)
`); err != nil {
		t.Fatalf("create V11 PostgreSQL schema: %v", err)
	}

	legacyMessageID := fmt.Sprintf("v11-before-upgrade-%d", time.Now().UnixNano())
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := legacyDB.ExecContext(ctx, `
INSERT INTO z_courier_downlink_messages (
  message_id, client_id, device_id, msg_id, body, ack_required, trace_id,
  session_id, status, attempts, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8, 0, $9, $9)
`,
		legacyMessageID,
		"legacy-client",
		"legacy-device",
		2001,
		[]byte("legacy-body"),
		"legacy-trace",
		"legacy-session",
		string(MessageStatusPending),
		createdAt,
	); err != nil {
		t.Fatalf("insert V11 message before upgrade: %v", err)
	}

	store, err := NewPostgresStore(ctx, PostgresStoreConfig{
		DSN:          dsn,
		AutoMigrate:  true,
		MaxOpenConns: 5,
	})
	if err != nil {
		t.Fatalf("upgrade V11 PostgreSQL schema: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("repeat V12 PostgreSQL migration: %v", err)
	}

	for _, column := range []string{
		"identity_fingerprint",
		"policy_name",
		"policy_max_attempts",
		"policy_max_age_ns",
		"policy_ack_timeout_ns",
		"policy_retry_delay_ns",
		"policy_backoff_multiplier",
		"policy_max_retry_delay_ns",
		"policy_retry_jitter_ns",
		"terminal_reason",
		"terminal_at",
		"terminal_publish_status",
		"terminal_publish_attempts",
		"terminal_next_publish_at",
		"terminal_publish_error",
		"terminal_published_at",
	} {
		assertPostgresColumnExists(t, ctx, store.db, "z_courier_downlink_messages", column)
	}
	assertPostgresRelationExists(t, ctx, store.db, "z_courier_downlink_terminal_events")
	assertPostgresRelationExists(t, ctx, store.db, "z_courier_downlink_terminal_events_message_status_idx")
	assertPostgresRelationExists(t, ctx, store.db, "z_courier_downlink_terminal_events_due_idx")

	legacy, ok, err := store.Get(ctx, legacyMessageID)
	if err != nil || !ok {
		t.Fatalf("read V11 message after upgrade = ok:%v err:%v", ok, err)
	}
	if legacy.ClientID != "legacy-client" || legacy.DeviceID != "legacy-device" ||
		legacy.MsgID != 2001 || string(legacy.Body) != "legacy-body" ||
		legacy.TraceID != "legacy-trace" || legacy.SessionID != "legacy-session" ||
		legacy.Status != MessageStatusPending || legacy.Policy.Name != "" ||
		legacy.TerminalPublishStatus != TerminalPublicationDisabled ||
		len(legacy.IdentityFingerprint) != 0 {
		t.Fatalf("V11 message changed during migration: %+v", legacy)
	}

	replayed, err := store.Save(ctx, Message{
		MessageID:   legacyMessageID,
		ClientID:    "legacy-client",
		DeviceID:    "legacy-device",
		MsgID:       2001,
		Body:        []byte("legacy-body"),
		AckRequired: true,
		TraceID:     "new-trace-is-not-identity",
	})
	if err != nil || replayed.Outcome != SaveOutcomeExisting {
		t.Fatalf("replay migrated V11 message = %+v, err:%v", replayed, err)
	}
	if len(replayed.Message.IdentityFingerprint) != sha256.Size {
		t.Fatalf("lazy identity fingerprint size = %d, want %d", len(replayed.Message.IdentityFingerprint), sha256.Size)
	}

	// A V11 binary writes only the original columns. Keeping defaults on every
	// additive V12 column lets that writer continue after a binary rollback.
	rollbackMessageID := fmt.Sprintf("v11-after-rollback-%d", time.Now().UnixNano())
	if _, err := legacyDB.ExecContext(ctx, `
INSERT INTO z_courier_downlink_messages (
  message_id, client_id, device_id, msg_id, body, ack_required, trace_id,
  session_id, status, attempts, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, false, '', '', $6, 0, $7, $7)
`, rollbackMessageID, "rollback-client", "rollback-device", 2002, []byte("rollback-body"), string(MessageStatusPending), createdAt); err != nil {
		t.Fatalf("simulate V11 write after rollback: %v", err)
	}
	rollbackMessage, ok, err := store.Get(ctx, rollbackMessageID)
	if err != nil || !ok || rollbackMessage.ClientID != "rollback-client" ||
		rollbackMessage.TerminalPublishStatus != TerminalPublicationDisabled {
		t.Fatalf("read simulated V11 rollback write = %+v, ok:%v err:%v", rollbackMessage, ok, err)
	}
}

func TestPostgresStoreIdempotentSaveIntegration(t *testing.T) {
	dsn := os.Getenv("ZCOURIER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ZCOURIER_TEST_POSTGRES_DSN to run PostgreSQL downlink store integration test")
	}
	dsn = isolatedPostgresTestDSN(t, dsn)

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
	claimed, err := store.ClaimDueRetry(ctx, ackDeadline, time.Hour, 100, "gateway-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueRetry(at deadline) error = %v", err)
	}
	if !containsMessageID(claimed, messageID) {
		t.Fatalf("message %s was not claimed at stored ACK deadline", messageID)
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
	if err := store.MarkFailed(ctx, messageID, TerminalTransition{
		Reason:      failureReasonMaxAge,
		GatewayNode: "gateway-a",
		At:          failedAt,
		Publish:     true,
	}); err != nil {
		t.Fatalf("MarkFailed(not attempted) error = %v", err)
	}
	failed, ok, err := store.Get(ctx, messageID)
	if err != nil || !ok {
		t.Fatalf("Get(failed) = ok:%v err:%v", ok, err)
	}
	if failed.Status != MessageStatusFailed || failed.Attempts != 1 || failed.LastError != failureReasonMaxAge ||
		failed.TerminalReason != failureReasonMaxAge || failed.TerminalPublishStatus != TerminalPublicationPending {
		t.Fatalf("failed message = status:%q attempts:%d last_error:%q", failed.Status, failed.Attempts, failed.LastError)
	}

	records, err := store.ClaimDueTerminal(ctx, failedAt, 10, "gateway-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueTerminal() error = %v", err)
	}
	if len(records) != 1 || records[0].Event.MessageID != messageID ||
		records[0].Event.TerminalReason != failureReasonMaxAge || records[0].Event.GatewayNode != "gateway-a" {
		t.Fatalf("terminal records = %+v", records)
	}
	nextPublishAt := failedAt.Add(5 * time.Second)
	if err := store.MarkTerminalPublishFailed(ctx, messageID, MessageStatusFailed, "nsq unavailable", nextPublishAt); err != nil {
		t.Fatalf("MarkTerminalPublishFailed() error = %v", err)
	}
	beforePublish, err := store.ClaimDueTerminal(ctx, nextPublishAt.Add(-time.Millisecond), 10, "gateway-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueTerminal(before retry) error = %v", err)
	}
	if len(beforePublish) != 0 {
		t.Fatalf("terminal records before retry = %+v, want empty", beforePublish)
	}
	records, err = store.ClaimDueTerminal(ctx, nextPublishAt, 10, "gateway-b", time.Minute)
	if err != nil || len(records) != 1 {
		t.Fatalf("ClaimDueTerminal(retry) = %+v, err:%v", records, err)
	}
	if err := store.MarkTerminalPublished(ctx, messageID, MessageStatusFailed, nextPublishAt.Add(time.Second)); err != nil {
		t.Fatalf("MarkTerminalPublished() error = %v", err)
	}
	if err := store.MarkTerminalPublishFailed(
		ctx,
		messageID,
		MessageStatusFailed,
		"stale worker failure",
		nextPublishAt.Add(time.Minute),
	); err != nil {
		t.Fatalf("MarkTerminalPublishFailed(after published) error = %v", err)
	}

	if err := store.MarkFailed(ctx, messageID, TerminalTransition{
		Reason:    failureReasonMaxAttempts,
		At:        failedAt.Add(time.Second),
		Attempted: true,
	}); err != nil {
		t.Fatalf("MarkFailed(attempted) error = %v", err)
	}
	failed, ok, err = store.Get(ctx, messageID)
	if err != nil || !ok {
		t.Fatalf("Get(failed after attempt) = ok:%v err:%v", ok, err)
	}
	if failed.Attempts != 2 || failed.LastError != failureReasonMaxAttempts ||
		failed.TerminalReason != failureReasonMaxAge || failed.TerminalPublishStatus != TerminalPublicationPublished ||
		failed.TerminalPublishAttempts != 2 {
		t.Fatalf("failed after attempt = %+v", failed)
	}
	var eventCount int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM z_courier_downlink_terminal_events WHERE message_id = $1
`, messageID).Scan(&eventCount); err != nil {
		t.Fatalf("count terminal events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("terminal event count = %d, want 1", eventCount)
	}
}

func TestPostgresStoreQueueCapacityIntegration(t *testing.T) {
	dsn := os.Getenv("ZCOURIER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ZCOURIER_TEST_POSTGRES_DSN to run PostgreSQL downlink store integration test")
	}
	dsn = isolatedPostgresTestDSN(t, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := NewPostgresStore(ctx, PostgresStoreConfig{
		DSN:          dsn,
		AutoMigrate:  true,
		MaxOpenConns: 20,
	})
	if err != nil {
		t.Fatalf("NewPostgresStore() error = %v", err)
	}
	defer store.Close()

	runID := time.Now().UnixNano()
	clientID := fmt.Sprintf("capacity-client-%d", runID)
	globalClientID := fmt.Sprintf("capacity-global-client-%d", runID)
	deviceID := "device-1"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_, _ = store.db.ExecContext(
			cleanupCtx,
			"DELETE FROM z_courier_downlink_messages WHERE client_id IN ($1, $2)",
			clientID,
			globalClientID,
		)
	})

	capacity := QueueCapacity{MaxPendingPerDevice: 2}
	const workers = 20
	created := runPostgresCapacityAdmissions(
		t,
		ctx,
		store,
		capacity,
		clientID,
		workers,
		capacity.MaxPendingPerDevice,
		QueueCapacityScopeDevice,
		func(int) string { return deviceID },
	)

	replayed, err := store.SaveWithCapacity(ctx, created[0], capacity)
	if err != nil || replayed.Outcome != SaveOutcomeExisting {
		t.Fatalf("SaveWithCapacity(replay) = %+v, error = %v", replayed, err)
	}
	conflicting := created[0]
	conflicting.Body = []byte("different")
	conflict, err := store.SaveWithCapacity(ctx, conflicting, capacity)
	if err != nil || conflict.Outcome != SaveOutcomeConflict {
		t.Fatalf("SaveWithCapacity(conflict) = %+v, error = %v", conflict, err)
	}

	var existingPending int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM z_courier_downlink_messages
WHERE status = $1
`, string(MessageStatusPending)).Scan(&existingPending); err != nil {
		t.Fatalf("count existing pending messages: %v", err)
	}
	globalCapacity := QueueCapacity{MaxPendingGlobal: existingPending + 2}
	runPostgresCapacityAdmissions(
		t,
		ctx,
		store,
		globalCapacity,
		globalClientID,
		workers,
		2,
		QueueCapacityScopeGlobal,
		func(index int) string { return fmt.Sprintf("device-%d", index) },
	)
}

func TestPostgresStoreRetryFairnessIntegration(t *testing.T) {
	dsn := os.Getenv("ZCOURIER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ZCOURIER_TEST_POSTGRES_DSN to run PostgreSQL downlink store integration test")
	}
	dsn = isolatedPostgresTestDSN(t, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := NewPostgresStore(ctx, PostgresStoreConfig{
		DSN:          dsn,
		AutoMigrate:  true,
		MaxOpenConns: 20,
	})
	if err != nil {
		t.Fatalf("NewPostgresStore() error = %v", err)
	}
	defer store.Close()
	peer, err := NewPostgresStore(ctx, PostgresStoreConfig{
		DSN:          dsn,
		AutoMigrate:  false,
		MaxOpenConns: 10,
	})
	if err != nil {
		t.Fatalf("NewPostgresStore(peer) error = %v", err)
	}
	defer peer.Close()

	runID := time.Now().UnixNano()
	clientID := fmt.Sprintf("fairness-client-%d", runID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM z_courier_downlink_messages WHERE client_id = $1", clientID)
	})

	for _, device := range []struct {
		id    string
		count int
		age   time.Duration
	}{
		{id: "hot", count: 8, age: 3 * time.Minute},
		{id: "cold", count: 2, age: 2 * time.Minute},
		{id: "warm", count: 2, age: time.Minute},
	} {
		for index := 0; index < device.count; index++ {
			messageID := fmt.Sprintf("fairness-%d-%s-%d", runID, device.id, index)
			message := Message{
				MessageID:   messageID,
				ClientID:    clientID,
				DeviceID:    device.id,
				MsgID:       2001,
				Body:        []byte("fairness"),
				Status:      MessageStatusPending,
				CreatedAt:   now.Add(-device.age + time.Duration(index)*time.Millisecond),
				UpdatedAt:   now.Add(-device.age + time.Duration(index)*time.Millisecond),
				NextRetryAt: time.Time{},
			}
			if _, err := store.Save(ctx, message); err != nil {
				t.Fatalf("Save(%s) error = %v", messageID, err)
			}
		}
	}

	selection, err := store.ListDueRetryFair(ctx, now, time.Second, 6, 12)
	if err != nil {
		t.Fatalf("ListDueRetryFair() error = %v", err)
	}
	assertFairDeviceCounts(t, selection, map[string]int{"hot": 2, "cold": 2, "warm": 2})

	claimed, err := store.ClaimDueRetryFair(ctx, now, time.Second, 6, 12, "gateway-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueRetryFair() error = %v", err)
	}
	assertFairDeviceCounts(t, claimed, map[string]int{"hot": 2, "cold": 2, "warm": 2})
	for _, message := range claimed.Messages {
		if message.ClaimOwner != "gateway-a" || !message.ClaimUntil.Equal(now.Add(time.Minute)) {
			t.Fatalf("claimed message = %+v", message)
		}
	}

	if _, err := store.db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET claim_owner = '', claim_until = NULL
WHERE client_id = $1
`, clientID); err != nil {
		t.Fatalf("release fairness claims: %v", err)
	}
	for index := 0; index < 28; index++ {
		messageID := fmt.Sprintf("fairness-%d-extra-%d", runID, index)
		message := Message{
			MessageID: messageID,
			ClientID:  clientID,
			DeviceID:  fmt.Sprintf("extra-%d", index%7),
			MsgID:     2001,
			Body:      []byte("fairness"),
			Status:    MessageStatusPending,
			CreatedAt: now.Add(time.Duration(index) * time.Millisecond),
			UpdatedAt: now.Add(time.Duration(index) * time.Millisecond),
		}
		if _, err := store.Save(ctx, message); err != nil {
			t.Fatalf("Save(%s) error = %v", messageID, err)
		}
	}

	selections := make(chan RetrySelection, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for _, input := range []struct {
		store *PostgresStore
		owner string
	}{{store: store, owner: "gateway-a"}, {store: peer, owner: "gateway-b"}} {
		wait.Add(1)
		go func(input struct {
			store *PostgresStore
			owner string
		}) {
			defer wait.Done()
			selection, err := input.store.ClaimDueRetryFair(ctx, now, time.Second, 5, 20, input.owner, time.Minute)
			if err != nil {
				errors <- err
				return
			}
			selections <- selection
		}(input)
	}
	wait.Wait()
	close(selections)
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent ClaimDueRetryFair() error = %v", err)
	}
	seen := make(map[string]struct{})
	total := 0
	for selection := range selections {
		if len(selection.Messages) != 5 {
			t.Fatalf("concurrent selection size = %d, want 5", len(selection.Messages))
		}
		for _, message := range selection.Messages {
			if _, exists := seen[message.MessageID]; exists {
				t.Fatalf("message %q claimed by both gateways", message.MessageID)
			}
			seen[message.MessageID] = struct{}{}
			total++
		}
	}
	if total != 10 {
		t.Fatalf("concurrent claimed messages = %d, want 10", total)
	}
}

func assertFairDeviceCounts(t *testing.T, selection RetrySelection, expected map[string]int) {
	t.Helper()
	counts := make(map[string]int)
	for _, message := range selection.Messages {
		counts[message.DeviceID]++
	}
	if len(selection.Messages) == 0 || selection.Mode != RetrySelectionModeFair || len(counts) != len(expected) {
		t.Fatalf("selection = %+v, counts = %v", selection, counts)
	}
	for deviceID, count := range expected {
		if counts[deviceID] != count {
			t.Fatalf("device %s selected %d messages, want %d; all counts = %v", deviceID, counts[deviceID], count, counts)
		}
	}
}

func isolatedPostgresTestDSN(t *testing.T, dsn string) string {
	t.Helper()
	schema := fmt.Sprintf("z_courier_test_%d", time.Now().UnixNano())

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL test admin connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	})

	isolatedDSN := postgresDSNWithSearchPath(t, dsn, schema)
	check, err := sql.Open("pgx", isolatedDSN)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL test connection: %v", err)
	}
	defer check.Close()

	var currentSchema string
	if err := check.QueryRowContext(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		t.Fatalf("verify isolated PostgreSQL test schema: %v", err)
	}
	if currentSchema != schema {
		t.Fatalf("isolated PostgreSQL test schema = %q, want %q", currentSchema, schema)
	}
	return isolatedDSN
}

func postgresDSNWithSearchPath(t *testing.T, dsn string, schema string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}

func assertPostgresColumnExists(t *testing.T, ctx context.Context, db *sql.DB, table string, column string) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM information_schema.columns
  WHERE table_schema = current_schema()
    AND table_name = $1
    AND column_name = $2
)
`, table, column).Scan(&exists); err != nil {
		t.Fatalf("check PostgreSQL column %s.%s: %v", table, column, err)
	}
	if !exists {
		t.Fatalf("PostgreSQL column %s.%s does not exist", table, column)
	}
}

func assertPostgresRelationExists(t *testing.T, ctx context.Context, db *sql.DB, relation string) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, `
SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL
`, relation).Scan(&exists); err != nil {
		t.Fatalf("check PostgreSQL relation %s: %v", relation, err)
	}
	if !exists {
		t.Fatalf("PostgreSQL relation %s does not exist", relation)
	}
}

type postgresCapacityOutcome struct {
	message Message
	result  SaveResult
	err     error
}

func runPostgresCapacityAdmissions(
	t *testing.T,
	ctx context.Context,
	store *PostgresStore,
	capacity QueueCapacity,
	clientID string,
	workers int,
	expectedCreated int,
	expectedScope string,
	deviceID func(int) string,
) []Message {
	t.Helper()
	outcomes := make(chan postgresCapacityOutcome, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			messageID := fmt.Sprintf("%s-message-%d", clientID, index)
			message := Message{
				MessageID:   messageID,
				ClientID:    clientID,
				DeviceID:    deviceID(index),
				MsgID:       2001,
				AckRequired: true,
				Body:        []byte(messageID),
			}
			result, err := store.SaveWithCapacity(ctx, message, capacity)
			outcomes <- postgresCapacityOutcome{message: message, result: result, err: err}
		}(index)
	}
	wait.Wait()
	close(outcomes)

	created := make([]Message, 0, expectedCreated)
	rejected := 0
	for outcome := range outcomes {
		switch {
		case outcome.err == nil && outcome.result.Outcome == SaveOutcomeCreated:
			created = append(created, outcome.message)
		case errors.Is(outcome.err, ErrQueueCapacityExceeded):
			var capacityErr *QueueCapacityError
			if !errors.As(outcome.err, &capacityErr) || capacityErr.Scope != expectedScope {
				t.Fatalf("SaveWithCapacity() capacity error = %v, want scope %q", outcome.err, expectedScope)
			}
			rejected++
		default:
			t.Fatalf("SaveWithCapacity() result = %+v, error = %v", outcome.result, outcome.err)
		}
	}
	if len(created) != expectedCreated || rejected != workers-expectedCreated {
		t.Fatalf("created/rejected = %d/%d, want %d/%d", len(created), rejected, expectedCreated, workers-expectedCreated)
	}
	return created
}

func containsMessageID(messages []Message, messageID string) bool {
	for _, message := range messages {
		if message.MessageID == messageID {
			return true
		}
	}
	return false
}
