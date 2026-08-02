package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/upstream"
	"gopkg.in/yaml.v3"
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

func TestWriteRouteDocumentAtomicReplacesCompleteDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes", "upstream.yaml")
	if err := writeRouteDocumentAtomic(path, routeDocumentForBackendA("127.0.0.1:18001")); err != nil {
		t.Fatalf("write backend A document error = %v", err)
	}
	if err := writeRouteDocumentAtomic(path, routeDocumentForBackendB("127.0.0.1:18002")); err != nil {
		t.Fatalf("write backend B document error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var document routeFileDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if document.Version != 1 || len(document.Routes) != 2 {
		t.Fatalf("document = %+v", document)
	}
	if document.Routes[0].Target.URL != "http://127.0.0.1:18002/gateway/upstream" || document.Routes[1].MsgIDMin != addedMsgID {
		t.Fatalf("routes = %+v", document.Routes)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".upstream-routes-*.tmp")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary route files = %v, error = %v", matches, err)
	}
}

func TestControlledBackendBlocksAndRecordsRequest(t *testing.T) {
	backend := newControlledBackend("test", "127.0.0.1:0", "upstream-token")
	if err := backend.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	block, err := backend.Block("blocked-message")
	if err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	defer block.Release()
	message := upstream.Message{
		MsgID:     primaryMsgID,
		ClientID:  defaultClientID,
		DeviceID:  defaultDeviceID,
		MessageID: "blocked-message",
		TraceID:   "blocked-message",
		Body:      []byte("blocked-body"),
	}
	body, err := sonic.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	responseDone := make(chan *http.Response, 1)
	errorDone := make(chan error, 1)
	go func() {
		request, requestErr := http.NewRequest(
			http.MethodPost,
			"http://"+backend.Address()+"/gateway/upstream",
			bytes.NewReader(body),
		)
		if requestErr != nil {
			errorDone <- requestErr
			return
		}
		request.Header.Set(httpforwarder.InternalTokenHeader, "upstream-token")
		request.Header.Set(httpforwarder.TraceIDHeader, message.TraceID)
		response, requestErr := (&http.Client{Timeout: time.Second}).Do(request)
		if requestErr != nil {
			errorDone <- requestErr
			return
		}
		responseDone <- response
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := block.WaitEntered(ctx); err != nil {
		t.Fatalf("WaitEntered() error = %v", err)
	}
	if backend.RecordCount() != 1 {
		t.Fatalf("RecordCount() = %d, want 1", backend.RecordCount())
	}
	select {
	case response := <-responseDone:
		_ = response.Body.Close()
		t.Fatal("backend response completed before release")
	case err := <-errorDone:
		t.Fatalf("backend request error before release = %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	block.Release()
	select {
	case response := <-responseDone:
		defer response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
		}
	case err := <-errorDone:
		t.Fatalf("backend request error = %v", err)
	case <-ctx.Done():
		t.Fatalf("wait for backend response: %v", ctx.Err())
	}

	record, err := backend.Record(0)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if record.Message.MessageID != message.MessageID || !bytes.Equal(record.RawBody, body) {
		t.Fatalf("record = %+v raw=%q", record.Message, record.RawBody)
	}
}
