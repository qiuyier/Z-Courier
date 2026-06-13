package session

import (
	"errors"
	"testing"
	"time"
)

func TestManagerBindAndLookup(t *testing.T) {
	manager := NewManager()
	now := time.UnixMilli(1760000000000)

	result, err := manager.Bind(BindInput{
		SessionID: "session-1",
		ConnID:    1,
		ClientID:  "client-1",
		DeviceID:  "device-1",
		TokenID:   "token-1",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	if result.Session.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want %q", result.Session.SessionID, "session-1")
	}
	if result.Replaced != nil {
		t.Fatalf("Replaced = %v, want nil", result.Replaced)
	}
	if !result.Created {
		t.Fatal("Created = false, want true")
	}

	byConn, ok := manager.GetByConnID(1)
	if !ok {
		t.Fatal("GetByConnID() did not find session")
	}
	if byConn.ClientID != "client-1" || byConn.DeviceID != "device-1" {
		t.Fatalf("session identity = %q/%q", byConn.ClientID, byConn.DeviceID)
	}

	byPair, ok := manager.GetByClientDevice("client-1", "device-1")
	if !ok {
		t.Fatal("GetByClientDevice() did not find session")
	}
	if byPair.ConnID != 1 {
		t.Fatalf("ConnID = %d, want 1", byPair.ConnID)
	}
}

func TestManagerReplacesSameClientDevice(t *testing.T) {
	manager := NewManager()

	if _, err := manager.Bind(BindInput{SessionID: "old", ConnID: 1, ClientID: "client-1", DeviceID: "device-1"}); err != nil {
		t.Fatalf("Bind old error = %v", err)
	}

	result, err := manager.Bind(BindInput{SessionID: "new", ConnID: 2, ClientID: "client-1", DeviceID: "device-1"})
	if err != nil {
		t.Fatalf("Bind new error = %v", err)
	}

	if result.Replaced == nil {
		t.Fatal("Replaced = nil, want old session")
	}
	if result.Replaced.ConnID != 1 {
		t.Fatalf("Replaced ConnID = %d, want 1", result.Replaced.ConnID)
	}
	if !result.Created {
		t.Fatal("Created = false, want true")
	}

	if _, ok := manager.GetByConnID(1); ok {
		t.Fatal("old conn should be removed")
	}

	current, ok := manager.GetByClientDevice("client-1", "device-1")
	if !ok {
		t.Fatal("current session not found")
	}
	if current.ConnID != 2 {
		t.Fatalf("current ConnID = %d, want 2", current.ConnID)
	}
}

func TestManagerKeepsSessionForSameConnection(t *testing.T) {
	manager := NewManager()
	initial := time.UnixMilli(1760000000000)
	later := initial.Add(time.Second)

	first, err := manager.Bind(BindInput{
		SessionID: "session-1",
		ConnID:    1,
		ClientID:  "client-1",
		DeviceID:  "device-1",
		TokenID:   "token-1",
		Now:       initial,
	})
	if err != nil {
		t.Fatalf("first Bind() error = %v", err)
	}

	second, err := manager.Bind(BindInput{
		ConnID:      1,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		TokenID:     "token-2",
		GatewayNode: "node-1",
		Now:         later,
	})
	if err != nil {
		t.Fatalf("second Bind() error = %v", err)
	}

	if second.Replaced != nil {
		t.Fatalf("Replaced = %v, want nil", second.Replaced)
	}
	if second.Created {
		t.Fatal("Created = true, want false")
	}
	if second.Session.SessionID != first.Session.SessionID {
		t.Fatalf("SessionID = %q, want %q", second.Session.SessionID, first.Session.SessionID)
	}
	if second.Session.TokenID != "token-2" {
		t.Fatalf("TokenID = %q, want token-2", second.Session.TokenID)
	}
	if !second.Session.ConnectedAt.Equal(initial) {
		t.Fatalf("ConnectedAt = %v, want %v", second.Session.ConnectedAt, initial)
	}
	if !second.Session.LastSeenAt.Equal(later) {
		t.Fatalf("LastSeenAt = %v, want %v", second.Session.LastSeenAt, later)
	}
}

func TestManagerAllowsMultipleDevices(t *testing.T) {
	manager := NewManager()

	if _, err := manager.Bind(BindInput{ConnID: 1, ClientID: "client-1", DeviceID: "phone"}); err != nil {
		t.Fatalf("Bind phone error = %v", err)
	}
	if _, err := manager.Bind(BindInput{ConnID: 2, ClientID: "client-1", DeviceID: "tablet"}); err != nil {
		t.Fatalf("Bind tablet error = %v", err)
	}

	sessions := manager.ListByClientID("client-1")
	if len(sessions) != 2 {
		t.Fatalf("ListByClientID() length = %d, want 2", len(sessions))
	}
	if manager.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", manager.Len())
	}
	if manager.UniqueClientLen() != 1 {
		t.Fatalf("UniqueClientLen() = %d, want 1", manager.UniqueClientLen())
	}
}

func TestManagerCountsUniqueClients(t *testing.T) {
	manager := NewManager()

	if _, err := manager.Bind(BindInput{ConnID: 1, ClientID: "client-1", DeviceID: "phone"}); err != nil {
		t.Fatalf("Bind client-1 phone error = %v", err)
	}
	if _, err := manager.Bind(BindInput{ConnID: 2, ClientID: "client-1", DeviceID: "tablet"}); err != nil {
		t.Fatalf("Bind client-1 tablet error = %v", err)
	}
	if _, err := manager.Bind(BindInput{ConnID: 3, ClientID: "client-2", DeviceID: "phone"}); err != nil {
		t.Fatalf("Bind client-2 phone error = %v", err)
	}

	if manager.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", manager.Len())
	}
	if manager.UniqueClientLen() != 2 {
		t.Fatalf("UniqueClientLen() = %d, want 2", manager.UniqueClientLen())
	}

	if _, ok := manager.UnbindByConnID(1); !ok {
		t.Fatal("UnbindByConnID(1) ok = false, want true")
	}
	if manager.UniqueClientLen() != 2 {
		t.Fatalf("UniqueClientLen() after one device removed = %d, want 2", manager.UniqueClientLen())
	}

	if _, ok := manager.UnbindByConnID(2); !ok {
		t.Fatal("UnbindByConnID(2) ok = false, want true")
	}
	if manager.UniqueClientLen() != 1 {
		t.Fatalf("UniqueClientLen() after client removed = %d, want 1", manager.UniqueClientLen())
	}
}

func TestManagerSnapshotReturnsClones(t *testing.T) {
	manager := NewManager()

	if _, err := manager.Bind(BindInput{ConnID: 1, ClientID: "client-1", DeviceID: "phone"}); err != nil {
		t.Fatalf("Bind phone error = %v", err)
	}
	if _, err := manager.Bind(BindInput{ConnID: 2, ClientID: "client-2", DeviceID: "tablet"}); err != nil {
		t.Fatalf("Bind tablet error = %v", err)
	}

	snapshot := manager.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("Snapshot() length = %d, want 2", len(snapshot))
	}

	changedConnID := snapshot[0].ConnID
	snapshot[0].ClientID = "changed"

	stored, ok := manager.GetByConnID(changedConnID)
	if !ok {
		t.Fatalf("GetByConnID(%d) ok = false, want true", changedConnID)
	}
	if stored.ClientID == "changed" {
		t.Fatal("Snapshot() returned mutable stored session")
	}
}

func TestManagerUnbindRemovesIndexes(t *testing.T) {
	manager := NewManager()

	if _, err := manager.Bind(BindInput{SessionID: "session-1", ConnID: 1, ClientID: "client-1", DeviceID: "device-1"}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	removed, ok := manager.UnbindByConnID(1)
	if !ok {
		t.Fatal("UnbindByConnID() did not remove session")
	}
	if removed.SessionID != "session-1" {
		t.Fatalf("removed SessionID = %q, want %q", removed.SessionID, "session-1")
	}

	if _, ok := manager.GetByConnID(1); ok {
		t.Fatal("GetByConnID() found removed session")
	}
	if _, ok := manager.GetBySessionID("session-1"); ok {
		t.Fatal("GetBySessionID() found removed session")
	}
	if _, ok := manager.GetByClientDevice("client-1", "device-1"); ok {
		t.Fatal("GetByClientDevice() found removed session")
	}
}

func TestManagerTouchByConnID(t *testing.T) {
	manager := NewManager()
	initial := time.UnixMilli(1760000000000)
	later := initial.Add(time.Second)

	if _, err := manager.Bind(BindInput{ConnID: 1, ClientID: "client-1", DeviceID: "device-1", Now: initial}); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	updated, err := manager.TouchByConnID(1, later)
	if err != nil {
		t.Fatalf("TouchByConnID() error = %v", err)
	}
	if !updated.LastSeenAt.Equal(later) {
		t.Fatalf("LastSeenAt = %v, want %v", updated.LastSeenAt, later)
	}
}

func TestManagerBindValidation(t *testing.T) {
	manager := NewManager()

	_, err := manager.Bind(BindInput{ClientID: "client-1", DeviceID: "device-1"})
	if !errors.Is(err, ErrInvalidConnID) {
		t.Fatalf("Bind() error = %v, want %v", err, ErrInvalidConnID)
	}

	_, err = manager.Bind(BindInput{ConnID: 1, DeviceID: "device-1"})
	if !errors.Is(err, ErrEmptyClientID) {
		t.Fatalf("Bind() error = %v, want %v", err, ErrEmptyClientID)
	}

	_, err = manager.Bind(BindInput{ConnID: 1, ClientID: "client-1"})
	if !errors.Is(err, ErrEmptyDeviceID) {
		t.Fatalf("Bind() error = %v, want %v", err, ErrEmptyDeviceID)
	}
}
