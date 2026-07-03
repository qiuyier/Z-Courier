package adminaudit

import (
	"net/url"
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
