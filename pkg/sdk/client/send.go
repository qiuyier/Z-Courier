package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

// SendRequest describes one client-to-gateway business packet.
type SendRequest struct {
	MsgID       uint32
	Body        []byte
	MessageID   string
	TraceID     string
	Flags       protocol.Flags
	AckRequired bool
}

// SendResult identifies the sent packet and includes the gateway ACK when one
// was requested.
type SendResult struct {
	MessageID string
	Ack       *protocol.Ack
}

// AckError contains a non-success gateway ACK.
type AckError struct {
	Ack  protocol.Ack
	kind error
}

func (err *AckError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Ack.Reason == "" {
		return fmt.Sprintf("client: ACK failed with code %q", err.Ack.Code)
	}
	return fmt.Sprintf("client: ACK failed with code %q: %s", err.Ack.Code, err.Ack.Reason)
}

func (err *AckError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.kind
}

type ackResult struct {
	ack *protocol.Ack
	err error
}

type ackWaiter struct {
	runtimeID uint64
	msgID     uint32
	result    chan ackResult
}

// Send writes one business packet. When AckRequired is true it waits for the
// matching gateway ACK using MessageID correlation.
func (client *Client) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if ctx == nil {
		return SendResult{}, fmt.Errorf("%w: context is nil", ErrInvalidConfig)
	}
	if protocol.IsReservedMsgID(request.MsgID) {
		return SendResult{}, fmt.Errorf("%w: %d", ErrReservedMsgID, request.MsgID)
	}

	runtime, binding, err := client.sendSnapshot()
	if err != nil {
		return SendResult{}, err
	}
	messageID := request.MessageID
	if messageID == "" {
		messageID, err = newMessageID("zc-msg-")
		if err != nil {
			return SendResult{}, err
		}
	}
	traceID := request.TraceID
	if traceID == "" {
		traceID = messageID
	}

	packet := protocol.NewPacket(request.MsgID, request.Body)
	packet.Flags = request.Flags
	ackRequired := request.AckRequired || request.Flags&protocol.FlagAckRequired != 0
	if ackRequired {
		packet.Flags |= protocol.FlagAckRequired
	}
	packet.Seq = client.sequence.Add(1)
	packet.Timestamp = time.Now().UnixMilli()
	packet.ClientID = binding.ClientID
	packet.DeviceID = binding.DeviceID
	packet.SessionID = binding.SessionID
	packet.MessageID = messageID
	packet.TraceID = traceID
	packet.Token = runtime.token

	result := SendResult{MessageID: messageID}
	var waiter *ackWaiter
	if ackRequired {
		waiter = &ackWaiter{
			runtimeID: runtime.id,
			msgID:     request.MsgID,
			result:    make(chan ackResult, 1),
		}
		if err := client.registerAckWaiter(messageID, waiter); err != nil {
			return result, err
		}
		defer client.unregisterAckWaiter(messageID, waiter)
	}

	if err := client.writePacket(ctx, runtime, packet); err != nil {
		return result, err
	}
	if waiter == nil {
		return result, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, client.config.ackTimeout)
	defer cancel()
	ack, err := client.waitForAck(waitCtx, ctx, runtime, waiter)
	if err != nil {
		return result, err
	}
	result.Ack = ack
	return result, nil
}

func (client *Client) sendSnapshot() (*connectionRuntime, Binding, error) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.closed {
		return nil, Binding{}, ErrClientClosed
	}
	if client.state != StateReady || client.runtime == nil {
		return nil, Binding{}, ErrNotReady
	}
	return client.runtime, client.binding, nil
}

func (client *Client) writePacket(ctx context.Context, runtime *connectionRuntime, packet *protocol.Packet) error {
	writeCtx, cancel := context.WithTimeout(ctx, client.config.writeTimeout)
	defer cancel()

	select {
	case client.writeGate <- struct{}{}:
		defer func() { <-client.writeGate }()
	case <-runtime.done:
		return runtime.failure()
	case <-writeCtx.Done():
		return writeContextError(ctx.Err(), writeCtx.Err())
	}
	if !client.runtimeIsReady(runtime) {
		return runtime.failure()
	}

	if deadline, ok := writeCtx.Deadline(); ok {
		if err := runtime.connection.SetWriteDeadline(deadline); err != nil {
			client.terminateRuntime(runtime, err)
			return err
		}
	}
	interruptDone := make(chan struct{})
	stopInterrupt := context.AfterFunc(writeCtx, func() {
		_ = runtime.connection.SetWriteDeadline(time.Now())
		close(interruptDone)
	})
	err := writePacketFrame(runtime.connection, packet, client.config.maxFramePayloadSize)
	if !stopInterrupt() {
		<-interruptDone
	}
	if clearErr := runtime.connection.SetWriteDeadline(time.Time{}); err == nil && clearErr != nil {
		err = clearErr
	}
	if err != nil {
		failure := connectionFailure(err)
		client.terminateRuntime(runtime, failure)
		if contextErr := writeContextError(ctx.Err(), writeCtx.Err()); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("client: write packet: %w", failure)
	}
	return nil
}

func writeContextError(parentErr, writeErr error) error {
	if parentErr != nil {
		return parentErr
	}
	if errors.Is(writeErr, context.DeadlineExceeded) {
		return fmt.Errorf("client: write timeout: %w", writeErr)
	}
	return nil
}

func (client *Client) registerAckWaiter(messageID string, waiter *ackWaiter) error {
	client.waitersMu.Lock()
	defer client.waitersMu.Unlock()
	if _, exists := client.waiters[messageID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateMessageID, messageID)
	}
	client.waiters[messageID] = waiter
	return nil
}

func (client *Client) unregisterAckWaiter(messageID string, waiter *ackWaiter) {
	client.waitersMu.Lock()
	if client.waiters[messageID] == waiter {
		delete(client.waiters, messageID)
	}
	client.waitersMu.Unlock()
}

func (client *Client) dispatchAck(runtime *connectionRuntime, packet *protocol.Packet) {
	if packet.MessageID == "" {
		return
	}
	client.waitersMu.Lock()
	waiter := client.waiters[packet.MessageID]
	if waiter == nil || waiter.runtimeID != runtime.id {
		client.waitersMu.Unlock()
		return
	}
	delete(client.waiters, packet.MessageID)
	client.waitersMu.Unlock()

	ack, err := protocol.DecodeAck(packet)
	if err != nil {
		waiter.result <- ackResult{err: fmt.Errorf("%w: %v", ErrUnexpectedAck, err)}
		return
	}
	if ack.MessageID != packet.MessageID || ack.MsgID != waiter.msgID {
		waiter.result <- ackResult{err: fmt.Errorf(
			"%w: message_id=%q msg_id=%d",
			ErrUnexpectedAck,
			ack.MessageID,
			ack.MsgID,
		)}
		return
	}
	if ack.Code != protocol.AckAccepted {
		waiter.result <- ackResult{err: newAckError(ack)}
		return
	}
	ackCopy := ack
	waiter.result <- ackResult{ack: &ackCopy}
}

func newAckError(ack protocol.Ack) error {
	kind := ErrAckRejected
	switch ack.Code {
	case protocol.AckUnauthorized:
		kind = ErrAuthenticationFailed
	case protocol.AckAuthUnavailable:
		kind = ErrAuthenticationUnavailable
	}
	return &AckError{Ack: ack, kind: kind}
}

func (client *Client) waitForAck(waitCtx, parentCtx context.Context, runtime *connectionRuntime, waiter *ackWaiter) (*protocol.Ack, error) {
	select {
	case result := <-waiter.result:
		return result.ack, result.err
	case <-runtime.done:
		select {
		case result := <-waiter.result:
			return result.ack, result.err
		default:
			return nil, runtime.failure()
		}
	case <-waitCtx.Done():
		select {
		case result := <-waiter.result:
			return result.ack, result.err
		default:
		}
		if parentCtx.Err() != nil {
			return nil, parentCtx.Err()
		}
		return nil, fmt.Errorf("%w: %w", ErrAckTimeout, waitCtx.Err())
	}
}

func (client *Client) failAckWaiters(runtimeID uint64, failure error) {
	client.waitersMu.Lock()
	waiters := make([]*ackWaiter, 0)
	for messageID, waiter := range client.waiters {
		if waiter.runtimeID == runtimeID {
			delete(client.waiters, messageID)
			waiters = append(waiters, waiter)
		}
	}
	client.waitersMu.Unlock()

	for _, waiter := range waiters {
		waiter.result <- ackResult{err: failure}
	}
}
