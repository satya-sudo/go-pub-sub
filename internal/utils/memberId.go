package utils

import (
	"crypto/rand"
	"encoding/hex"
)

// NewMemberID returns a short random id for consumer group members.
func NewMemberID() string {
	b := make([]byte, 8) // 16 hex chars
	if _, err := rand.Read(b); err != nil {
		// fallback to timestamp-ish value if crypto fails (very unlikely)
		return "m-" + hex.EncodeToString(b)
	}
	return hex.EncodeToString(b)
}
