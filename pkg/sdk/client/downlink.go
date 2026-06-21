package client

import (
	"context"
	"fmt"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

// DownlinkHandler processes one non-ACK packet from the gateway. Returning an
// error prevents automatic delivery acknowledgement.
type DownlinkHandler func(context.Context, *protocol.Packet) error

// DownlinkError reports a handler or automatic acknowledgement failure.
type DownlinkError struct {
	Operation string
	MsgID     uint32
	MessageID string
	Err       error
}

func (err *DownlinkError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf(
		"client: downlink %s failed: msg_id=%d message_id=%q: %v",
		err.Operation,
		err.MsgID,
		err.MessageID,
		err.Err,
	)
}

func (err *DownlinkError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

type downlinkTarget struct {
	msgID       uint32
	messageID   string
	traceID     string
	ackRequired bool
}

func newDownlinkTarget(packet *protocol.Packet) (downlinkTarget, error) {
	if packet == nil || protocol.IsReservedMsgID(packet.MsgID) {
		return downlinkTarget{}, ErrInvalidDownlink
	}
	target := downlinkTarget{
		msgID:       packet.MsgID,
		messageID:   packet.MessageID,
		traceID:     packet.TraceID,
		ackRequired: packet.Flags&protocol.FlagAckRequired != 0,
	}
	if !target.ackRequired || target.messageID == "" {
		return downlinkTarget{}, ErrInvalidDownlink
	}
	return target, nil
}

func (client *Client) downlinkLoop(runtime *connectionRuntime) {
	for {
		select {
		case <-runtime.done:
			return
		default:
		}

		select {
		case <-runtime.done:
			return
		case packet := <-runtime.inbound:
			client.handleDownlink(runtime, packet)
		}
	}
}

func (client *Client) handleDownlink(runtime *connectionRuntime, packet *protocol.Packet) {
	target, targetErr := newDownlinkTarget(packet)
	if targetErr == nil && client.deduper.contains(target.messageID) {
		if _, err := client.acknowledgeDownlink(runtime.context, runtime, target); err != nil {
			client.reportDownlinkError("ack_duplicate", target.msgID, target.messageID, err)
		}
		return
	}

	if err := callDownlinkHandler(runtime.context, client.config.downlinkHandler, packet); err != nil {
		client.reportDownlinkError("handle", packet.MsgID, packet.MessageID, err)
		return
	}
	if targetErr != nil || client.config.manualDownlinkAck {
		return
	}

	client.deduper.mark(target.messageID)
	if _, err := client.acknowledgeDownlink(runtime.context, runtime, target); err != nil {
		client.reportDownlinkError("ack", target.msgID, target.messageID, err)
	}
}

func callDownlinkHandler(ctx context.Context, handler DownlinkHandler, packet *protocol.Packet) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: panic: %v", ErrDownlinkHandler, recovered)
		}
	}()
	if err := handler(ctx, packet); err != nil {
		return fmt.Errorf("%w: %w", ErrDownlinkHandler, err)
	}
	return nil
}

// AcknowledgeDownlink confirms that an ACK-required downlink packet was
// durably processed. It waits for the gateway to accept the MsgID=2 packet.
func (client *Client) AcknowledgeDownlink(ctx context.Context, packet *protocol.Packet) (*protocol.Ack, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidConfig)
	}
	target, err := newDownlinkTarget(packet)
	if err != nil {
		return nil, err
	}
	runtime, _, err := client.sendSnapshot()
	if err != nil {
		return nil, err
	}
	client.deduper.mark(target.messageID)
	return client.acknowledgeDownlink(ctx, runtime, target)
}

func (client *Client) acknowledgeDownlink(ctx context.Context, expectedRuntime *connectionRuntime, target downlinkTarget) (*protocol.Ack, error) {
	runtime, binding, err := client.sendSnapshot()
	if err != nil {
		return nil, err
	}
	if runtime != expectedRuntime {
		return nil, expectedRuntime.failure()
	}
	body, err := protocol.EncodeDeliveryAck(protocol.DeliveryAck{
		MessageID: target.messageID,
		Code:      protocol.DeliveryAckDelivered,
	})
	if err != nil {
		return nil, err
	}
	requestMessageID, err := newMessageID("zc-dack-")
	if err != nil {
		return nil, err
	}
	traceID := target.traceID
	if traceID == "" {
		traceID = target.messageID
	}

	packet := protocol.NewPacket(protocol.MsgIDDownlinkAck, body)
	packet.Flags = protocol.FlagAckRequired
	packet.Seq = client.sequence.Add(1)
	packet.Timestamp = time.Now().UnixMilli()
	packet.ClientID = binding.ClientID
	packet.DeviceID = binding.DeviceID
	packet.SessionID = binding.SessionID
	packet.MessageID = requestMessageID
	packet.TraceID = traceID
	packet.Token = runtime.token
	return client.writeAndWaitAck(ctx, runtime, packet)
}

func (client *Client) reportDownlinkError(operation string, msgID uint32, messageID string, err error) {
	if err == nil || client.config.onDownlinkError == nil {
		return
	}
	downlinkError := &DownlinkError{
		Operation: operation,
		MsgID:     msgID,
		MessageID: messageID,
		Err:       err,
	}
	defer func() {
		_ = recover()
	}()
	client.config.onDownlinkError(downlinkError)
}
