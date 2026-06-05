package downlink

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreListDuePending(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	if _, err := store.Save(context.Background(), Message{
		MessageID:   "due",
		ClientID:    "c1",
		DeviceID:    "d1",
		MsgID:       2001,
		Status:      MessageStatusPending,
		NextRetryAt: now,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("Save due error = %v", err)
	}
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "future",
		ClientID:    "c1",
		DeviceID:    "d1",
		MsgID:       2001,
		Status:      MessageStatusPending,
		NextRetryAt: now.Add(time.Minute),
		CreatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("Save future error = %v", err)
	}
	if _, err := store.Save(context.Background(), Message{
		MessageID: "sent",
		ClientID:  "c1",
		DeviceID:  "d1",
		MsgID:     2001,
		Status:    MessageStatusSent,
		CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("Save sent error = %v", err)
	}

	messages, err := store.ListDuePending(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("ListDuePending() error = %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != "due" {
		t.Fatalf("ListDuePending() = %+v, want only due", messages)
	}
}

func TestMemoryStoreListPendingByClientDeviceIgnoresRetryTime(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	if _, err := store.Save(context.Background(), Message{
		MessageID:   "future",
		ClientID:    "c1",
		DeviceID:    "d1",
		MsgID:       2001,
		Status:      MessageStatusPending,
		NextRetryAt: now.Add(time.Minute),
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("Save future error = %v", err)
	}
	if _, err := store.Save(context.Background(), Message{
		MessageID: "other-device",
		ClientID:  "c1",
		DeviceID:  "d2",
		MsgID:     2001,
		Status:    MessageStatusPending,
		CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("Save other device error = %v", err)
	}

	messages, err := store.ListPendingByClientDevice(context.Background(), "c1", "d1", 10)
	if err != nil {
		t.Fatalf("ListPendingByClientDevice() error = %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != "future" {
		t.Fatalf("ListPendingByClientDevice() = %+v, want only future", messages)
	}
}

func TestMemoryStoreMarkFailed(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "c1",
		DeviceID:  "d1",
		MsgID:     2001,
		Status:    MessageStatusPending,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	if err := store.MarkFailed(context.Background(), "message-1", "offline", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != MessageStatusFailed {
		t.Fatalf("Status = %q, want failed", stored.Status)
	}
	if stored.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", stored.Attempts)
	}
}

func TestMemoryStoreMarkDelivered(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "c1",
		DeviceID:  "d1",
		MsgID:     2001,
		Status:    MessageStatusSent,
		SentAt:    now,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	deliveredAt := now.Add(time.Second)
	if err := store.MarkDelivered(context.Background(), "message-1", "c1", "d1", deliveredAt); err != nil {
		t.Fatalf("MarkDelivered() error = %v", err)
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != MessageStatusDelivered {
		t.Fatalf("Status = %q, want delivered", stored.Status)
	}
	if !stored.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("DeliveredAt = %v, want %v", stored.DeliveredAt, deliveredAt)
	}
}

func TestMemoryStoreMarkDeliveredRejectsDifferentClientDevice(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "c1",
		DeviceID:  "d1",
		MsgID:     2001,
		Status:    MessageStatusSent,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	err := store.MarkDelivered(context.Background(), "message-1", "c1", "other", time.Now())
	if err != ErrMessageNotFound {
		t.Fatalf("MarkDelivered() error = %v, want %v", err, ErrMessageNotFound)
	}
}
