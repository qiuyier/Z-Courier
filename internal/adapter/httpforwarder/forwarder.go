package httpforwarder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
)

const (
	TargetType          = "http"
	InternalTokenHeader = "X-ZCourier-Internal-Token"
	TraceIDHeader       = "X-ZCourier-Trace-ID"
)

type Config struct {
	URL     string
	Token   string
	Timeout time.Duration
	Client  *http.Client
}

type Forwarder struct {
	url    string
	token  string
	client *http.Client
}

type UpstreamRequest struct {
	Version   uint8          `json:"version"`
	Flags     protocol.Flags `json:"flags"`
	MsgID     uint32         `json:"msg_id"`
	Seq       uint64         `json:"seq"`
	Timestamp int64          `json:"timestamp"`

	ClientID  string `json:"client_id"`
	DeviceID  string `json:"device_id"`
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	TraceID   string `json:"trace_id"`
	Body      []byte `json:"body"`
}

func New(config Config) *Forwarder {
	client := config.Client
	if client == nil {
		timeout := config.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	return &Forwarder{
		url:    config.URL,
		token:  config.Token,
		client: client,
	}
}

func (f *Forwarder) Forward(ctx context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	if f.url == "" {
		return nil, fmt.Errorf("http forwarder: empty url")
	}

	body, err := sonic.Marshal(newUpstreamRequest(packet))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if packet.TraceID != "" {
		req.Header.Set(TraceIDHeader, packet.TraceID)
	}
	if f.token != "" {
		req.Header.Set(InternalTokenHeader, f.token)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &router.ForwardResult{
			TargetType: TargetType,
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("http forwarder: status %s: %s", resp.Status, string(data))
	}

	return &router.ForwardResult{
		TargetType: TargetType,
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
	}, nil
}

func newUpstreamRequest(packet *protocol.Packet) UpstreamRequest {
	return UpstreamRequest{
		Version:   packet.Version,
		Flags:     packet.Flags,
		MsgID:     packet.MsgID,
		Seq:       packet.Seq,
		Timestamp: packet.Timestamp,
		ClientID:  packet.ClientID,
		DeviceID:  packet.DeviceID,
		SessionID: packet.SessionID,
		MessageID: packet.MessageID,
		TraceID:   packet.TraceID,
		Body:      packet.Body,
	}
}
