package protocol

import (
	"bytes"
	"testing"

	sdkprotocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func TestPublicAndInternalProtocolCompatibility(t *testing.T) {
	publicPacket := sdkprotocol.NewPacket(1001, []byte("public-to-internal"))
	publicPacket.ClientID = "client-1"

	encoded, err := sdkprotocol.Encode(publicPacket)
	if err != nil {
		t.Fatalf("public Encode() error = %v", err)
	}
	internalPacket, err := Decode(encoded)
	if err != nil {
		t.Fatalf("internal Decode() error = %v", err)
	}
	if internalPacket.ClientID != publicPacket.ClientID || !bytes.Equal(internalPacket.Body, publicPacket.Body) {
		t.Fatalf("internal packet = %+v, public packet = %+v", internalPacket, publicPacket)
	}

	var _ *sdkprotocol.Packet = internalPacket
	var _ *Packet = publicPacket
}
