package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

// Client owns one authenticated gateway connection.
type Client struct {
	config normalizedConfig

	connectMu          sync.Mutex
	mu                 sync.RWMutex
	state              State
	closed             bool
	runtime            *connectionRuntime
	nextRuntimeID      uint64
	activeCancel       context.CancelFunc
	binding            Binding
	lastError          error
	pendingBeforeReady []*protocol.Packet

	waitersMu sync.Mutex
	waiters   map[string]*ackWaiter
	writeGate chan struct{}
	deduper   *messageDeduper

	sequence atomic.Uint64
}

// New validates config and creates a disconnected Client.
func New(config Config) (*Client, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	return &Client{
		config:    normalized,
		state:     StateDisconnected,
		waiters:   make(map[string]*ackWaiter),
		writeGate: make(chan struct{}, 1),
		deduper:   newMessageDeduper(normalized.downlinkDedupCapacity),
	}, nil
}

// State returns the current connection lifecycle state.
func (client *Client) State() State {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.state
}

// Ready reports whether AUTH/BIND completed for the active connection.
func (client *Client) Ready() bool {
	return client.State() == StateReady
}

// Binding returns the identity accepted by the gateway. It is zero before the
// client reaches StateReady.
func (client *Client) Binding() Binding {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.binding
}

// LastError returns the most recent active-connection failure. A new Connect
// attempt clears it.
func (client *Client) LastError() error {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.lastError
}

// Connect opens a TCP connection, performs AUTH/BIND, and waits until the
// gateway accepts the binding. Concurrent calls are serialized.
func (client *Client) Connect(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidConfig)
	}

	client.connectMu.Lock()
	defer client.connectMu.Unlock()

	alreadyReady, err := client.beginConnect()
	if err != nil || alreadyReady {
		return err
	}

	token, connection, err := client.connectTransport(ctx)
	if err != nil {
		client.failConnection(nil)
		return err
	}
	runtime, err := client.installConnection(connection)
	if err != nil {
		_ = connection.Close()
		return err
	}

	bindPacket, err := client.newBindPacket(token)
	if err != nil {
		client.failConnection(runtime)
		return err
	}
	binding, err := client.performBind(ctx, connection, bindPacket)
	if err != nil {
		client.failConnection(runtime)
		return err
	}
	if err := client.completeBinding(runtime, binding, token); err != nil {
		client.failConnection(runtime)
		return err
	}
	return nil
}

// Close permanently stops the Client and interrupts an active Connect call.
// It is safe to call more than once.
func (client *Client) Close() error {
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return nil
	}
	client.closed = true
	client.state = StateClosing
	cancel := client.activeCancel
	client.activeCancel = nil
	runtime := client.runtime
	client.runtime = nil
	client.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	var closeErr error
	if runtime != nil {
		runtime.finish(ErrClientClosed)
		client.failAckWaiters(runtime.id, ErrClientClosed)
		closeErr = runtime.connection.Close()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
	}

	client.mu.Lock()
	client.state = StateClosed
	client.binding = Binding{}
	client.lastError = ErrClientClosed
	client.pendingBeforeReady = nil
	client.mu.Unlock()
	return closeErr
}

func (client *Client) beginConnect() (bool, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return false, ErrClientClosed
	}
	if client.state == StateReady {
		return true, nil
	}
	client.state = StateConnecting
	client.binding = Binding{}
	client.lastError = nil
	client.pendingBeforeReady = nil
	return false, nil
}

func (client *Client) connectTransport(ctx context.Context) (string, net.Conn, error) {
	connectCtx, cancel := context.WithTimeout(ctx, client.config.connectTimeout)
	if !client.setActiveCancel(cancel) {
		cancel()
		return "", nil, ErrClientClosed
	}

	token, err := client.config.tokenProvider(connectCtx)
	if err != nil {
		connectContextErr := connectCtx.Err()
		cancel()
		client.clearActiveCancel()
		if contextErr := client.connectContextError(ctx.Err(), connectContextErr); contextErr != nil {
			return "", nil, contextErr
		}
		return "", nil, fmt.Errorf("%w: %w", ErrTokenUnavailable, err)
	}
	if token == "" {
		connectContextErr := connectCtx.Err()
		cancel()
		client.clearActiveCancel()
		if contextErr := client.connectContextError(ctx.Err(), connectContextErr); contextErr != nil {
			return "", nil, contextErr
		}
		return "", nil, ErrTokenUnavailable
	}

	connection, err := client.config.dialer.DialContext(connectCtx, client.config.network, client.config.address)
	connectContextErr := connectCtx.Err()
	cancel()
	client.clearActiveCancel()
	if err != nil {
		if contextErr := client.connectContextError(ctx.Err(), connectContextErr); contextErr != nil {
			return "", nil, contextErr
		}
		return "", nil, fmt.Errorf("client: dial %s: %w", client.config.address, err)
	}
	return token, connection, nil
}

func (client *Client) installConnection(connection net.Conn) (*connectionRuntime, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return nil, ErrClientClosed
	}
	client.nextRuntimeID++
	runtime := newConnectionRuntime(client.nextRuntimeID, connection, client.config.inboundBuffer)
	client.runtime = runtime
	client.state = StateBinding
	return runtime, nil
}

func (client *Client) performBind(ctx context.Context, connection net.Conn, packet *protocol.Packet) (Binding, error) {
	bindCtx, cancel := context.WithTimeout(ctx, client.config.bindTimeout)
	if !client.setActiveCancel(cancel) {
		cancel()
		return Binding{}, ErrClientClosed
	}
	if deadline, ok := bindCtx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			cancel()
			client.clearActiveCancel()
			return Binding{}, fmt.Errorf("client: set bind deadline: %w", err)
		}
	}
	interruptDone := make(chan struct{})
	stopInterrupt := context.AfterFunc(bindCtx, func() {
		_ = connection.SetDeadline(time.Now())
		close(interruptDone)
	})

	err := writePacketFrame(connection, packet, client.config.maxFramePayloadSize)
	var binding Binding
	if err == nil {
		binding, err = client.waitForBindAck(connection, packet.MessageID)
	}
	bindContextErr := bindCtx.Err()
	if !stopInterrupt() {
		<-interruptDone
	}
	cancel()
	client.clearActiveCancel()

	if err != nil {
		if contextErr := client.bindContextError(ctx.Err(), bindContextErr); contextErr != nil {
			return Binding{}, contextErr
		}
		if client.isClosed() {
			return Binding{}, ErrClientClosed
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return Binding{}, fmt.Errorf("%w: %w", ErrBindTimeout, err)
		}
		return Binding{}, err
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return Binding{}, fmt.Errorf("client: clear bind deadline: %w", err)
	}
	return binding, nil
}

func (client *Client) completeBinding(runtime *connectionRuntime, binding Binding, token string) error {
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return ErrClientClosed
	}
	if client.runtime != runtime {
		client.mu.Unlock()
		return fmt.Errorf("client: connection disappeared during bind")
	}
	runtime.token = token
	client.binding = binding
	client.state = StateReady
	pending := client.pendingBeforeReady
	client.pendingBeforeReady = nil
	client.mu.Unlock()

	if client.config.downlinkHandler != nil {
		go client.downlinkLoop(runtime)
	}
	go client.readLoop(runtime, pending)
	return nil
}

func (client *Client) bufferBeforeReady(packet *protocol.Packet) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.pendingBeforeReady) >= client.config.maxPendingBeforeReady {
		return fmt.Errorf("%w: max %d", ErrPendingBeforeReadyOverflow, client.config.maxPendingBeforeReady)
	}
	client.pendingBeforeReady = append(client.pendingBeforeReady, packet)
	return nil
}

func (client *Client) failConnection(runtime *connectionRuntime) {
	if runtime != nil {
		client.terminateRuntime(runtime, ErrConnectionClosed)
		return
	}
	client.mu.Lock()
	client.activeCancel = nil
	client.binding = Binding{}
	client.pendingBeforeReady = nil
	if client.closed {
		client.state = StateClosed
	} else {
		client.state = StateDisconnected
	}
	client.mu.Unlock()
}

func (client *Client) setActiveCancel(cancel context.CancelFunc) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return false
	}
	client.activeCancel = cancel
	return true
}

func (client *Client) clearActiveCancel() {
	client.mu.Lock()
	client.activeCancel = nil
	client.mu.Unlock()
}

func (client *Client) isClosed() bool {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return client.closed
}

func (client *Client) connectContextError(parentErr, connectErr error) error {
	if client.isClosed() {
		return ErrClientClosed
	}
	if parentErr != nil {
		return parentErr
	}
	if errors.Is(connectErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrConnectTimeout, connectErr)
	}
	return nil
}

func (client *Client) bindContextError(parentErr, bindErr error) error {
	if client.isClosed() {
		return ErrClientClosed
	}
	if parentErr != nil {
		return parentErr
	}
	if errors.Is(bindErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrBindTimeout, bindErr)
	}
	return nil
}
