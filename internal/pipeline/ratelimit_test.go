package pipeline

import (
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func TestRateLimitHandlerRejectsAfterLimit(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	handler := NewRateLimitHandler(RateLimitConfig{
		Enabled:     true,
		MaxRequests: 2,
		Window:      time.Second,
	})
	handler.now = func() time.Time { return now }
	ctx := rateLimitContext("client-a")

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() first error = %v", err)
	}
	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() second error = %v", err)
	}

	err := handler.Handle(ctx)
	code, _ := AckError(err)
	if code != protocol.AckRejected {
		t.Fatalf("Ack code = %s, want %s", code, protocol.AckRejected)
	}
}

func TestRateLimitHandlerResetsAfterWindow(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	handler := NewRateLimitHandler(RateLimitConfig{
		Enabled:     true,
		MaxRequests: 1,
		Window:      time.Second,
	})
	handler.now = func() time.Time { return now }
	ctx := rateLimitContext("client-a")

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() first error = %v", err)
	}

	now = now.Add(time.Second)
	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() after reset error = %v", err)
	}
}

func rateLimitContext(clientID string) *Context {
	return &Context{
		Principal: &auth.Principal{ClientID: clientID},
		Packet:    protocol.NewPacket(1000, nil),
	}
}
