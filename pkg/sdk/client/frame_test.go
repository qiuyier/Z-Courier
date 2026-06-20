package client

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

type validFrameFixtures struct {
	Vectors []validFrameFixture `json:"vectors"`
}

type validFrameFixture struct {
	Name   string `json:"name"`
	Packet struct {
		MsgID uint32 `json:"msg_id"`
	} `json:"packet"`
	InnerHex string `json:"inner_hex"`
	FrameHex string `json:"frame_hex"`
}

type invalidFrameFixtures struct {
	Vectors []invalidFrameFixture `json:"vectors"`
}

type invalidFrameFixture struct {
	Name          string        `json:"name"`
	Scope         string        `json:"scope"`
	Source        string        `json:"source"`
	Mutation      frameMutation `json:"mutation"`
	ExpectedError string        `json:"expected_error"`
}

type frameMutation struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Count  int    `json:"count"`
	Hex    string `json:"hex"`
}

func TestFrameSharedValidFixtures(t *testing.T) {
	fixtures := loadFrameFixture[validFrameFixtures](t, "valid.json")
	for _, fixture := range fixtures.Vectors {
		t.Run(fixture.Name, func(t *testing.T) {
			inner := decodeFixtureHex(t, fixture.InnerHex)
			encoded, err := encodeFrame(fixture.Packet.MsgID, inner)
			if err != nil {
				t.Fatalf("encodeFrame() error = %v", err)
			}
			if actual := hex.EncodeToString(encoded); actual != fixture.FrameHex {
				t.Fatalf("frame = %s, want %s", actual, fixture.FrameHex)
			}

			frame, err := decodeExactFrame(encoded, 0)
			if err != nil {
				t.Fatalf("decodeExactFrame() error = %v", err)
			}
			packet, err := decodePacketFrame(frame, 0)
			if err != nil {
				t.Fatalf("decodePacketFrame() error = %v", err)
			}
			if packet.MsgID != fixture.Packet.MsgID || !bytes.Equal(frame.payload, inner) {
				t.Fatalf("decoded frame msg_id=%d payload_size=%d", packet.MsgID, len(frame.payload))
			}
		})
	}
}

func TestFrameSharedInvalidFixtures(t *testing.T) {
	valid := loadFrameFixture[validFrameFixtures](t, "valid.json")
	invalid := loadFrameFixture[invalidFrameFixtures](t, "invalid.json")
	sources := make(map[string]validFrameFixture, len(valid.Vectors))
	for _, fixture := range valid.Vectors {
		sources[fixture.Name] = fixture
	}

	for _, fixture := range invalid.Vectors {
		if fixture.Scope != "frame" {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			source, ok := sources[fixture.Source]
			if !ok {
				t.Fatalf("unknown source fixture %q", fixture.Source)
			}
			data := mutateFrameFixture(t, decodeFixtureHex(t, source.FrameHex), fixture.Mutation)
			frame, err := decodeExactFrame(data, 0)
			if err == nil {
				_, err = decodePacketFrame(frame, 0)
			}
			if !errors.Is(err, expectedFrameError(t, fixture.ExpectedError)) {
				t.Fatalf("decode error = %v, want %s", err, fixture.ExpectedError)
			}
		})
	}
}

func TestReadFrameHandlesFragmentedAndCoalescedReads(t *testing.T) {
	fixtures := loadFrameFixture[validFrameFixtures](t, "valid.json")
	first := decodeFixtureHex(t, fixtures.Vectors[0].FrameHex)
	second := decodeFixtureHex(t, fixtures.Vectors[1].FrameHex)
	stream := &chunkReader{reader: bytes.NewReader(append(first, second...)), maxChunk: 3}

	firstFrame, err := readFrame(stream, 0)
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	secondFrame, err := readFrame(stream, 0)
	if err != nil {
		t.Fatalf("read second frame: %v", err)
	}
	if firstFrame.msgID != fixtures.Vectors[0].Packet.MsgID || secondFrame.msgID != fixtures.Vectors[1].Packet.MsgID {
		t.Fatalf("frame msg IDs = %d, %d", firstFrame.msgID, secondFrame.msgID)
	}
	if _, err := readFrame(stream, 0); !errors.Is(err, io.EOF) {
		t.Fatalf("final read error = %v, want EOF", err)
	}
}

func TestReadFrameRejectsConfiguredPayloadLimit(t *testing.T) {
	fixtures := loadFrameFixture[validFrameFixtures](t, "valid.json")
	data := decodeFixtureHex(t, fixtures.Vectors[0].FrameHex)
	inner := decodeFixtureHex(t, fixtures.Vectors[0].InnerHex)
	if _, err := readFrame(bytes.NewReader(data), uint32(len(inner)-1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("readFrame() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestWritePacketFrameHandlesShortWrites(t *testing.T) {
	fixtures := loadFrameFixture[validFrameFixtures](t, "valid.json")
	expected := decodeFixtureHex(t, fixtures.Vectors[0].FrameHex)
	inner := decodeFixtureHex(t, fixtures.Vectors[0].InnerHex)
	packet, err := protocol.Decode(inner)
	if err != nil {
		t.Fatalf("protocol.Decode() error = %v", err)
	}

	writer := &chunkWriter{maxChunk: 5}
	if err := writePacketFrame(writer, packet, 0); err != nil {
		t.Fatalf("writePacketFrame() error = %v", err)
	}
	if !bytes.Equal(writer.Bytes(), expected) {
		t.Fatalf("written frame size = %d, want %d", writer.Len(), len(expected))
	}
}

func TestEncodePacketFrameRejectsNilPacket(t *testing.T) {
	if _, err := encodePacketFrame(nil, 0); !errors.Is(err, protocol.ErrNilPacket) {
		t.Fatalf("encodePacketFrame(nil) error = %v, want ErrNilPacket", err)
	}
}

func TestEncodePacketFrameRejectsConfiguredPayloadLimit(t *testing.T) {
	packet := protocol.NewPacket(2001, []byte("body"))
	if _, err := encodePacketFrame(packet, protocol.FixedHeaderSize); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("encodePacketFrame() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestWriteAllRejectsNoProgress(t *testing.T) {
	if err := writeAll(noProgressWriter{}, []byte("frame")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeAll() error = %v, want io.ErrShortWrite", err)
	}
}

type chunkReader struct {
	reader   io.Reader
	maxChunk int
}

func (r *chunkReader) Read(data []byte) (int, error) {
	if len(data) > r.maxChunk {
		data = data[:r.maxChunk]
	}
	return r.reader.Read(data)
}

type chunkWriter struct {
	bytes.Buffer
	maxChunk int
}

type noProgressWriter struct{}

func (noProgressWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (w *chunkWriter) Write(data []byte) (int, error) {
	if len(data) > w.maxChunk {
		data = data[:w.maxChunk]
	}
	return w.Buffer.Write(data)
}

func loadFrameFixture[T any](t *testing.T, name string) T {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve frame fixture path")
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

func decodeFixtureHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode fixture hex: %v", err)
	}
	return data
}

func mutateFrameFixture(t *testing.T, source []byte, mutation frameMutation) []byte {
	t.Helper()
	data := bytes.Clone(source)
	switch mutation.Type {
	case "truncate_to":
		if mutation.Count < 0 || mutation.Count > len(data) {
			t.Fatalf("invalid truncate_to count %d", mutation.Count)
		}
		return data[:mutation.Count]
	case "truncate_tail":
		if mutation.Count < 0 || mutation.Count > len(data) {
			t.Fatalf("invalid truncate_tail count %d", mutation.Count)
		}
		return data[:len(data)-mutation.Count]
	case "replace_hex":
		replacement := decodeFixtureHex(t, mutation.Hex)
		end := mutation.Offset + len(replacement)
		if mutation.Offset < 0 || end > len(data) {
			t.Fatalf("invalid replacement range [%d:%d]", mutation.Offset, end)
		}
		copy(data[mutation.Offset:end], replacement)
		return data
	default:
		t.Fatalf("unsupported frame mutation %q", mutation.Type)
		return nil
	}
}

func expectedFrameError(t *testing.T, name string) error {
	t.Helper()
	switch name {
	case "frame_too_short":
		return ErrFrameTooShort
	case "frame_length_mismatch":
		return ErrFrameLengthMismatch
	case "outer_inner_msg_id_mismatch":
		return ErrFrameMsgIDMismatch
	default:
		t.Fatalf("unsupported frame error %q", name)
		return nil
	}
}
