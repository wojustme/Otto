package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type RandomIDGenerator struct{}

func (RandomIDGenerator) NewID(prefix string) (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}
