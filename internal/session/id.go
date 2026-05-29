package session

import (
	"crypto/rand"
)

func NewID() string {
	return "zs_" + rand.Text()
}
