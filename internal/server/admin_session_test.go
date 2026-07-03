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
		Role:           adminSessionRoleOperator,
	})
	manager.now = func() time.Time { return now }

	token, created, err := manager.Create("operator")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if token == "" || !strings.HasPrefix(created.SessionID, adminSessionIDPrefix) || created.Principal != "operator" || created.Role != adminSessionRoleOperator {
		t.Fatalf("created session = %+v token=%q, want token/operator session", created, token)
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

func TestAdminRoleAllows(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		permission string
		want       bool
	}{
		{name: "readonly read", role: adminSessionRoleReadonly, permission: adminPermissionRead, want: true},
		{name: "readonly repair", role: adminSessionRoleReadonly, permission: adminPermissionMessageRepair, want: false},
		{name: "operator read", role: adminSessionRoleOperator, permission: adminPermissionRead, want: true},
		{name: "operator repair", role: adminSessionRoleOperator, permission: adminPermissionMessageRepair, want: true},
		{name: "admin repair", role: adminSessionRoleAdmin, permission: adminPermissionMessageRepair, want: true},
		{name: "empty defaults admin", role: "", permission: adminPermissionMessageRepair, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adminRoleAllows(test.role, test.permission); got != test.want {
				t.Fatalf("adminRoleAllows(%q, %q) = %v, want %v", test.role, test.permission, got, test.want)
			}
		})
	}
}
