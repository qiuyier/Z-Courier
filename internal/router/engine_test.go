package router

import (
	"context"
	"errors"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func TestRouteMatches(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		msgID uint32
		want  bool
	}{
		{name: "single match", route: Route{MsgIDMin: 1000}, msgID: 1000, want: true},
		{name: "single miss", route: Route{MsgIDMin: 1000}, msgID: 1001, want: false},
		{name: "range match", route: Route{MsgIDMin: 1000, MsgIDMax: 1999}, msgID: 1500, want: true},
		{name: "range miss", route: Route{MsgIDMin: 1000, MsgIDMax: 1999}, msgID: 2000, want: false},
		{name: "empty route", route: Route{}, msgID: 1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.route.Matches(tt.msgID); got != tt.want {
				t.Fatalf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEngineForward(t *testing.T) {
	forwarder := &fakeForwarder{result: &ForwardResult{TargetType: "http", Status: "ok", StatusCode: 200}}
	engine := NewEngine([]Route{
		{Name: "chat", MsgIDMin: 1000, MsgIDMax: 1999, Forwarder: forwarder},
	})

	result, err := engine.Forward(context.Background(), protocol.NewPacket(1001, []byte("hello")))
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if result.RouteName != "chat" {
		t.Fatalf("RouteName = %q, want chat", result.RouteName)
	}
	if forwarder.packet.MsgID != 1001 {
		t.Fatalf("forwarded MsgID = %d, want 1001", forwarder.packet.MsgID)
	}
}

func TestEngineForwardRouteNotFound(t *testing.T) {
	engine := NewEngine([]Route{{Name: "chat", MsgIDMin: 1000, MsgIDMax: 1999, Forwarder: &fakeForwarder{}}})

	_, err := engine.Forward(context.Background(), protocol.NewPacket(2000, nil))
	if !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("Forward() error = %v, want %v", err, ErrRouteNotFound)
	}
}

type fakeForwarder struct {
	packet *protocol.Packet
	result *ForwardResult
	err    error
}

func (f *fakeForwarder) Forward(_ context.Context, packet *protocol.Packet) (*ForwardResult, error) {
	f.packet = packet
	return f.result, f.err
}
