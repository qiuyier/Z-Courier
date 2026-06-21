package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

type connectionRuntime struct {
	id         uint64
	connection net.Conn
	token      string
	inbound    chan *protocol.Packet
	done       chan struct{}
	context    context.Context
	cancel     context.CancelFunc

	finishOnce sync.Once
	errMu      sync.RWMutex
	err        error
}

func newConnectionRuntime(id uint64, connection net.Conn, inboundBuffer int) *connectionRuntime {
	runtimeContext, cancel := context.WithCancel(context.Background())
	return &connectionRuntime{
		id:         id,
		connection: connection,
		inbound:    make(chan *protocol.Packet, inboundBuffer),
		done:       make(chan struct{}),
		context:    runtimeContext,
		cancel:     cancel,
	}
}

func (runtime *connectionRuntime) finish(err error) bool {
	finished := false
	runtime.finishOnce.Do(func() {
		if err == nil {
			err = ErrConnectionClosed
		}
		runtime.errMu.Lock()
		runtime.err = err
		runtime.errMu.Unlock()
		runtime.cancel()
		close(runtime.done)
		finished = true
	})
	return finished
}

func (runtime *connectionRuntime) failure() error {
	runtime.errMu.RLock()
	defer runtime.errMu.RUnlock()
	if runtime.err == nil {
		return ErrConnectionClosed
	}
	return runtime.err
}

func (client *Client) readLoop(runtime *connectionRuntime, pending []*protocol.Packet) {
	for _, packet := range pending {
		if err := client.dispatchPacket(runtime, packet); err != nil {
			client.terminateRuntime(runtime, err)
			return
		}
	}

	for {
		packet, err := readPacketFrame(
			runtime.connection,
			client.config.maxFramePayloadSize,
			client.config.maxBodySize,
		)
		if err != nil {
			client.terminateRuntime(runtime, err)
			return
		}
		if err := client.dispatchPacket(runtime, packet); err != nil {
			client.terminateRuntime(runtime, err)
			return
		}
	}
}

func (client *Client) dispatchPacket(runtime *connectionRuntime, packet *protocol.Packet) error {
	if packet.MsgID == protocol.MsgIDAck {
		client.dispatchAck(runtime, packet)
		return nil
	}

	select {
	case runtime.inbound <- packet:
		return nil
	default:
		return ErrInboundOverflow
	}
}

func (client *Client) terminateRuntime(runtime *connectionRuntime, cause error) {
	failure := connectionFailure(cause)
	if runtime.finish(failure) {
		_ = runtime.connection.Close()
		client.failAckWaiters(runtime.id, failure)
	}

	startReconnect := false
	client.mu.Lock()
	if client.runtime == runtime {
		client.runtime = nil
		client.binding = Binding{}
		client.lastError = failure
		if client.closed {
			client.setStateLocked(StateClosed)
		} else if client.config.reconnect.enabled {
			client.setStateLocked(StateReconnectWait)
			if !client.reconnectRunning {
				client.reconnectRunning = true
				startReconnect = true
			}
		} else {
			client.setStateLocked(StateDisconnected)
		}
	}
	client.mu.Unlock()
	if startReconnect {
		go client.reconnectLoop(failure)
	}
}

func connectionFailure(cause error) error {
	if cause == nil {
		return ErrConnectionClosed
	}
	if errors.Is(cause, ErrClientClosed) || errors.Is(cause, ErrConnectionClosed) {
		return cause
	}
	return fmt.Errorf("%w: %w", ErrConnectionClosed, cause)
}

// Receive returns the next non-ACK packet from the active connection. Raw
// consumers can acknowledge delivery with AcknowledgeDownlink.
func (client *Client) Receive(ctx context.Context) (*protocol.Packet, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidConfig)
	}
	if client.config.downlinkHandler != nil {
		return nil, ErrReceiveUnavailable
	}
	runtime, err := client.readyRuntime()
	if err != nil {
		return nil, err
	}

	for {
		select {
		case packet := <-runtime.inbound:
			return packet, nil
		case <-runtime.done:
			select {
			case packet := <-runtime.inbound:
				return packet, nil
			default:
				return nil, runtime.failure()
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (client *Client) readyRuntime() (*connectionRuntime, error) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.closed {
		return nil, ErrClientClosed
	}
	if client.state != StateReady || client.runtime == nil {
		return nil, ErrNotReady
	}
	return client.runtime, nil
}

func (client *Client) runtimeIsReady(runtime *connectionRuntime) bool {
	client.mu.RLock()
	defer client.mu.RUnlock()
	return !client.closed && client.state == StateReady && client.runtime == runtime
}
