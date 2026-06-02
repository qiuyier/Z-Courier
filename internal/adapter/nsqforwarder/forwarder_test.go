package nsqforwarder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	nsq "github.com/nsqio/go-nsq"
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

func TestForwarderPublishesRoundRobin(t *testing.T) {
	producers := []*fakeProducer{{}, {}, {}}
	forwarder, err := New(Config{
		Topic: "message_events",
		Producers: []Producer{
			producers[0],
			producers[1],
			producers[2],
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for range 4 {
		_, err := forwarder.Forward(context.Background(), protocol.NewPacket(2001, []byte("hello")))
		if err != nil {
			t.Fatalf("Forward() error = %v", err)
		}
	}

	if producers[0].publishCount != 2 {
		t.Fatalf("producer 0 publish count = %d, want 2", producers[0].publishCount)
	}
	if producers[1].publishCount != 1 {
		t.Fatalf("producer 1 publish count = %d, want 1", producers[1].publishCount)
	}
	if producers[2].publishCount != 1 {
		t.Fatalf("producer 2 publish count = %d, want 1", producers[2].publishCount)
	}
}

func TestForwarderFailsOverToNextProducer(t *testing.T) {
	wantErr := errors.New("first producer failed")
	producers := []*fakeProducer{
		{err: wantErr},
		{},
	}
	forwarder, err := New(Config{
		Topic:         "message_events",
		RetryAttempts: 1,
		Producers: []Producer{
			producers[0],
			producers[1],
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = forwarder.Forward(context.Background(), protocol.NewPacket(2001, []byte("hello")))
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if producers[0].publishCount != 1 {
		t.Fatalf("producer 0 publish count = %d, want 1", producers[0].publishCount)
	}
	if producers[1].publishCount != 1 {
		t.Fatalf("producer 1 publish count = %d, want 1", producers[1].publishCount)
	}
}

func TestForwarderStopsRetryingAfterConfiguredAttempts(t *testing.T) {
	wantErr := errors.New("publish failed")
	producers := []*fakeProducer{
		{err: wantErr},
		{err: wantErr},
		{err: wantErr},
	}
	forwarder, err := New(Config{
		Topic:         "message_events",
		RetryAttempts: 1,
		Producers: []Producer{
			producers[0],
			producers[1],
			producers[2],
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = forwarder.Forward(context.Background(), protocol.NewPacket(2001, nil))
	if err == nil {
		t.Fatal("Forward() error = nil, want error")
	}
	if producers[0].publishCount != 1 {
		t.Fatalf("producer 0 publish count = %d, want 1", producers[0].publishCount)
	}
	if producers[1].publishCount != 1 {
		t.Fatalf("producer 1 publish count = %d, want 1", producers[1].publishCount)
	}
	if producers[2].publishCount != 0 {
		t.Fatalf("producer 2 publish count = %d, want 0", producers[2].publishCount)
	}
}

func TestNewCreatesProducersForAddresses(t *testing.T) {
	var gotAddresses []string
	forwarder, err := New(Config{
		Addresses:     []string{"127.0.0.1:4150", "127.0.0.1:4151"},
		Topic:         "message_events",
		ReadTimeout:   5 * time.Second,
		PublishMode:   PublishModeRoundRobin,
		RetryAttempts: 1,
		Factory: func(address string, nsqConfig *nsq.Config) (Producer, error) {
			gotAddresses = append(gotAddresses, address)
			if nsqConfig.ReadTimeout != 5*time.Second {
				t.Fatalf("ReadTimeout = %v, want 5s", nsqConfig.ReadTimeout)
			}
			if nsqConfig.HeartbeatInterval >= nsqConfig.ReadTimeout {
				t.Fatalf("HeartbeatInterval = %v, want less than ReadTimeout %v", nsqConfig.HeartbeatInterval, nsqConfig.ReadTimeout)
			}
			return &fakeProducer{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := forwarder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	want := []string{"127.0.0.1:4150", "127.0.0.1:4151"}
	if len(gotAddresses) != len(want) {
		t.Fatalf("addresses = %v, want %v", gotAddresses, want)
	}
	for i := range want {
		if gotAddresses[i] != want[i] {
			t.Fatalf("addresses = %v, want %v", gotAddresses, want)
		}
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
	producers := []*fakeProducer{{}, {}}
	forwarder, err := New(Config{
		Topic: "message_events",
		Producers: []Producer{
			producers[0],
			producers[1],
		},
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
	if producers[0].stopCount != 1 {
		t.Fatalf("producer 0 Stop count = %d, want 1", producers[0].stopCount)
	}
	if producers[1].stopCount != 1 {
		t.Fatalf("producer 1 Stop count = %d, want 1", producers[1].stopCount)
	}
}

type fakeProducer struct {
	topic        string
	body         []byte
	err          error
	stopCount    int
	publishCount int
}

func (p *fakeProducer) Publish(topic string, body []byte) error {
	p.publishCount++
	p.topic = topic
	p.body = append([]byte(nil), body...)
	return p.err
}

func (p *fakeProducer) Stop() {
	p.stopCount++
}
