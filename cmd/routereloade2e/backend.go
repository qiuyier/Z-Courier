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

type backendRecord struct {
	Message upstream.Message
	RawBody []byte
}

type backendBlock struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func newBackendBlock() *backendBlock {
	return &backendBlock{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (block *backendBlock) WaitEntered(ctx context.Context) error {
	select {
	case <-block.entered:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (block *backendBlock) Release() {
	if block == nil {
		return
	}
	block.releaseOnce.Do(func() { close(block.release) })
}

type controlledBackend struct {
	name          string
	address       string
	expectedToken string

	mu      sync.Mutex
	server  *http.Server
	done    chan error
	records []backendRecord
	blocks  map[string]*backendBlock
}

func newControlledBackend(name, address, expectedToken string) *controlledBackend {
	return &controlledBackend{
		name:          name,
		address:       address,
		expectedToken: expectedToken,
		blocks:        make(map[string]*backendBlock),
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
	blocks := make([]*backendBlock, 0, len(backend.blocks))
	for _, block := range backend.blocks {
		blocks = append(blocks, block)
	}
	backend.blocks = make(map[string]*backendBlock)
	backend.server = nil
	backend.done = nil
	backend.mu.Unlock()
	for _, block := range blocks {
		block.Release()
	}
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

func (backend *controlledBackend) Block(messageID string) (*backendBlock, error) {
	if messageID == "" {
		return nil, errors.New("backend block message ID is empty")
	}
	block := newBackendBlock()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if _, exists := backend.blocks[messageID]; exists {
		return nil, fmt.Errorf("backend %s already blocks message %q", backend.name, messageID)
	}
	backend.blocks[messageID] = block
	return block, nil
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
	block := backend.blocks[message.MessageID]
	delete(backend.blocks, message.MessageID)
	backend.mu.Unlock()

	if block != nil {
		block.enteredOnce.Do(func() { close(block.entered) })
		select {
		case <-block.release:
		case <-request.Context().Done():
			return
		}
	}
	writer.WriteHeader(http.StatusAccepted)
}
