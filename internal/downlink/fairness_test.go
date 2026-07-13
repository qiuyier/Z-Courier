package downlink

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRetryFairnessCandidateLimit(t *testing.T) {
	fairness := RetryFairness{Enabled: true, CandidateMultiplier: 4}
	if got := fairness.CandidateLimit(100, 0); got != 400 {
		t.Fatalf("CandidateLimit(no device capacity) = %d, want 400", got)
	}
	if got := fairness.CandidateLimit(100, 1000); got != 1100 {
		t.Fatalf("CandidateLimit(device capacity) = %d, want 1100", got)
	}
}

func TestFairRetrySelectionDistributesAcrossDevices(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	candidates := make([]Message, 0, 10)
	for index := 0; index < 8; index++ {
		candidates = append(candidates, fairnessTestMessage("hot", index, now.Add(time.Duration(index)*time.Millisecond)))
	}
	for index := 0; index < 2; index++ {
		candidates = append(candidates, fairnessTestMessage("cold", index, now.Add(time.Minute+time.Duration(index)*time.Millisecond)))
	}

	selection := fairRetrySelection(candidates, 4)
	want := []string{"hot-0", "cold-0", "hot-1", "cold-1"}
	if len(selection.Messages) != len(want) {
		t.Fatalf("selected messages = %d, want %d", len(selection.Messages), len(want))
	}
	for index, messageID := range want {
		if selection.Messages[index].MessageID != messageID {
			t.Fatalf("selected[%d] = %q, want %q", index, selection.Messages[index].MessageID, messageID)
		}
	}
	if selection.Mode != RetrySelectionModeFair || selection.DeviceCount != 2 || selection.MaxPerDevice != 2 {
		t.Fatalf("selection = %+v", selection)
	}
}

func TestFairRetrySelectionKeepsSingleDeviceThroughput(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	candidates := make([]Message, 0, 10)
	for index := 0; index < 10; index++ {
		candidates = append(candidates, fairnessTestMessage("only", index, now.Add(time.Duration(index)*time.Millisecond)))
	}

	selection := fairRetrySelection(candidates, 6)
	if len(selection.Messages) != 6 || selection.DeviceCount != 1 || selection.MaxPerDevice != 6 {
		t.Fatalf("selection = %+v", selection)
	}
	for index, message := range selection.Messages {
		want := fmt.Sprintf("only-%d", index)
		if message.MessageID != want {
			t.Fatalf("selected[%d] = %q, want %q", index, message.MessageID, want)
		}
	}
}

func TestMemoryStoreFairRetrySelectionAndClaim(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	store.now = func() time.Time { return now }
	for index := 0; index < 8; index++ {
		message := fairnessTestMessage("hot", index, now.Add(-time.Minute+time.Duration(index)*time.Millisecond))
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save(hot-%d) error = %v", index, err)
		}
	}
	for index := 0; index < 2; index++ {
		message := fairnessTestMessage("cold", index, now.Add(-time.Second+time.Duration(index)*time.Millisecond))
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save(cold-%d) error = %v", index, err)
		}
	}

	legacy, err := store.ListDueRetry(context.Background(), now, time.Second, 4)
	if err != nil {
		t.Fatalf("ListDueRetry() error = %v", err)
	}
	for _, message := range legacy {
		if message.DeviceID != "hot" {
			t.Fatalf("legacy selection unexpectedly includes device %q", message.DeviceID)
		}
	}

	selection, err := store.ClaimDueRetryFair(context.Background(), now, time.Second, 4, 10, "gateway-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDueRetryFair() error = %v", err)
	}
	if selection.DeviceCount != 2 || selection.MaxPerDevice != 2 || len(selection.Messages) != 4 {
		t.Fatalf("selection = %+v", selection)
	}
	for _, message := range selection.Messages {
		if message.ClaimOwner != "gateway-a" || !message.ClaimUntil.Equal(now.Add(time.Minute)) {
			t.Fatalf("claimed message = %+v", message)
		}
	}

	next, err := store.ListDueRetryFair(context.Background(), now, time.Second, 10, 10)
	if err != nil {
		t.Fatalf("ListDueRetryFair(after claim) error = %v", err)
	}
	if len(next.Messages) != 6 || next.DeviceCount != 1 || next.Messages[0].DeviceID != "hot" {
		t.Fatalf("next selection = %+v", next)
	}
}

func BenchmarkRetrySelection(b *testing.B) {
	now := time.UnixMilli(1760000000000)
	candidates := make([]Message, 0, 4000)
	for index := 0; index < 4000; index++ {
		device := fmt.Sprintf("device-%03d", index%100)
		candidates = append(candidates, fairnessTestMessage(device, index, now.Add(time.Duration(index)*time.Millisecond)))
	}

	b.Run("fifo", func(b *testing.B) {
		for range b.N {
			_ = retrySelectionFromMessages(candidates[:500], RetrySelectionModeFIFO)
		}
	})
	b.Run("fair", func(b *testing.B) {
		for range b.N {
			_ = fairRetrySelection(candidates, 500)
		}
	})
}

func fairnessTestMessage(deviceID string, index int, createdAt time.Time) Message {
	return Message{
		MessageID:   fmt.Sprintf("%s-%d", deviceID, index),
		ClientID:    "client-1",
		DeviceID:    deviceID,
		MsgID:       2001,
		Status:      MessageStatusPending,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
		NextRetryAt: time.Time{},
	}
}
