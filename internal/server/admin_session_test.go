package server

import (
	"strings"
	"testing"
	"time"
)

func TestAdminSessionManagerCreateLookupDeleteAndExpire(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	manager := newAdminSessionManager(AdminConsoleSessionConfig{
		Enabled:        true,
		TTL:            time.Second,
		CookieName:     "zcourier_admin_session",
		CookieSameSite: "lax",
	})
	manager.now = func() time.Time { return now }

	token, created, err := manager.Create("operator")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if token == "" || !strings.HasPrefix(created.SessionID, adminSessionIDPrefix) || created.Principal != "operator" || created.Role != adminSessionRoleAdmin {
		t.Fatalf("created session = %+v token=%q, want token/admin session", created, token)
	}

	found, ok := manager.Lookup(token)
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if found.SessionID != created.SessionID || found.LastSeenAt != now {
		t.Fatalf("found session = %+v, want session %s with last seen %v", found, created.SessionID, now)
	}

	manager.now = func() time.Time { return now.Add(2 * time.Second) }
	if _, ok := manager.Lookup(token); ok {
		t.Fatal("Lookup(expired) ok = true, want false")
	}

	token, _, err = manager.Create("operator")
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	if !manager.Delete(token) {
		t.Fatal("Delete() = false, want true")
	}
	if _, ok := manager.Lookup(token); ok {
		t.Fatal("Lookup(deleted) ok = true, want false")
	}
}
