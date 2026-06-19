package protocol_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	protocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

const goldenPacketHex = "5a43010001000003e9000000000000002a00000199c82cc00000080008000900090007000700000005636c69656e742d316465766963652d3173657373696f6e2d316d6573736167652d3174726163652d31746f6b656e2d3168656c6c6f"

func TestWireFormatGolden(t *testing.T) {
	packet := protocol.NewPacket(1001, []byte("hello"))
	packet.Flags = protocol.FlagAckRequired
	packet.ClientID = "client-1"
	packet.DeviceID = "device-1"
	packet.SessionID = "session-1"
	packet.MessageID = "message-1"
	packet.TraceID = "trace-1"
	packet.Token = "token-1"
	packet.Seq = 42
	packet.Timestamp = 1760000000000

	encoded, err := protocol.Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got := hex.EncodeToString(encoded); got != goldenPacketHex {
		t.Fatalf("wire packet = %s, want %s", got, goldenPacketHex)
	}

	decoded, err := protocol.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.MsgID != packet.MsgID || decoded.ClientID != packet.ClientID ||
		decoded.DeviceID != packet.DeviceID || decoded.SessionID != packet.SessionID ||
		decoded.MessageID != packet.MessageID || decoded.TraceID != packet.TraceID ||
		decoded.Token != packet.Token || decoded.Seq != packet.Seq ||
		decoded.Timestamp != packet.Timestamp || !bytes.Equal(decoded.Body, packet.Body) {
		t.Fatalf("decoded packet = %+v, want %+v", decoded, packet)
	}
}

func TestNewPacketClonesBody(t *testing.T) {
	body := []byte("hello")
	packet := protocol.NewPacket(1001, body)
	body[0] = 'j'
	if got := string(packet.Body); got != "hello" {
		t.Fatalf("packet body = %q, want hello", got)
	}
}

func TestPublicCodecErrorsSupportErrorsIs(t *testing.T) {
	if _, err := protocol.Encode(nil); !errors.Is(err, protocol.ErrNilPacket) {
		t.Fatalf("Encode(nil) error = %v, want ErrNilPacket", err)
	}

	packet := protocol.NewPacket(1001, []byte("body"))
	encoded, err := protocol.Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "too short", data: encoded[:protocol.FixedHeaderSize-1], want: protocol.ErrPacketTooShort},
		{name: "length mismatch", data: encoded[:len(encoded)-1], want: protocol.ErrLengthMismatch},
		{name: "invalid magic", data: append([]byte{0}, encoded[1:]...), want: protocol.ErrInvalidMagic},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := protocol.Decode(test.data)
			if !errors.Is(err, test.want) {
				t.Fatalf("Decode() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestReservedMsgIDs(t *testing.T) {
	for _, msgID := range []uint32{protocol.MsgIDAck, protocol.MsgIDDownlinkAck, protocol.MsgIDBind} {
		if !protocol.IsReservedMsgID(msgID) {
			t.Fatalf("IsReservedMsgID(%d) = false, want true", msgID)
		}
	}
	if protocol.IsReservedMsgID(1001) {
		t.Fatal("IsReservedMsgID(1001) = true, want false")
	}
}

func TestAckHelpers(t *testing.T) {
	origin := protocol.NewPacket(protocol.MsgIDBind, nil)
	origin.ClientID = "client-1"
	origin.DeviceID = "device-1"
	origin.MessageID = "message-1"
	origin.TraceID = "trace-1"

	packet, err := protocol.NewAckPacket(origin, protocol.AckAccepted, "")
	if err != nil {
		t.Fatalf("NewAckPacket() error = %v", err)
	}
	ack, err := protocol.DecodeAck(packet)
	if err != nil {
		t.Fatalf("DecodeAck() error = %v", err)
	}
	if ack.Code != protocol.AckAccepted || ack.MsgID != protocol.MsgIDBind || ack.MessageID != origin.MessageID {
		t.Fatalf("ack = %+v", ack)
	}
	if packet.ClientID != origin.ClientID || packet.DeviceID != origin.DeviceID || packet.TraceID != origin.TraceID {
		t.Fatalf("ack packet = %+v", packet)
	}

	if _, err := protocol.DecodeAck(origin); !errors.Is(err, protocol.ErrInvalidAck) {
		t.Fatalf("DecodeAck(non-ack) error = %v, want ErrInvalidAck", err)
	}
}

func Example() {
	packet := protocol.NewPacket(1001, []byte("hello"))
	packet.ClientID = "client-1"
	packet.DeviceID = "device-1"

	encoded, _ := protocol.Encode(packet)
	decoded, _ := protocol.Decode(encoded)
	fmt.Printf("msg_id=%d body=%s", decoded.MsgID, decoded.Body)
	// Output: msg_id=1001 body=hello
}
