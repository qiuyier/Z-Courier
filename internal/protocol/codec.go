package protocol

import (
	"io"

	sdkprotocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func Encode(packet *Packet) ([]byte, error) {
	return sdkprotocol.Encode(packet)
}

func Decode(data []byte) (*Packet, error) {
	return sdkprotocol.Decode(data)
}

func DecodeWithMaxBodySize(data []byte, maxBodySize uint32) (*Packet, error) {
	return sdkprotocol.DecodeWithMaxBodySize(data, maxBodySize)
}

func DecodeReader(reader io.Reader, maxBodySize uint32) (*Packet, error) {
	return sdkprotocol.DecodeReader(reader, maxBodySize)
}
