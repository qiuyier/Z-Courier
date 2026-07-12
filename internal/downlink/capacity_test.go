package downlink

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

func TestMemoryStoreQueueCapacityPreservesIdempotency(t *testing.T) {
	store := NewMemoryStore()
	capacity := QueueCapacity{MaxPendingGlobal: 2, MaxPendingPerDevice: 1}
	first := capacityTestMessage("message-1", "device-1")

	created, err := store.SaveWithCapacity(context.Background(), first, capacity)
	if err != nil || created.Outcome != SaveOutcomeCreated {
		t.Fatalf("SaveWithCapacity(created) = %+v, error = %v", created, err)
	}
	replayed, err := store.SaveWithCapacity(context.Background(), first, capacity)
	if err != nil || replayed.Outcome != SaveOutcomeExisting {
		t.Fatalf("SaveWithCapacity(replay) = %+v, error = %v", replayed, err)
	}
	conflicting := first
	conflicting.Body = []byte("different")
	conflict, err := store.SaveWithCapacity(context.Background(), conflicting, capacity)
	if err != nil || conflict.Outcome != SaveOutcomeConflict {
		t.Fatalf("SaveWithCapacity(conflict) = %+v, error = %v", conflict, err)
	}

	_, err = store.SaveWithCapacity(context.Background(), capacityTestMessage("message-2", "device-1"), capacity)
	assertQueueCapacityError(t, err, QueueCapacityScopeDevice, 1, 1)
	if _, err := store.SaveWithCapacity(context.Background(), capacityTestMessage("message-3", "device-2"), capacity); err != nil {
		t.Fatalf("SaveWithCapacity(second device) error = %v", err)
	}
	_, err = store.SaveWithCapacity(context.Background(), capacityTestMessage("message-4", "device-3"), capacity)
	assertQueueCapacityError(t, err, QueueCapacityScopeGlobal, 2, 2)
}

func TestMemoryStoreQueueCapacityIsAtomicUnderConcurrency(t *testing.T) {
	store := NewMemoryStore()
	capacity := QueueCapacity{MaxPendingGlobal: 10}
	const writers = 50

	var wait sync.WaitGroup
	results := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := store.SaveWithCapacity(
				context.Background(),
				capacityTestMessage(fmt.Sprintf("message-%d", index), fmt.Sprintf("device-%d", index)),
				capacity,
			)
			results <- err
		}(index)
	}
	wait.Wait()
	close(results)

	created := 0
	rejected := 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrQueueCapacityExceeded):
			rejected++
		default:
			t.Fatalf("SaveWithCapacity() unexpected error = %v", err)
		}
	}
	if created != capacity.MaxPendingGlobal || rejected != writers-capacity.MaxPendingGlobal {
		t.Fatalf("created/rejected = %d/%d", created, rejected)
	}
	messages, err := store.ListByStatus(context.Background(), MessageStatusPending, writers)
	if err != nil || len(messages) != capacity.MaxPendingGlobal {
		t.Fatalf("pending messages = %d, error = %v", len(messages), err)
	}
}

func TestMemoryStoreRequeueRespectsQueueCapacity(t *testing.T) {
	store := NewMemoryStore()
	capacity := QueueCapacity{MaxPendingPerDevice: 1}
	if _, err := store.Save(context.Background(), capacityTestMessage("pending", "device-1")); err != nil {
		t.Fatalf("Save(pending) error = %v", err)
	}
	failed := capacityTestMessage("failed", "device-1")
	failed.Status = MessageStatusFailed
	if _, err := store.Save(context.Background(), failed); err != nil {
		t.Fatalf("Save(failed) error = %v", err)
	}

	err := store.RequeueWithCapacity(context.Background(), "failed", time.Now(), capacity)
	assertQueueCapacityError(t, err, QueueCapacityScopeDevice, 1, 1)
	if err := store.MarkSent(context.Background(), "pending", "session-1", time.Now(), time.Time{}); err != nil {
		t.Fatalf("MarkSent() error = %v", err)
	}
	if err := store.RequeueWithCapacity(context.Background(), "failed", time.Now(), capacity); err != nil {
		t.Fatalf("RequeueWithCapacity() error = %v", err)
	}
	if err := store.RequeueWithCapacity(context.Background(), "failed", time.Now(), capacity); err != nil {
		t.Fatalf("RequeueWithCapacity(existing pending) error = %v", err)
	}
}

func TestMemoryStoreQueueCapacityOnlyCountsPendingAdmissions(t *testing.T) {
	store := NewMemoryStore()
	capacity := QueueCapacity{MaxPendingGlobal: 1}
	if _, err := store.SaveWithCapacity(context.Background(), capacityTestMessage("pending", "device-1"), capacity); err != nil {
		t.Fatalf("SaveWithCapacity(pending) error = %v", err)
	}

	delivered := capacityTestMessage("delivered", "device-2")
	delivered.Status = MessageStatusDelivered
	result, err := store.SaveWithCapacity(context.Background(), delivered, capacity)
	if err != nil || result.Outcome != SaveOutcomeCreated {
		t.Fatalf("SaveWithCapacity(delivered) = %+v, error = %v", result, err)
	}
}

func TestDownlinkHTTPQueueCapacityExceeded(t *testing.T) {
	service := NewService(fakeSessions{}, fakeConnections{},
		WithStore(NewMemoryStore()),
		WithQueueCapacity(QueueCapacity{MaxPendingPerDevice: 1}),
	)
	handler := NewHandler(HandlerConfig{Service: service})

	first := capacityTestMessage("message-1", "device-1")
	firstBody, _ := sonic.Marshal(pushRequestFromMessage(first))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodPost, "/internal/push", bytes.NewReader(firstBody)))
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}

	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, httptest.NewRequest(http.MethodPost, "/internal/push", bytes.NewReader(firstBody)))
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body = %s", replayResponse.Code, replayResponse.Body.String())
	}

	second := capacityTestMessage("message-2", "device-1")
	secondBody, _ := sonic.Marshal(pushRequestFromMessage(second))
	rejectedResponse := httptest.NewRecorder()
	handler.ServeHTTP(rejectedResponse, httptest.NewRequest(http.MethodPost, "/internal/push", bytes.NewReader(secondBody)))
	if rejectedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("rejected status = %d, body = %s", rejectedResponse.Code, rejectedResponse.Body.String())
	}
	var response PushResponse
	if err := sonic.Unmarshal(rejectedResponse.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.Code != "queue_capacity_exceeded" || response.CapacityScope != QueueCapacityScopeDevice ||
		response.CapacityLimit != 1 || response.CapacityPending != 1 {
		t.Fatalf("capacity response = %+v", response)
	}
}

func capacityTestMessage(messageID, deviceID string) Message {
	return Message{
		MessageID:   messageID,
		ClientID:    "client-1",
		DeviceID:    deviceID,
		MsgID:       2001,
		AckRequired: true,
		Body:        []byte("hello"),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func assertQueueCapacityError(t *testing.T, err error, scope string, pending, limit int) {
	t.Helper()
	if !errors.Is(err, ErrQueueCapacityExceeded) {
		t.Fatalf("error = %v, want ErrQueueCapacityExceeded", err)
	}
	var capacityErr *QueueCapacityError
	if !errors.As(err, &capacityErr) {
		t.Fatalf("error type = %T, want *QueueCapacityError", err)
	}
	if capacityErr.Scope != scope || capacityErr.Pending != pending || capacityErr.Limit != limit {
		t.Fatalf("capacity error = %+v", capacityErr)
	}
}
