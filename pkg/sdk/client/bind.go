package client

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

// BindError contains a gateway AUTH/BIND rejection.
type BindError struct {
	Code   protocol.AckCode
	Reason string
	kind   error
}

func (err *BindError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Reason == "" {
		return fmt.Sprintf("client: bind failed with code %q", err.Code)
	}
	return fmt.Sprintf("client: bind failed with code %q: %s", err.Code, err.Reason)
}

func (err *BindError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.kind
}

func (client *Client) newBindPacket(token string) (*protocol.Packet, error) {
	messageID, err := newBindMessageID()
	if err != nil {
		return nil, err
	}

	packet := protocol.NewPacket(protocol.MsgIDBind, nil)
	packet.Flags = protocol.FlagAckRequired
	packet.Seq = client.sequence.Add(1)
	packet.Timestamp = time.Now().UnixMilli()
	packet.ClientID = client.config.clientID
	packet.DeviceID = client.config.deviceID
	packet.MessageID = messageID
	packet.TraceID = messageID
	packet.Token = token
	return packet, nil
}

func (client *Client) waitForBindAck(connection net.Conn, bindMessageID string) (Binding, error) {
	for {
		packet, err := readPacketFrame(connection, client.config.maxFramePayloadSize, client.config.maxBodySize)
		if err != nil {
			return Binding{}, err
		}
		if packet.MsgID != protocol.MsgIDAck || packet.MessageID != bindMessageID {
			if err := client.bufferBeforeReady(packet); err != nil {
				return Binding{}, err
			}
			continue
		}

		ack, err := protocol.DecodeAck(packet)
		if err != nil {
			return Binding{}, fmt.Errorf("%w: %v", ErrUnexpectedBindAck, err)
		}
		if ack.MessageID != bindMessageID || ack.MsgID != protocol.MsgIDBind {
			return Binding{}, fmt.Errorf(
				"%w: message_id=%q msg_id=%d",
				ErrUnexpectedBindAck,
				ack.MessageID,
				ack.MsgID,
			)
		}

		switch ack.Code {
		case protocol.AckAccepted:
			if packet.SessionID == "" {
				return Binding{}, fmt.Errorf("%w: accepted ACK has no session ID", ErrUnexpectedBindAck)
			}
			return Binding{
				ClientID:  packet.ClientID,
				DeviceID:  packet.DeviceID,
				SessionID: packet.SessionID,
			}, nil
		case protocol.AckUnauthorized:
			return Binding{}, &BindError{Code: ack.Code, Reason: ack.Reason, kind: ErrAuthenticationFailed}
		case protocol.AckAuthUnavailable:
			return Binding{}, &BindError{Code: ack.Code, Reason: ack.Reason, kind: ErrAuthenticationUnavailable}
		case protocol.AckDecodeFailed, protocol.AckRejected:
			return Binding{}, &BindError{Code: ack.Code, Reason: ack.Reason, kind: ErrBindRejected}
		default:
			return Binding{}, fmt.Errorf("%w: unknown code %q", ErrUnexpectedBindAck, ack.Code)
		}
	}
}

func newBindMessageID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("client: generate bind message ID: %w", err)
	}
	return "zc-bind-" + hex.EncodeToString(random[:]), nil
}
