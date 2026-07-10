package downlink

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreSaveReportsCreatedExistingAndConflict(t *testing.T) {
	store := NewMemoryStore()
	message := Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		AckRequired: true,
		TraceID:     "trace-1",
		Body:        []byte("hello"),
	}

	created, err := store.Save(context.Background(), message)
	if err != nil {
		t.Fatalf("Save created error = %v", err)
	}
	if created.Outcome != SaveOutcomeCreated || created.Message.MessageID != message.MessageID {
		t.Fatalf("created result = %+v", created)
	}

	replay := message.Clone()
	replay.TraceID = "trace-2"
	existing, err := store.Save(context.Background(), replay)
	if err != nil {
		t.Fatalf("Save existing error = %v", err)
	}
	if existing.Outcome != SaveOutcomeExisting || existing.Message.TraceID != "trace-1" {
		t.Fatalf("existing result = %+v", existing)
	}

	conflicting := message.Clone()
	conflicting.Body = []byte("different")
	conflict, err := store.Save(context.Background(), conflicting)
	if err != nil {
		t.Fatalf("Save conflict error = %v", err)
	}
	if conflict.Outcome != SaveOutcomeConflict || string(conflict.Message.Body) != "hello" {
		t.Fatalf("conflict result = %+v", conflict)
	}
}

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

func TestMemoryStoreListDueRetrySkipsActiveClaim(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	for _, message := range []Message{
		{
			MessageID:   "claimed",
			ClientID:    "c1",
			DeviceID:    "d1",
			MsgID:       2001,
			Status:      MessageStatusPending,
			NextRetryAt: now,
			ClaimOwner:  "gateway-a",
			ClaimUntil:  now.Add(time.Minute),
			CreatedAt:   now,
		},
		{
			MessageID:   "expired-claim",
			ClientID:    "c1",
			DeviceID:    "d1",
			MsgID:       2001,
			Status:      MessageStatusPending,
			NextRetryAt: now,
			ClaimOwner:  "gateway-a",
			ClaimUntil:  now.Add(-time.Second),
			CreatedAt:   now.Add(time.Second),
		},
	} {
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save %s error = %v", message.MessageID, err)
		}
	}

	messages, err := store.ListDueRetry(context.Background(), now, time.Second, 10)
	if err != nil {
		t.Fatalf("ListDueRetry() error = %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != "expired-claim" {
		t.Fatalf("ListDueRetry() = %+v, want only expired-claim", messages)
	}
}

func TestMemoryStoreListDueRetryIncludesAckTimeout(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	for _, message := range []Message{
		{
			MessageID:   "pending",
			ClientID:    "c1",
			DeviceID:    "d1",
			MsgID:       2001,
			Status:      MessageStatusPending,
			NextRetryAt: now,
			CreatedAt:   now,
		},
		{
			MessageID:   "sent-timeout",
			ClientID:    "c1",
			DeviceID:    "d1",
			MsgID:       2001,
			AckRequired: true,
			Status:      MessageStatusSent,
			SentAt:      now.Add(-2 * time.Second),
			CreatedAt:   now.Add(time.Second),
		},
		{
			MessageID:   "sent-fresh",
			ClientID:    "c1",
			DeviceID:    "d1",
			MsgID:       2001,
			AckRequired: true,
			Status:      MessageStatusSent,
			SentAt:      now.Add(-500 * time.Millisecond),
			CreatedAt:   now.Add(2 * time.Second),
		},
		{
			MessageID: "sent-no-ack",
			ClientID:  "c1",
			DeviceID:  "d1",
			MsgID:     2001,
			Status:    MessageStatusSent,
			SentAt:    now.Add(-2 * time.Second),
			CreatedAt: now.Add(3 * time.Second),
		},
	} {
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save %s error = %v", message.MessageID, err)
		}
	}

	messages, err := store.ListDueRetry(context.Background(), now, time.Second, 10)
	if err != nil {
		t.Fatalf("ListDueRetry() error = %v", err)
	}
	if len(messages) != 2 || messages[0].MessageID != "pending" || messages[1].MessageID != "sent-timeout" {
		t.Fatalf("ListDueRetry() = %+v, want pending and sent-timeout", messages)
	}
}

func TestMemoryStoreListByStatus(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	if _, err := store.Save(context.Background(), Message{
		MessageID: "old-failed",
		ClientID:  "c1",
		DeviceID:  "d1",
		MsgID:     2001,
		Status:    MessageStatusFailed,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Save old failed error = %v", err)
	}
	if _, err := store.Save(context.Background(), Message{
		MessageID: "pending",
		ClientID:  "c1",
		DeviceID:  "d1",
		MsgID:     2001,
		Status:    MessageStatusPending,
		CreatedAt: now.Add(time.Second),
		UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("Save pending error = %v", err)
	}
	if _, err := store.Save(context.Background(), Message{
		MessageID: "new-failed",
		ClientID:  "c1",
		DeviceID:  "d1",
		MsgID:     2001,
		Status:    MessageStatusFailed,
		CreatedAt: now.Add(2 * time.Second),
		UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("Save new failed error = %v", err)
	}

	messages, err := store.ListByStatus(context.Background(), MessageStatusFailed, 10)
	if err != nil {
		t.Fatalf("ListByStatus() error = %v", err)
	}
	if len(messages) != 2 || messages[0].MessageID != "new-failed" || messages[1].MessageID != "old-failed" {
		t.Fatalf("ListByStatus() = %+v, want new-failed then old-failed", messages)
	}
}

func TestMemoryStoreListByStatusPage(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	for _, message := range []Message{
		{MessageID: "failed-a", ClientID: "c1", DeviceID: "d1", MsgID: 2001, Status: MessageStatusFailed, UpdatedAt: now.Add(3 * time.Second)},
		{MessageID: "failed-b", ClientID: "c1", DeviceID: "d1", MsgID: 2001, Status: MessageStatusFailed, UpdatedAt: now.Add(2 * time.Second)},
		{MessageID: "failed-c", ClientID: "c1", DeviceID: "d1", MsgID: 2001, Status: MessageStatusFailed, UpdatedAt: now.Add(time.Second)},
		{MessageID: "pending", ClientID: "c1", DeviceID: "d1", MsgID: 2001, Status: MessageStatusPending, UpdatedAt: now.Add(4 * time.Second)},
	} {
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save(%s) error = %v", message.MessageID, err)
		}
	}

	first, err := store.ListByStatusPage(context.Background(), MessageListQuery{Status: MessageStatusFailed, Limit: 2})
	if err != nil {
		t.Fatalf("ListByStatusPage(first) error = %v", err)
	}
	if first.Total != 3 || !first.HasMore || len(first.Messages) != 2 {
		t.Fatalf("first result = %+v, want total=3 has_more and two messages", first)
	}
	if first.Messages[0].MessageID != "failed-a" || first.Messages[1].MessageID != "failed-b" {
		t.Fatalf("first messages = %+v, want failed-a then failed-b", first.Messages)
	}
	if messageListCursorIsZero(first.NextCursor) {
		t.Fatalf("first.NextCursor = zero, want cursor")
	}

	second, err := store.ListByStatusPage(context.Background(), MessageListQuery{
		Status: MessageStatusFailed,
		Limit:  2,
		Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListByStatusPage(second) error = %v", err)
	}
	if second.Total != 3 || second.HasMore || len(second.Messages) != 1 || second.Messages[0].MessageID != "failed-c" {
		t.Fatalf("second result = %+v, want final failed-c page", second)
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
		ClaimOwner:  "gateway-a",
		ClaimUntil:  now.Add(time.Minute),
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("Save future error = %v", err)
	}
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "expired-claim",
		ClientID:    "c1",
		DeviceID:    "d1",
		MsgID:       2001,
		Status:      MessageStatusPending,
		NextRetryAt: now.Add(time.Minute),
		ClaimOwner:  "gateway-a",
		ClaimUntil:  now.Add(-time.Second),
		CreatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("Save expired claim error = %v", err)
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
	if len(messages) != 1 || messages[0].MessageID != "expired-claim" {
		t.Fatalf("ListPendingByClientDevice() = %+v, want only expired-claim", messages)
	}
}

func TestMemoryStoreRequeue(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "c1",
		DeviceID:    "d1",
		MsgID:       2001,
		Status:      MessageStatusFailed,
		Attempts:    5,
		LastError:   "offline",
		NextRetryAt: now.Add(time.Minute),
		ClaimOwner:  "gateway-a",
		ClaimUntil:  now.Add(time.Minute),
		SessionID:   "session-1",
		SentAt:      now,
		DeliveredAt: now,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	requeuedAt := now.Add(time.Second)
	if err := store.Requeue(context.Background(), "message-1", requeuedAt); err != nil {
		t.Fatalf("Requeue() error = %v", err)
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != MessageStatusPending || stored.Attempts != 0 || stored.LastError != "" {
		t.Fatalf("stored = %+v, want pending attempts reset", stored)
	}
	if !stored.NextRetryAt.IsZero() || stored.ClaimOwner != "" || !stored.ClaimUntil.IsZero() || stored.SessionID != "" || !stored.SentAt.IsZero() || !stored.DeliveredAt.IsZero() {
		t.Fatalf("stored retry metadata = %+v, want cleared", stored)
	}
	if !stored.UpdatedAt.Equal(requeuedAt) {
		t.Fatalf("UpdatedAt = %v, want %v", stored.UpdatedAt, requeuedAt)
	}
}

func TestMemoryStoreDiscard(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "c1",
		DeviceID:    "d1",
		MsgID:       2001,
		Status:      MessageStatusFailed,
		LastError:   "offline",
		NextRetryAt: now.Add(time.Minute),
		ClaimOwner:  "gateway-a",
		ClaimUntil:  now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	discardedAt := now.Add(time.Second)
	if err := store.Discard(context.Background(), "message-1", "manual discard", discardedAt); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != MessageStatusDiscarded || stored.LastError != "manual discard" {
		t.Fatalf("stored = %+v, want discarded with reason", stored)
	}
	if !stored.NextRetryAt.IsZero() || stored.ClaimOwner != "" || !stored.ClaimUntil.IsZero() {
		t.Fatalf("stored retry metadata = %+v, want cleared", stored)
	}
}

func TestMemoryStoreDeleteExpired(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	for _, message := range []Message{
		{
			MessageID: "old-delivered",
			ClientID:  "c1",
			DeviceID:  "d1",
			MsgID:     2001,
			Status:    MessageStatusDelivered,
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-3 * time.Hour),
		},
		{
			MessageID: "fresh-delivered",
			ClientID:  "c1",
			DeviceID:  "d1",
			MsgID:     2001,
			Status:    MessageStatusDelivered,
			CreatedAt: now.Add(-30 * time.Minute),
			UpdatedAt: now.Add(-30 * time.Minute),
		},
		{
			MessageID: "old-failed",
			ClientID:  "c1",
			DeviceID:  "d1",
			MsgID:     2001,
			Status:    MessageStatusFailed,
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-3 * time.Hour),
		},
		{
			MessageID: "old-pending",
			ClientID:  "c1",
			DeviceID:  "d1",
			MsgID:     2001,
			Status:    MessageStatusPending,
			CreatedAt: now.Add(-3 * time.Hour),
			UpdatedAt: now.Add(-3 * time.Hour),
		},
	} {
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save %s error = %v", message.MessageID, err)
		}
	}

	deleted, err := store.DeleteExpired(context.Background(), MessageStatusDelivered, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("DeleteExpired() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteExpired() deleted = %d, want 1", deleted)
	}

	if _, ok, err := store.Get(context.Background(), "old-delivered"); err != nil || ok {
		t.Fatalf("old-delivered exists = %v, err = %v; want deleted", ok, err)
	}
	for _, messageID := range []string{"fresh-delivered", "old-failed", "old-pending"} {
		if _, ok, err := store.Get(context.Background(), messageID); err != nil || !ok {
			t.Fatalf("%s exists = %v, err = %v; want retained", messageID, ok, err)
		}
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

func TestMemoryStoreMarkSentDoesNotOverwriteDelivered(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }

	deliveredAt := now.Add(time.Second)
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "c1",
		DeviceID:    "d1",
		MsgID:       2001,
		Status:      MessageStatusDelivered,
		SessionID:   "session-fast-ack",
		Attempts:    1,
		DeliveredAt: deliveredAt,
		UpdatedAt:   deliveredAt,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	if err := store.MarkSent(context.Background(), "message-1", "session-origin", now.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkSent() error = %v", err)
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
	if stored.SessionID != "session-fast-ack" {
		t.Fatalf("SessionID = %q, want session-fast-ack", stored.SessionID)
	}
	if stored.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", stored.Attempts)
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
