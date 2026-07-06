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

func TestStoreListPaginatesWithCursor(t *testing.T) {
	store := NewStore(StoreConfig{Capacity: 10})
	for i := 1; i <= 5; i++ {
		store.RecordAdminAudit(Entry{Action: fmt.Sprintf("event-%d", i), Result: "success"})
	}

	first := store.List(Query{Limit: 2})
	if first.Total != 5 || len(first.Entries) != 2 || !first.HasMore || first.NextCursor == 0 {
		t.Fatalf("first page = %+v, want two entries with next cursor", first)
	}
	if first.Entries[0].Action != "event-5" || first.Entries[1].Action != "event-4" {
		t.Fatalf("first entries = %+v, want event-5/event-4", first.Entries)
	}

	second := store.List(Query{Limit: 2, Cursor: first.NextCursor})
	if second.Total != 5 || len(second.Entries) != 2 || !second.HasMore || second.NextCursor == 0 {
		t.Fatalf("second page = %+v, want two entries with next cursor", second)
	}
	if second.Entries[0].Action != "event-3" || second.Entries[1].Action != "event-2" {
		t.Fatalf("second entries = %+v, want event-3/event-2", second.Entries)
	}

	third := store.List(Query{Limit: 2, Cursor: second.NextCursor})
	if third.Total != 5 || len(third.Entries) != 1 || third.HasMore || third.NextCursor != 0 {
		t.Fatalf("third page = %+v, want final single entry", third)
	}
	if third.Entries[0].Action != "event-1" {
		t.Fatalf("third entry = %+v, want event-1", third.Entries[0])
	}
}

func TestQueryFromValuesClampsLimit(t *testing.T) {
	values := url.Values{
		"limit":      []string{"50000"},
		"cursor":     []string{"42"},
		"action":     []string{" retry_scan "},
		"client_id":  []string{" client-1 "},
		"session_id": []string{" session-1 "},
	}
	query := QueryFromValues(values)
	if query.Limit != MaxLimit {
		t.Fatalf("limit = %d, want %d", query.Limit, MaxLimit)
	}
	if query.Cursor != 42 {
		t.Fatalf("cursor = %d, want 42", query.Cursor)
	}
	if query.Action != "retry_scan" || query.ClientID != "client-1" || query.SessionID != "session-1" {
		t.Fatalf("query = %+v, want trimmed values", query)
	}
}

func TestQueryFromValuesIgnoresInvalidCursor(t *testing.T) {
	query := QueryFromValues(url.Values{"cursor": []string{"not-a-number"}})
	if query.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0 for invalid cursor", query.Cursor)
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
		Cursor:    123,
	})

	wantWhere := " WHERE action = $1 AND result = $2 AND principal LIKE '%' || $3 || '%' AND target_client_id = $4 AND (target_session_id = $5 OR admin_session_id = $5) AND message_id = $6 AND id < $7"
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
		int64(123),
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

	firstRecorded := store.RecordAdminAudit(Entry{
		Action:          "integration_test",
		Result:          "success",
		Principal:       "operator-a",
		Role:            "admin",
		TargetClientID:  "client-1",
		TargetSessionID: "session-1",
		MessageID:       messageID,
		Details:         map[string]string{"mode": "postgres"},
	})
	if firstRecorded.ID == 0 {
		t.Fatalf("firstRecorded.ID = 0, want persisted id")
	}
	secondRecorded := store.RecordAdminAudit(Entry{
		Action:    "integration_test_second",
		Result:    "success",
		MessageID: messageID,
	})
	if secondRecorded.ID == 0 {
		t.Fatalf("secondRecorded.ID = 0, want persisted id")
	}

	result := store.List(Query{MessageID: messageID, Limit: 10})
	if result.Total != 2 || len(result.Entries) != 2 {
		t.Fatalf("result = %+v, want two persisted entries", result)
	}
	entry := result.Entries[0]
	if entry.Action != "integration_test_second" || entry.MessageID != messageID {
		t.Fatalf("entry = %+v, want newest persisted integration event", entry)
	}

	firstPage := store.List(Query{MessageID: messageID, Limit: 1})
	if firstPage.Total != 2 || len(firstPage.Entries) != 1 || !firstPage.HasMore || firstPage.NextCursor == 0 {
		t.Fatalf("first page = %+v, want paginated persisted result", firstPage)
	}
	secondPage := store.List(Query{MessageID: messageID, Limit: 1, Cursor: firstPage.NextCursor})
	if secondPage.Total != 2 || len(secondPage.Entries) != 1 || secondPage.HasMore {
		t.Fatalf("second page = %+v, want final persisted result", secondPage)
	}
	if secondPage.Entries[0].Action != "integration_test" || secondPage.Entries[0].Details["mode"] != "postgres" {
		t.Fatalf("second page entry = %+v, want original postgres event", secondPage.Entries[0])
	}
}
