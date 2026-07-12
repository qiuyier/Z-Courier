package server

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
)

func TestTerminalEnvelopePublisherEncodesBodyFreeEvent(t *testing.T) {
	raw := &fakeRawTerminalPublisher{}
	publisher := &terminalEnvelopePublisher{next: raw}
	event := downlink.TerminalEvent{
		Version:        downlink.TerminalEventVersion,
		Type:           downlink.TerminalEventType,
		EventID:        "message-1:failed",
		MessageID:      "message-1",
		ClientID:       "client-1",
		DeviceID:       "device-1",
		MsgID:          2001,
		TerminalStatus: downlink.MessageStatusFailed,
		TerminalReason: downlink.TerminalReasonMaxAttempts,
		PolicyName:     "critical",
		Attempts:       5,
		MessageCreated: time.Unix(1, 0),
		TerminalAt:     time.Unix(2, 0),
		GatewayNode:    "gateway-a",
	}
	if err := publisher.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if bytes.Contains(raw.body, []byte(`"body"`)) {
		t.Fatalf("published terminal envelope contains body field: %s", raw.body)
	}
	var decoded downlink.TerminalEvent
	if err := sonic.Unmarshal(raw.body, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.EventID != event.EventID || decoded.TerminalReason != event.TerminalReason {
		t.Fatalf("decoded event = %+v", decoded)
	}
}

func TestTerminalEnvelopePublisherPropagatesPublishError(t *testing.T) {
	wantErr := errors.New("nsq unavailable")
	publisher := &terminalEnvelopePublisher{next: &fakeRawTerminalPublisher{err: wantErr}}
	if err := publisher.Publish(context.Background(), downlink.TerminalEvent{}); !errors.Is(err, wantErr) {
		t.Fatalf("Publish() error = %v, want %v", err, wantErr)
	}
}

type fakeRawTerminalPublisher struct {
	body []byte
	err  error
}

func (p *fakeRawTerminalPublisher) Publish(_ context.Context, body []byte) error {
	p.body = bytes.Clone(body)
	return p.err
}
