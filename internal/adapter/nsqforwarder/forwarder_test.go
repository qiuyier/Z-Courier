package nsqforwarder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adapter/upstream"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func TestForwarderPublishesPacketEnvelope(t *testing.T) {
	producer := &fakeProducer{}
	forwarder, err := New(Config{
		Topic:    "message_events",
		Producer: producer,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	packet := protocol.NewPacket(2001, []byte("hello"))
	packet.ClientID = "client-1"
	packet.DeviceID = "device-1"
	packet.SessionID = "session-1"
	packet.MessageID = "message-1"
	packet.TraceID = "trace-1"

	result, err := forwarder.Forward(context.Background(), packet)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}

	if result.TargetType != TargetType {
		t.Fatalf("TargetType = %q, want %q", result.TargetType, TargetType)
	}
	if result.Status != StatusPublished {
		t.Fatalf("Status = %q, want %q", result.Status, StatusPublished)
	}
	if producer.topic != "message_events" {
		t.Fatalf("topic = %q, want message_events", producer.topic)
	}

	var got upstream.Message
	if err := sonic.Unmarshal(producer.body, &got); err != nil {
		t.Fatalf("decode published body: %v", err)
	}
	if got.MsgID != packet.MsgID || got.ClientID != packet.ClientID || got.DeviceID != packet.DeviceID {
		t.Fatalf("message identity = msgID:%d client:%q device:%q", got.MsgID, got.ClientID, got.DeviceID)
	}
	if string(got.Body) != "hello" {
		t.Fatalf("Body = %q, want hello", got.Body)
	}
}

func TestForwarderReturnsErrorOnPublishFailure(t *testing.T) {
	wantErr := errors.New("publish failed")
	forwarder, err := New(Config{
		Topic:    "message_events",
		Producer: &fakeProducer{err: wantErr},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(2001, nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Forward() error = %v, want %v", err, wantErr)
	}
	if result == nil || result.TargetType != TargetType || result.Status != StatusFailed {
		t.Fatalf("result = %+v, want failed NSQ result", result)
	}
}

func TestNewAllowsShortReadTimeout(t *testing.T) {
	forwarder, err := New(Config{
		Address:     "127.0.0.1:4150",
		Topic:       "message_events",
		ReadTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := forwarder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestForwarderCloseStopsProducerOnce(t *testing.T) {
	producer := &fakeProducer{}
	forwarder, err := New(Config{
		Topic:    "message_events",
		Producer: producer,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := forwarder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := forwarder.Close(); err != nil {
		t.Fatalf("Close() second error = %v", err)
	}
	if producer.stopCount != 1 {
		t.Fatalf("Stop count = %d, want 1", producer.stopCount)
	}
}

type fakeProducer struct {
	topic     string
	body      []byte
	err       error
	stopCount int
}

func (p *fakeProducer) Publish(topic string, body []byte) error {
	p.topic = topic
	p.body = append([]byte(nil), body...)
	return p.err
}

func (p *fakeProducer) Stop() {
	p.stopCount++
}
