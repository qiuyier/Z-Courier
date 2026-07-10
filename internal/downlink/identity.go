package downlink

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

const messageIdentityDomain = "z-courier-downlink-identity-v1"

func messageIdentityFingerprint(message Message) []byte {
	digest := sha256.New()
	writeIdentityBytes(digest, []byte(messageIdentityDomain))
	writeIdentityBytes(digest, []byte(message.ClientID))
	writeIdentityBytes(digest, []byte(message.DeviceID))

	var msgID [4]byte
	binary.BigEndian.PutUint32(msgID[:], message.MsgID)
	_, _ = digest.Write(msgID[:])
	if message.AckRequired {
		_, _ = digest.Write([]byte{1})
	} else {
		_, _ = digest.Write([]byte{0})
	}

	bodyDigest := sha256.Sum256(message.Body)
	_, _ = digest.Write(bodyDigest[:])
	return digest.Sum(nil)
}

func messagesHaveSameIdentity(left, right Message) bool {
	return bytes.Equal(identityFingerprint(left), identityFingerprint(right))
}

func identityFingerprint(message Message) []byte {
	if len(message.IdentityFingerprint) == sha256.Size {
		return message.IdentityFingerprint
	}
	return messageIdentityFingerprint(message)
}

func writeIdentityBytes(target hash.Hash, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = target.Write(size[:])
	_, _ = target.Write(value)
}
