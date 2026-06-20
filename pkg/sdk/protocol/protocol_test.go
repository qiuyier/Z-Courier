package protocol_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	protocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

var (
	errFrameTooShort           = errors.New("frame too short")
	errFrameLengthMismatch     = errors.New("frame length mismatch")
	errOuterInnerMsgIDMismatch = errors.New("outer and inner message IDs differ")
)

type validFixtureFile struct {
	SchemaVersion    int                     `json:"schema_version"`
	WireVersion      int                     `json:"wire_version"`
	Vectors          []validFixture          `json:"vectors"`
	GeneratedVectors []generatedValidFixture `json:"generated_vectors"`
}

type invalidFixtureFile struct {
	SchemaVersion    int                       `json:"schema_version"`
	Vectors          []invalidFixture          `json:"vectors"`
	GeneratedVectors []generatedInvalidFixture `json:"generated_vectors"`
}

type validFixture struct {
	Name     string        `json:"name"`
	Packet   packetFixture `json:"packet"`
	InnerHex string        `json:"inner_hex"`
	FrameHex string        `json:"frame_hex"`
}

type generatedValidFixture struct {
	Name        string           `json:"name"`
	Packet      packetFixture    `json:"packet"`
	Expansion   fixtureExpansion `json:"expansion"`
	InnerLength int              `json:"inner_length"`
	FrameLength int              `json:"frame_length"`
	InnerSHA256 string           `json:"inner_sha256"`
	FrameSHA256 string           `json:"frame_sha256"`
}

type invalidFixture struct {
	Name          string          `json:"name"`
	Scope         string          `json:"scope"`
	Source        string          `json:"source"`
	Mutation      fixtureMutation `json:"mutation"`
	MaxBodySize   uint32          `json:"max_body_size"`
	ExpectedError string          `json:"expected_error"`
}

type generatedInvalidFixture struct {
	Name          string           `json:"name"`
	Packet        packetFixture    `json:"packet"`
	Expansion     fixtureExpansion `json:"expansion"`
	ExpectedError string           `json:"expected_error"`
}

type packetFixture struct {
	Version   uint8  `json:"version"`
	Flags     uint16 `json:"flags"`
	MsgID     uint32 `json:"msg_id"`
	Seq       string `json:"seq"`
	Timestamp string `json:"timestamp"`
	ClientID  string `json:"client_id"`
	DeviceID  string `json:"device_id"`
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	TraceID   string `json:"trace_id"`
	Token     string `json:"token"`
	BodyHex   string `json:"body_hex"`
}

type fixtureExpansion struct {
	Field   string `json:"field"`
	ByteHex string `json:"byte_hex"`
	Count   int    `json:"count"`
}

type fixtureMutation struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Count  int    `json:"count"`
	Hex    string `json:"hex"`
}

func TestWireFormatFixtures(t *testing.T) {
	fixtures := loadFixture[validFixtureFile](t, "valid.json")
	if fixtures.SchemaVersion != 1 || fixtures.WireVersion != int(protocol.Version) {
		t.Fatalf("fixture versions = schema %d wire %d", fixtures.SchemaVersion, fixtures.WireVersion)
	}

	for _, fixture := range fixtures.Vectors {
		t.Run(fixture.Name, func(t *testing.T) {
			packet := packetFromFixture(t, fixture.Packet)
			inner := mustEncode(t, packet)
			assertHex(t, "inner packet", inner, fixture.InnerHex)

			frame := encodeOuterFrame(packet.MsgID, inner)
			assertHex(t, "outer frame", frame, fixture.FrameHex)

			decoded, err := decodeOuterFrame(frame, protocol.DefaultMaxBodySize)
			if err != nil {
				t.Fatalf("decodeOuterFrame() error = %v", err)
			}
			assertPacketEqual(t, decoded, packet)
		})
	}

	for _, fixture := range fixtures.GeneratedVectors {
		t.Run(fixture.Name, func(t *testing.T) {
			packet := packetFromFixture(t, fixture.Packet)
			applyExpansion(t, packet, fixture.Expansion)
			inner := mustEncode(t, packet)
			frame := encodeOuterFrame(packet.MsgID, inner)

			if len(inner) != fixture.InnerLength || len(frame) != fixture.FrameLength {
				t.Fatalf("lengths = inner %d frame %d, want %d and %d", len(inner), len(frame), fixture.InnerLength, fixture.FrameLength)
			}
			assertSHA256(t, "inner packet", inner, fixture.InnerSHA256)
			assertSHA256(t, "outer frame", frame, fixture.FrameSHA256)
		})
	}
}

func TestInvalidWireFormatFixtures(t *testing.T) {
	validFixtures := loadFixture[validFixtureFile](t, "valid.json")
	invalidFixtures := loadFixture[invalidFixtureFile](t, "invalid.json")
	if invalidFixtures.SchemaVersion != 1 {
		t.Fatalf("fixture schema version = %d, want 1", invalidFixtures.SchemaVersion)
	}

	sources := make(map[string]validFixture, len(validFixtures.Vectors))
	for _, fixture := range validFixtures.Vectors {
		sources[fixture.Name] = fixture
	}

	for _, fixture := range invalidFixtures.Vectors {
		t.Run(fixture.Name, func(t *testing.T) {
			source, ok := sources[fixture.Source]
			if !ok {
				t.Fatalf("unknown source fixture %q", fixture.Source)
			}

			hexData := source.InnerHex
			if fixture.Scope == "frame" {
				hexData = source.FrameHex
			}
			data := mustDecodeHex(t, hexData)
			data = applyMutation(t, data, fixture.Mutation)

			maxBodySize := fixture.MaxBodySize
			if maxBodySize == 0 {
				maxBodySize = protocol.DefaultMaxBodySize
			}

			var err error
			switch fixture.Scope {
			case "inner":
				_, err = protocol.DecodeWithMaxBodySize(data, maxBodySize)
			case "frame":
				_, err = decodeOuterFrame(data, maxBodySize)
			default:
				t.Fatalf("unknown fixture scope %q", fixture.Scope)
			}

			if !errors.Is(err, fixtureError(t, fixture.ExpectedError)) {
				t.Fatalf("decode error = %v, want %s", err, fixture.ExpectedError)
			}
		})
	}

	for _, fixture := range invalidFixtures.GeneratedVectors {
		t.Run(fixture.Name, func(t *testing.T) {
			packet := packetFromFixture(t, fixture.Packet)
			applyExpansion(t, packet, fixture.Expansion)
			_, err := protocol.Encode(packet)
			if !errors.Is(err, fixtureError(t, fixture.ExpectedError)) {
				t.Fatalf("Encode() error = %v, want %s", err, fixture.ExpectedError)
			}
		})
	}
}

func loadFixture[T any](t *testing.T, name string) T {
	t.Helper()

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve protocol fixture path")
	}
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "testdata", "protocol", "v1", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}

	var fixture T
	if err := sonic.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	return fixture
}

func packetFromFixture(t *testing.T, fixture packetFixture) *protocol.Packet {
	t.Helper()

	if fixture.Version != protocol.Version {
		t.Fatalf("fixture packet version = %d, want %d", fixture.Version, protocol.Version)
	}
	seq, err := strconv.ParseUint(fixture.Seq, 10, 64)
	if err != nil {
		t.Fatalf("parse seq %q: %v", fixture.Seq, err)
	}
	timestamp, err := strconv.ParseInt(fixture.Timestamp, 10, 64)
	if err != nil {
		t.Fatalf("parse timestamp %q: %v", fixture.Timestamp, err)
	}

	packet := protocol.NewPacket(fixture.MsgID, mustDecodeHex(t, fixture.BodyHex))
	packet.Flags = protocol.Flags(fixture.Flags)
	packet.Seq = seq
	packet.Timestamp = timestamp
	packet.ClientID = fixture.ClientID
	packet.DeviceID = fixture.DeviceID
	packet.SessionID = fixture.SessionID
	packet.MessageID = fixture.MessageID
	packet.TraceID = fixture.TraceID
	packet.Token = fixture.Token
	return packet
}

func mustEncode(t *testing.T, packet *protocol.Packet) []byte {
	t.Helper()
	data, err := protocol.Encode(packet)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	return data
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex %q: %v", value, err)
	}
	return data
}

func assertHex(t *testing.T, label string, actual []byte, expectedHex string) {
	t.Helper()
	if actualHex := hex.EncodeToString(actual); actualHex != expectedHex {
		t.Fatalf("%s = %s, want %s", label, actualHex, expectedHex)
	}
}

func assertSHA256(t *testing.T, label string, data []byte, expected string) {
	t.Helper()
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != expected {
		t.Fatalf("%s SHA-256 = %s, want %s", label, actual, expected)
	}
}

func assertPacketEqual(t *testing.T, actual, expected *protocol.Packet) {
	t.Helper()
	if actual.Header != expected.Header || actual.ClientID != expected.ClientID ||
		actual.DeviceID != expected.DeviceID || actual.SessionID != expected.SessionID ||
		actual.MessageID != expected.MessageID || actual.TraceID != expected.TraceID ||
		actual.Token != expected.Token || !bytes.Equal(actual.Body, expected.Body) {
		t.Fatalf("decoded packet = %+v, want %+v", actual, expected)
	}
}

func encodeOuterFrame(msgID uint32, inner []byte) []byte {
	frame := make([]byte, 8+len(inner))
	binary.BigEndian.PutUint32(frame[0:4], msgID)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(inner)))
	copy(frame[8:], inner)
	return frame
}

func decodeOuterFrame(frame []byte, maxBodySize uint32) (*protocol.Packet, error) {
	if len(frame) < 8 {
		return nil, errFrameTooShort
	}
	outerMsgID := binary.BigEndian.Uint32(frame[0:4])
	payloadLength := binary.BigEndian.Uint32(frame[4:8])
	if uint64(payloadLength)+8 != uint64(len(frame)) {
		return nil, errFrameLengthMismatch
	}

	packet, err := protocol.DecodeWithMaxBodySize(frame[8:], maxBodySize)
	if err != nil {
		return nil, err
	}
	if outerMsgID != packet.MsgID {
		return nil, errOuterInnerMsgIDMismatch
	}
	return packet, nil
}

func applyExpansion(t *testing.T, packet *protocol.Packet, expansion fixtureExpansion) {
	t.Helper()
	unit := mustDecodeHex(t, expansion.ByteHex)
	if len(unit) != 1 || expansion.Count < 0 {
		t.Fatalf("invalid expansion byte=%q count=%d", expansion.ByteHex, expansion.Count)
	}
	value := strings.Repeat(string(unit), expansion.Count)

	switch expansion.Field {
	case "client_id":
		packet.ClientID = value
	case "device_id":
		packet.DeviceID = value
	case "session_id":
		packet.SessionID = value
	case "message_id":
		packet.MessageID = value
	case "trace_id":
		packet.TraceID = value
	case "token":
		packet.Token = value
	default:
		t.Fatalf("unsupported expansion field %q", expansion.Field)
	}
}

func applyMutation(t *testing.T, source []byte, mutation fixtureMutation) []byte {
	t.Helper()
	data := bytes.Clone(source)

	switch mutation.Type {
	case "":
		return data
	case "truncate_to":
		if mutation.Count < 0 || mutation.Count > len(data) {
			t.Fatalf("invalid truncate_to count %d for %d bytes", mutation.Count, len(data))
		}
		return data[:mutation.Count]
	case "truncate_tail":
		if mutation.Count < 0 || mutation.Count > len(data) {
			t.Fatalf("invalid truncate_tail count %d for %d bytes", mutation.Count, len(data))
		}
		return data[:len(data)-mutation.Count]
	case "append_hex":
		return append(data, mustDecodeHex(t, mutation.Hex)...)
	case "replace_hex":
		replacement := mustDecodeHex(t, mutation.Hex)
		end := mutation.Offset + len(replacement)
		if mutation.Offset < 0 || end > len(data) {
			t.Fatalf("invalid replacement range [%d:%d] for %d bytes", mutation.Offset, end, len(data))
		}
		copy(data[mutation.Offset:end], replacement)
		return data
	default:
		t.Fatalf("unsupported mutation type %q", mutation.Type)
		return nil
	}
}

func fixtureError(t *testing.T, name string) error {
	t.Helper()
	switch name {
	case "packet_too_short":
		return protocol.ErrPacketTooShort
	case "invalid_magic":
		return protocol.ErrInvalidMagic
	case "unsupported_version":
		return protocol.ErrUnsupportedVersion
	case "length_mismatch":
		return protocol.ErrLengthMismatch
	case "body_too_large":
		return protocol.ErrBodyTooLarge
	case "field_too_large":
		return protocol.ErrFieldTooLarge
	case "frame_too_short":
		return errFrameTooShort
	case "frame_length_mismatch":
		return errFrameLengthMismatch
	case "outer_inner_msg_id_mismatch":
		return errOuterInnerMsgIDMismatch
	default:
		t.Fatalf("unknown fixture error %q", name)
		return nil
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
