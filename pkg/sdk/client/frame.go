package client

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

const (
	outerFrameHeaderSize = 8

	// DefaultMaxFramePayloadSize matches the default gateway-side Zinx packet
	// limit. The inner protocol decoder applies its own body limit separately.
	DefaultMaxFramePayloadSize uint32 = 8 << 20
)

type wireFrame struct {
	msgID   uint32
	payload []byte
}

func encodeFrame(msgID uint32, payload []byte) ([]byte, error) {
	if uint64(len(payload)) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: got %d bytes, max %d", ErrFrameTooLarge, len(payload), uint64(math.MaxUint32))
	}

	data := make([]byte, outerFrameHeaderSize+len(payload))
	binary.BigEndian.PutUint32(data[0:4], msgID)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(payload)))
	copy(data[outerFrameHeaderSize:], payload)
	return data, nil
}

func encodePacketFrame(packet *protocol.Packet, maxPayloadSize uint32) ([]byte, error) {
	payload, err := protocol.Encode(packet)
	if err != nil {
		return nil, err
	}
	if uint64(len(payload)) > uint64(normalizeFrameLimit(maxPayloadSize)) {
		return nil, fmt.Errorf("%w: got %d bytes, max %d", ErrFrameTooLarge, len(payload), normalizeFrameLimit(maxPayloadSize))
	}
	return encodeFrame(packet.MsgID, payload)
}

func writePacketFrame(writer io.Writer, packet *protocol.Packet, maxPayloadSize uint32) error {
	data, err := encodePacketFrame(packet, maxPayloadSize)
	if err != nil {
		return err
	}
	return writeAll(writer, data)
}

func readFrame(reader io.Reader, maxPayloadSize uint32) (wireFrame, error) {
	var header [outerFrameHeaderSize]byte
	read, err := io.ReadFull(reader, header[:])
	if err != nil {
		if err == io.EOF && read == 0 {
			return wireFrame{}, io.EOF
		}
		return wireFrame{}, fmt.Errorf("%w: read %d of %d bytes: %w", ErrFrameTooShort, read, outerFrameHeaderSize, err)
	}

	msgID := binary.BigEndian.Uint32(header[0:4])
	payloadSize := binary.BigEndian.Uint32(header[4:8])
	limit := normalizeFrameLimit(maxPayloadSize)
	if payloadSize > limit {
		return wireFrame{}, fmt.Errorf("%w: got %d bytes, max %d", ErrFrameTooLarge, payloadSize, limit)
	}

	payload := make([]byte, payloadSize)
	read, err = io.ReadFull(reader, payload)
	if err != nil {
		return wireFrame{}, fmt.Errorf("%w: read %d of %d payload bytes: %w", ErrFrameLengthMismatch, read, payloadSize, err)
	}
	return wireFrame{msgID: msgID, payload: payload}, nil
}

func readPacketFrame(reader io.Reader, maxPayloadSize, maxBodySize uint32) (*protocol.Packet, error) {
	frame, err := readFrame(reader, maxPayloadSize)
	if err != nil {
		return nil, err
	}
	return decodePacketFrame(frame, maxBodySize)
}

func decodePacketFrame(frame wireFrame, maxBodySize uint32) (*protocol.Packet, error) {
	if maxBodySize == 0 {
		maxBodySize = protocol.DefaultMaxBodySize
	}
	packet, err := protocol.DecodeWithMaxBodySize(frame.payload, maxBodySize)
	if err != nil {
		return nil, err
	}
	if frame.msgID != packet.MsgID {
		return nil, fmt.Errorf("%w: outer %d, inner %d", ErrFrameMsgIDMismatch, frame.msgID, packet.MsgID)
	}
	return packet, nil
}

func decodeExactFrame(data []byte, maxPayloadSize uint32) (wireFrame, error) {
	reader := bytes.NewReader(data)
	frame, err := readFrame(reader, maxPayloadSize)
	if err != nil {
		return wireFrame{}, err
	}
	if reader.Len() != 0 {
		return wireFrame{}, fmt.Errorf("%w: %d trailing bytes", ErrFrameLengthMismatch, reader.Len())
	}
	return frame, nil
}

func normalizeFrameLimit(limit uint32) uint32 {
	if limit == 0 {
		return DefaultMaxFramePayloadSize
	}
	return limit
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
