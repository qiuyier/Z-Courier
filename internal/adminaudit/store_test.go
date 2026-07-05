package adminaudit

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestStoreRecordListIsBoundedNewestFirst(t *testing.T) {
	store := NewStore(StoreConfig{Capacity: 2})
	now := time.Unix(100, 0).UTC()
	store.now = func() time.Time {
		now = now.Add(time.Second)
		return now
	}

	store.RecordAdminAudit(Entry{Action: "login", Result: "success", Principal: "admin-1"})
	store.RecordAdminAudit(Entry{Action: "retry_scan", Result: "success", Principal: "admin-2"})
	store.RecordAdminAudit(Entry{Action: "discard", Result: "failed", Principal: "admin-3"})

	result := store.List(Query{Limit: 10})
	if result.Total != 2 || len(result.Entries) != 2 {
		t.Fatalf("result = %+v, want two retained entries", result)
	}
	if result.Entries[0].Action != "discard" || result.Entries[1].Action != "retry_scan" {
		t.Fatalf("entries = %+v, want newest retained first", result.Entries)
	}
	if result.Entries[0].ID <= result.Entries[1].ID {
		t.Fatalf("ids = %d/%d, want descending order", result.Entries[0].ID, result.Entries[1].ID)
	}
}

func TestStoreListFiltersAndClones(t *testing.T) {
	store := NewStore(StoreConfig{Capacity: 10})
	store.RecordAdminAudit(Entry{
		Action:          "downlink_message_action",
		Result:          "success",
		Principal:       "operator-a",
		TargetClientID:  "client-1",
		TargetSessionID: "session-1",
		MessageID:       "message-1",
		Details:         map[string]string{"action": "discard"},
	})
	store.RecordAdminAudit(Entry{
		Action:          "admin_retry_scan",
		Result:          "success",
		Principal:       "operator-b",
		TargetClientID:  "client-2",
		TargetSessionID: "session-2",
		MessageID:       "message-2",
	})

	result := store.List(Query{
		Action:    "downlink_message_action",
		Result:    "success",
		Principal: "operator",
		ClientID:  "client-1",
		SessionID: "session-1",
		MessageID: "message-1",
		Limit:     10,
	})
	if result.Total != 1 || len(result.Entries) != 1 {
		t.Fatalf("result = %+v, want one filtered entry", result)
	}

	result.Entries[0].Details["action"] = "changed"
	again := store.List(Query{MessageID: "message-1"})
	if again.Entries[0].Details["action"] != "discard" {
		t.Fatalf("details mutated through list result: %+v", again.Entries[0].Details)
	}
}

func TestQueryFromValuesClampsLimit(t *testing.T) {
	values := url.Values{
		"limit":      []string{"50000"},
		"action":     []string{" retry_scan "},
		"client_id":  []string{" client-1 "},
		"session_id": []string{" session-1 "},
	}
	query := QueryFromValues(values)
	if query.Limit != MaxLimit {
		t.Fatalf("limit = %d, want %d", query.Limit, MaxLimit)
	}
	if query.Action != "retry_scan" || query.ClientID != "client-1" || query.SessionID != "session-1" {
		t.Fatalf("query = %+v, want trimmed values", query)
	}
}

func TestPostgresAuditWhereBuildsFilters(t *testing.T) {
	where, args := postgresAuditWhere(Query{
		Action:    "downlink_message_action",
		Result:    "success",
		Principal: "operator",
		ClientID:  "client-1",
		SessionID: "session-1",
		MessageID: "message-1",
	})

	wantWhere := " WHERE action = $1 AND result = $2 AND principal LIKE '%' || $3 || '%' AND target_client_id = $4 AND (target_session_id = $5 OR admin_session_id = $5) AND message_id = $6"
	if where != wantWhere {
		t.Fatalf("where = %q, want %q", where, wantWhere)
	}
	wantArgs := []any{
		"downlink_message_action",
		"success",
		"operator",
		"client-1",
		"session-1",
		"message-1",
	}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	for i := range args {
		if args[i] != wantArgs[i] {
			t.Fatalf("args[%d] = %#v, want %#v", i, args[i], wantArgs[i])
		}
	}
}

func TestPostgresStoreIntegration(t *testing.T) {
	dsn := os.Getenv("ZCOURIER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ZCOURIER_TEST_POSTGRES_DSN to run PostgreSQL audit store integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := NewPostgresStore(ctx, PostgresStoreConfig{
		DSN:              dsn,
		AutoMigrate:      true,
		OperationTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPostgresStore() error = %v", err)
	}

	messageID := fmt.Sprintf("adminaudit-integration-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_, _ = store.db.ExecContext(cleanupCtx, "DELETE FROM z_courier_admin_audit_events WHERE message_id = $1", messageID)
		_ = store.Close()
	})

	recorded := store.RecordAdminAudit(Entry{
		Action:          "integration_test",
		Result:          "success",
		Principal:       "operator-a",
		Role:            "admin",
		TargetClientID:  "client-1",
		TargetSessionID: "session-1",
		MessageID:       messageID,
		Details:         map[string]string{"mode": "postgres"},
	})
	if recorded.ID == 0 {
		t.Fatalf("recorded.ID = 0, want persisted id")
	}

	result := store.List(Query{MessageID: messageID, Limit: 10})
	if result.Total != 1 || len(result.Entries) != 1 {
		t.Fatalf("result = %+v, want one persisted entry", result)
	}
	entry := result.Entries[0]
	if entry.Action != "integration_test" || entry.MessageID != messageID {
		t.Fatalf("entry = %+v, want persisted integration event", entry)
	}
	if entry.Details["mode"] != "postgres" {
		t.Fatalf("details = %+v, want postgres mode", entry.Details)
	}
}
