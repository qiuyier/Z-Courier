package protocol

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	packet := NewPacket(1001, []byte(`{"hello":"world"}`))
	packet.Flags = FlagAckRequired
	packet.ClientID = "client-1"
	packet.DeviceID = "device-1"
	packet.SessionID = "session-1"
	packet.MessageID = "message-1"
	packet.TraceID = "trace-1"
	packet.Token = "token-1"
	packet.Seq = 42
	packet.Timestamp = 1760000000000

	encoded, err := Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if decoded.Version != Version {
		t.Fatalf("Version = %d, want %d", decoded.Version, Version)
	}
	if decoded.Flags != FlagAckRequired {
		t.Fatalf("Flags = %d, want %d", decoded.Flags, FlagAckRequired)
	}
	if decoded.MsgID != packet.MsgID {
		t.Fatalf("MsgID = %d, want %d", decoded.MsgID, packet.MsgID)
	}
	if decoded.ClientID != packet.ClientID {
		t.Fatalf("ClientID = %q, want %q", decoded.ClientID, packet.ClientID)
	}
	if decoded.DeviceID != packet.DeviceID {
		t.Fatalf("DeviceID = %q, want %q", decoded.DeviceID, packet.DeviceID)
	}
	if decoded.SessionID != packet.SessionID {
		t.Fatalf("SessionID = %q, want %q", decoded.SessionID, packet.SessionID)
	}
	if decoded.MessageID != packet.MessageID {
		t.Fatalf("MessageID = %q, want %q", decoded.MessageID, packet.MessageID)
	}
	if decoded.TraceID != packet.TraceID {
		t.Fatalf("TraceID = %q, want %q", decoded.TraceID, packet.TraceID)
	}
	if decoded.Token != packet.Token {
		t.Fatalf("Token = %q, want %q", decoded.Token, packet.Token)
	}
	if decoded.Seq != packet.Seq {
		t.Fatalf("Seq = %d, want %d", decoded.Seq, packet.Seq)
	}
	if decoded.Timestamp != packet.Timestamp {
		t.Fatalf("Timestamp = %d, want %d", decoded.Timestamp, packet.Timestamp)
	}
	if !bytes.Equal(decoded.Body, packet.Body) {
		t.Fatalf("Body = %q, want %q", decoded.Body, packet.Body)
	}
}

func TestEncodeDecodeEmptyBody(t *testing.T) {
	packet := NewPacket(1002, nil)

	encoded, err := Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(decoded.Body) != 0 {
		t.Fatalf("Body length = %d, want 0", len(decoded.Body))
	}
}

func TestDecodeBodyTooLarge(t *testing.T) {
	packet := NewPacket(1003, []byte("too large for test limit"))

	encoded, err := Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	_, err = DecodeWithMaxBodySize(encoded, 4)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("DecodeWithMaxBodySize() error = %v, want %v", err, ErrBodyTooLarge)
	}
}

func TestDecodeUnsupportedVersion(t *testing.T) {
	packet := NewPacket(1004, []byte("body"))

	encoded, err := Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	encoded[2] = Version + 1

	_, err = Decode(encoded)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Decode() error = %v, want %v", err, ErrUnsupportedVersion)
	}
}

func TestDecodeLengthMismatch(t *testing.T) {
	packet := NewPacket(1005, []byte("body"))

	encoded, err := Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	_, err = Decode(encoded[:len(encoded)-1])
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("Decode() error = %v, want %v", err, ErrLengthMismatch)
	}
}

func TestDecodeInvalidMagic(t *testing.T) {
	packet := NewPacket(1006, []byte("body"))

	encoded, err := Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	encoded[0] = 0

	_, err = Decode(encoded)
	if !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("Decode() error = %v, want %v", err, ErrInvalidMagic)
	}
}

func TestEncodeFieldTooLarge(t *testing.T) {
	packet := NewPacket(1007, nil)
	packet.ClientID = strings.Repeat("a", 1<<16)

	_, err := Encode(packet)
	if !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("Encode() error = %v, want %v", err, ErrFieldTooLarge)
	}
}
