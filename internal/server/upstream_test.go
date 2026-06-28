package server

import (
	"context"
	"errors"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
	"github.com/qiuyier/Z-Courier/internal/router"
)

func TestCapacityForwarderRejectsWhenFull(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	forwarder := newCapacityForwarder("chat", "http", 1, blockingForwarder{
		entered: entered,
		release: release,
	})

	done := make(chan error, 1)
	go func() {
		_, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, []byte("first")))
		done <- err
	}()

	<-entered
	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, []byte("second")))
	if !errors.Is(err, router.ErrOverloaded) {
		t.Fatalf("Forward() error = %v, want %v", err, router.ErrOverloaded)
	}
	if result == nil || result.RouteName != "chat" || result.TargetType != "http" || result.Status != resilience.ReasonOverloaded {
		t.Fatalf("result = %+v, want overloaded chat/http", result)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first Forward() error = %v", err)
	}
}

type blockingForwarder struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (f blockingForwarder) Forward(context.Context, *protocol.Packet) (*router.ForwardResult, error) {
	f.entered <- struct{}{}
	<-f.release
	return &router.ForwardResult{TargetType: "http", Status: "ok", StatusCode: 200}, nil
}
