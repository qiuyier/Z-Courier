package server

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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

	found, ok, err := manager.Lookup(token)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if found.SessionID != created.SessionID || found.LastSeenAt != now {
		t.Fatalf("found session = %+v, want session %s with last seen %v", found, created.SessionID, now)
	}

	manager.now = func() time.Time { return now.Add(2 * time.Second) }
	if _, ok, err := manager.Lookup(token); err != nil || ok {
		if err != nil {
			t.Fatalf("Lookup(expired) error = %v", err)
		}
		t.Fatal("Lookup(expired) ok = true, want false")
	}

	token, _, err = manager.Create("operator")
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	deleted, err := manager.Delete(token)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !deleted {
		t.Fatal("Delete() = false, want true")
	}
	if _, ok, err := manager.Lookup(token); err != nil || ok {
		if err != nil {
			t.Fatalf("Lookup(deleted) error = %v", err)
		}
		t.Fatal("Lookup(deleted) ok = true, want false")
	}
}

func TestRedisAdminSessionManagerSharesSessions(t *testing.T) {
	redisServer := miniredis.RunT(t)
	config := AdminConsoleSessionConfig{
		Enabled:        true,
		TTL:            time.Minute,
		CookieName:     "zcourier_admin_session",
		CookieSameSite: "lax",
		Role:           adminSessionRoleOperator,
		Store: AdminSessionStoreConfig{
			Type: "redis",
			Redis: AdminSessionRedisConfig{
				Addr:             redisServer.Addr(),
				KeyPrefix:        "zcourier:test:admin-session",
				DialTimeout:      time.Second,
				ReadTimeout:      time.Second,
				WriteTimeout:     time.Second,
				OperationTimeout: time.Second,
			},
		},
	}

	managerA, closerA, err := newConfiguredAdminSessionManager(config)
	if err != nil {
		t.Fatalf("newConfiguredAdminSessionManager(a) error = %v", err)
	}
	t.Cleanup(func() { _ = closerA.Close() })
	managerB, closerB, err := newConfiguredAdminSessionManager(config)
	if err != nil {
		t.Fatalf("newConfiguredAdminSessionManager(b) error = %v", err)
	}
	t.Cleanup(func() { _ = closerB.Close() })

	token, created, err := managerA.Create("operator")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	found, ok, err := managerB.Lookup(token)
	if err != nil {
		t.Fatalf("Lookup(shared) error = %v", err)
	}
	if !ok || found.SessionID != created.SessionID || found.Principal != "operator" {
		t.Fatalf("shared session = %+v ok=%v, want session %s", found, ok, created.SessionID)
	}

	deleted, err := managerB.Delete(token)
	if err != nil {
		t.Fatalf("Delete(shared) error = %v", err)
	}
	if !deleted {
		t.Fatal("Delete(shared) = false, want true")
	}
	if _, ok, err := managerA.Lookup(token); err != nil || ok {
		if err != nil {
			t.Fatalf("Lookup(after shared delete) error = %v", err)
		}
		t.Fatal("Lookup(after shared delete) ok = true, want false")
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
		{name: "readonly retry scan", role: adminSessionRoleReadonly, permission: adminPermissionRetryScan, want: false},
		{name: "readonly test push", role: adminSessionRoleReadonly, permission: adminPermissionDownlinkTestPush, want: false},
		{name: "readonly route reload", role: adminSessionRoleReadonly, permission: adminPermissionRouteReload, want: false},
		{name: "operator read", role: adminSessionRoleOperator, permission: adminPermissionRead, want: true},
		{name: "operator repair", role: adminSessionRoleOperator, permission: adminPermissionMessageRepair, want: true},
		{name: "operator retry scan", role: adminSessionRoleOperator, permission: adminPermissionRetryScan, want: true},
		{name: "operator test push", role: adminSessionRoleOperator, permission: adminPermissionDownlinkTestPush, want: true},
		{name: "operator route reload", role: adminSessionRoleOperator, permission: adminPermissionRouteReload, want: true},
		{name: "admin repair", role: adminSessionRoleAdmin, permission: adminPermissionMessageRepair, want: true},
		{name: "admin route reload", role: adminSessionRoleAdmin, permission: adminPermissionRouteReload, want: true},
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
