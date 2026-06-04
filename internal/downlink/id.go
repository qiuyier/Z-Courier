package downlink

import "crypto/rand"

func NewMessageID() string {
	return "zm_" + rand.Text()
}
