package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

func Encode(packet *Packet) ([]byte, error) {
	if packet == nil {
		return nil, fmt.Errorf("protocol: nil packet")
	}

	fields := []string{
		packet.ClientID,
		packet.DeviceID,
		packet.SessionID,
		packet.MessageID,
		packet.TraceID,
		packet.Token,
	}

	for _, field := range fields {
		if len(field) > math.MaxUint16 {
			return nil, fmt.Errorf("%w: string field exceeds %d bytes", ErrFieldTooLarge, math.MaxUint16)
		}
	}

	if len(packet.Body) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrBodyTooLarge, uint64(math.MaxUint32))
	}

	packet.Version = Version

	totalLen := FixedHeaderSize + len(packet.Body)
	for _, field := range fields {
		totalLen += len(field)
	}

	buf := bytes.NewBuffer(make([]byte, 0, totalLen))
	writeUint16(buf, Magic)
	buf.WriteByte(packet.Version)
	writeUint16(buf, uint16(packet.Flags))
	writeUint32(buf, packet.MsgID)
	writeUint64(buf, packet.Seq)
	writeUint64(buf, uint64(packet.Timestamp))

	for _, field := range fields {
		writeUint16(buf, uint16(len(field)))
	}

	writeUint32(buf, uint32(len(packet.Body)))

	for _, field := range fields {
		buf.WriteString(field)
	}

	buf.Write(packet.Body)
	return buf.Bytes(), nil
}

func Decode(data []byte) (*Packet, error) {
	return DecodeWithMaxBodySize(data, DefaultMaxBodySize)
}

func DecodeWithMaxBodySize(data []byte, maxBodySize uint32) (*Packet, error) {
	if len(data) < FixedHeaderSize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrPacketTooShort, len(data))
	}

	reader := bytes.NewReader(data)
	packet, expectedLen, err := readPacket(reader, maxBodySize)
	if err != nil {
		return nil, err
	}

	if expectedLen != len(data) {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d bytes", ErrLengthMismatch, expectedLen, len(data))
	}

	return packet, nil
}

func DecodeReader(reader io.Reader, maxBodySize uint32) (*Packet, error) {
	packet, _, err := readPacket(reader, maxBodySize)
	return packet, err
}

func readPacket(reader io.Reader, maxBodySize uint32) (*Packet, int, error) {
	fixed := make([]byte, FixedHeaderSize)
	if _, err := io.ReadFull(reader, fixed); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrPacketTooShort, err)
	}

	if binary.BigEndian.Uint16(fixed[0:2]) != Magic {
		return nil, 0, ErrInvalidMagic
	}

	version := fixed[2]
	if version != Version {
		return nil, 0, fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}

	flags := Flags(binary.BigEndian.Uint16(fixed[3:5]))
	msgID := binary.BigEndian.Uint32(fixed[5:9])
	seq := binary.BigEndian.Uint64(fixed[9:17])
	timestamp := int64(binary.BigEndian.Uint64(fixed[17:25]))

	offset := 25
	fieldLens := make([]uint16, 6)
	for i := range fieldLens {
		fieldLens[i] = binary.BigEndian.Uint16(fixed[offset : offset+2])
		offset += 2
	}

	bodyLen := binary.BigEndian.Uint32(fixed[offset : offset+4])
	if bodyLen > maxBodySize {
		return nil, 0, fmt.Errorf("%w: got %d bytes, max %d bytes", ErrBodyTooLarge, bodyLen, maxBodySize)
	}

	varLen := 0
	for _, fieldLen := range fieldLens {
		varLen += int(fieldLen)
	}

	expectedLen := FixedHeaderSize + varLen + int(bodyLen)
	varPart := make([]byte, varLen+int(bodyLen))
	if _, err := io.ReadFull(reader, varPart); err != nil {
		return nil, expectedLen, fmt.Errorf("%w: %v", ErrLengthMismatch, err)
	}

	cursor := 0
	readString := func(fieldLen uint16) string {
		end := cursor + int(fieldLen)
		value := string(varPart[cursor:end])
		cursor = end
		return value
	}

	packet := &Packet{
		Header: Header{
			Version:   version,
			Flags:     flags,
			MsgID:     msgID,
			Seq:       seq,
			Timestamp: timestamp,
		},
		ClientID:  readString(fieldLens[0]),
		DeviceID:  readString(fieldLens[1]),
		SessionID: readString(fieldLens[2]),
		MessageID: readString(fieldLens[3]),
		TraceID:   readString(fieldLens[4]),
		Token:     readString(fieldLens[5]),
	}

	if bodyLen > 0 {
		packet.Body = cloneBytes(varPart[cursor : cursor+int(bodyLen)])
	}

	return packet, expectedLen, nil
}

func writeUint16(writer io.Writer, value uint16) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], value)
	_, _ = writer.Write(buf[:])
}

func writeUint32(writer io.Writer, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	_, _ = writer.Write(buf[:])
}

func writeUint64(writer io.Writer, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = writer.Write(buf[:])
}
