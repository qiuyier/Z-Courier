package server

import (
	"strings"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/adminaudit"
)

func TestNewAdminAuditStoreCreatesMemoryStore(t *testing.T) {
	config := DefaultConfig()
	config.AdminAuditStorage = AdminAuditStorageConfig{
		Type:     "memory",
		Capacity: 1,
	}

	store, closer, err := newAdminAuditStore(config)
	if err != nil {
		t.Fatalf("newAdminAuditStore() error = %v", err)
	}
	if closer != nil {
		t.Fatalf("closer = %T, want nil for memory store", closer)
	}
	if store == nil {
		t.Fatal("store = nil, want memory audit store")
	}

	store.RecordAdminAudit(adminaudit.Entry{Action: "first", Result: "success"})
	store.RecordAdminAudit(adminaudit.Entry{Action: "second", Result: "success"})
	result := store.List(adminaudit.Query{Limit: 10})
	if result.Total != 1 || len(result.Entries) != 1 {
		t.Fatalf("result = %+v, want bounded single entry", result)
	}
	if result.Entries[0].Action != "second" {
		t.Fatalf("entry action = %q, want second", result.Entries[0].Action)
	}
}

func TestNewAdminAuditStoreSkipsWhenInternalHTTPDisabled(t *testing.T) {
	config := DefaultConfig()
	config.DisableInternalHTTP = true

	store, closer, err := newAdminAuditStore(config)
	if err != nil {
		t.Fatalf("newAdminAuditStore() error = %v", err)
	}
	if store != nil || closer != nil {
		t.Fatalf("store/closer = %T/%T, want nil/nil", store, closer)
	}
}

func TestNewAdminAuditStoreRejectsUnsupportedType(t *testing.T) {
	config := DefaultConfig()
	config.AdminAuditStorage.Type = "sqlite"

	_, _, err := newAdminAuditStore(config)
	if err == nil {
		t.Fatal("newAdminAuditStore() error = nil, want unsupported type error")
	}
	if !strings.Contains(err.Error(), "unsupported admin audit storage type") {
		t.Fatalf("newAdminAuditStore() error = %q, want unsupported type", err)
	}
}
