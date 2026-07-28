package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/upstream"
)

const maxBackendRequestBody = 2 << 20

type backendBehavior uint8

const (
	backendHealthy backendBehavior = iota
	backendDropResponse
	backendStatus
)

type backendRecord struct {
	Message upstream.Message
	RawBody []byte
}

type controlledBackend struct {
	name          string
	address       string
	expectedToken string

	mu       sync.Mutex
	server   *http.Server
	done     chan error
	behavior backendBehavior
	status   int
	records  []backendRecord
}

func newControlledBackend(name, address, expectedToken string) *controlledBackend {
	return &controlledBackend{
		name:          name,
		address:       address,
		expectedToken: expectedToken,
		status:        http.StatusAccepted,
	}
}

func (backend *controlledBackend) Start() error {
	listener, err := net.Listen("tcp", backend.address)
	if err != nil {
		return fmt.Errorf("listen backend %s on %s: %w", backend.name, backend.address, err)
	}

	server := &http.Server{
		Handler:           backend,
		ReadHeaderTimeout: time.Second,
	}
	done := make(chan error, 1)

	backend.mu.Lock()
	if backend.server != nil {
		backend.mu.Unlock()
		_ = listener.Close()
		return fmt.Errorf("backend %s is already running", backend.name)
	}
	backend.address = listener.Addr().String()
	backend.server = server
	backend.done = done
	backend.mu.Unlock()

	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		done <- serveErr
		close(done)
	}()
	return nil
}

func (backend *controlledBackend) Close() error {
	backend.mu.Lock()
	server := backend.server
	done := backend.done
	backend.server = nil
	backend.done = nil
	backend.mu.Unlock()
	if server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(ctx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}

	var serveErr error
	if done != nil {
		serveErr = <-done
	}
	return errors.Join(shutdownErr, serveErr)
}

func (backend *controlledBackend) Address() string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.address
}

func (backend *controlledBackend) SetHealthy() {
	backend.mu.Lock()
	backend.behavior = backendHealthy
	backend.status = http.StatusAccepted
	backend.mu.Unlock()
}

func (backend *controlledBackend) SetDropResponse() {
	backend.mu.Lock()
	backend.behavior = backendDropResponse
	backend.mu.Unlock()
}

func (backend *controlledBackend) SetResponseStatus(status int) {
	backend.mu.Lock()
	backend.behavior = backendStatus
	backend.status = status
	backend.mu.Unlock()
}

func (backend *controlledBackend) RecordCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.records)
}

func (backend *controlledBackend) Record(index int) (backendRecord, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if index < 0 || index >= len(backend.records) {
		return backendRecord{}, fmt.Errorf(
			"backend %s record index %d is outside %d recorded requests",
			backend.name,
			index,
			len(backend.records),
		)
	}
	record := backend.records[index]
	record.RawBody = bytes.Clone(record.RawBody)
	record.Message.Body = bytes.Clone(record.Message.Body)
	return record, nil
}

func (backend *controlledBackend) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/gateway/upstream" {
		http.NotFound(writer, request)
		return
	}
	if request.Header.Get(httpforwarder.InternalTokenHeader) != backend.expectedToken {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}

	rawBody, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxBackendRequestBody))
	if err != nil {
		http.Error(writer, "invalid request body", http.StatusBadRequest)
		return
	}
	var message upstream.Message
	if err := sonic.Unmarshal(rawBody, &message); err != nil {
		http.Error(writer, "invalid request envelope", http.StatusBadRequest)
		return
	}
	if request.Header.Get(httpforwarder.TraceIDHeader) != message.TraceID {
		http.Error(writer, "trace header mismatch", http.StatusBadRequest)
		return
	}

	backend.mu.Lock()
	backend.records = append(backend.records, backendRecord{
		Message: message,
		RawBody: bytes.Clone(rawBody),
	})
	behavior := backend.behavior
	status := backend.status
	backend.mu.Unlock()

	if behavior == backendDropResponse {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			http.Error(writer, "connection hijacking unavailable", http.StatusInternalServerError)
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_ = connection.Close()
		return
	}
	if behavior == backendStatus && status != 0 {
		http.Error(writer, http.StatusText(status), status)
		return
	}

	writer.WriteHeader(http.StatusAccepted)
}
