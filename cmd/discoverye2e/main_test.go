package main

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/upstream"
)

func TestParseConfigRequiresGatewayBinary(t *testing.T) {
	if _, err := parseConfig(nil, io.Discard); err == nil {
		t.Fatal("parseConfig() error = nil, want missing gateway binary error")
	}

	configuration, err := parseConfig([]string{"-gateway-bin", "/tmp/gateway"}, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if configuration.GatewayBin != "/tmp/gateway" || configuration.Timeout != 30*time.Second {
		t.Fatalf("configuration = %+v", configuration)
	}
}

func TestControlledBackendRecordsEnvelopeAndBehaviors(t *testing.T) {
	backend := newControlledBackend("test", "127.0.0.1:0", "internal-token")
	if err := backend.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	message := upstream.Message{
		MsgID:     1101,
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MessageID: "message-1",
		TraceID:   "trace-1",
		Body:      []byte("hello"),
	}
	body, err := sonic.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	client := &http.Client{Timeout: time.Second}

	response, err := postBackend(client, backend.Address(), body, message.TraceID)
	if err != nil {
		t.Fatalf("healthy request error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("healthy status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}

	backend.SetResponseStatus(http.StatusInternalServerError)
	response, err = postBackend(client, backend.Address(), body, message.TraceID)
	if err != nil {
		t.Fatalf("status request error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failure status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}

	backend.SetDropResponse()
	if _, err := postBackend(client, backend.Address(), body, message.TraceID); err == nil {
		t.Fatal("drop-response request error = nil")
	}

	if backend.RecordCount() != 3 {
		t.Fatalf("RecordCount() = %d, want 3", backend.RecordCount())
	}
	record, err := backend.Record(2)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if record.Message.MessageID != message.MessageID || !bytes.Equal(record.RawBody, body) {
		t.Fatalf("record = %+v raw=%q", record.Message, record.RawBody)
	}
}

func postBackend(client *http.Client, address string, body []byte, traceID string) (*http.Response, error) {
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+address+"/gateway/upstream",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set(httpforwarder.InternalTokenHeader, "internal-token")
	request.Header.Set(httpforwarder.TraceIDHeader, traceID)
	return client.Do(request)
}
