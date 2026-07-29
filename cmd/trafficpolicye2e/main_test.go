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

func TestParseConfig(t *testing.T) {
	configuration, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if configuration.GatewayAddress != defaultGatewayAddress ||
		configuration.BackendAddress != defaultBackendAddress ||
		configuration.Timeout != 20*time.Second {
		t.Fatalf("configuration = %+v", configuration)
	}

	if _, err := parseConfig([]string{"-timeout", "0s"}, io.Discard); err == nil {
		t.Fatal("parseConfig() error = nil, want invalid timeout")
	}
}

func TestRecordingBackendAcceptsAndRecordsEnvelope(t *testing.T) {
	backend := newRecordingBackend("127.0.0.1:0", defaultUpstreamToken)
	if err := backend.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	message := upstream.Message{
		MsgID:     standardMsgID,
		ClientID:  "client-a",
		DeviceID:  "device-a",
		MessageID: "message-a",
		TraceID:   "trace-a",
		Body:      []byte("hello"),
	}
	body, err := sonic.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+backend.Address()+"/gateway/upstream",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set(httpforwarder.InternalTokenHeader, defaultUpstreamToken)
	request.Header.Set(httpforwarder.TraceIDHeader, message.TraceID)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	if backend.RecordCount() != 1 {
		t.Fatalf("RecordCount() = %d, want 1", backend.RecordCount())
	}
	record, err := backend.Record(0)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if record.Message.MessageID != message.MessageID || !bytes.Equal(record.Message.Body, message.Body) {
		t.Fatalf("record = %+v", record)
	}
}
