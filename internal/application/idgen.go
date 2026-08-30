// idgen.go provides the identifier generators used by the application layer.
//
// The domain treats TaskID and UserID as opaque, so the choice of format —
// here UUIDv4-style strings — is an application-level concern. RandomTaskID
// and RandomUserID share the same minting helper; RandomAPIKey produces a
// 256-bit hex token for REST API authentication. These are normally wired in
// by NewTaskService; tests replace them with deterministic sources via
// NewTaskServiceWith (and the user-generator variant).
//
// Public API:
//   - RandomTaskID, RandomUserID, RandomAPIKey
package application

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"hexarch/internal/domain"
)

// randomUUID mints a UUIDv4-style identifier string. The domain treats IDs
// as opaque, so the application is free to choose the format.
func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// RandomTaskID mints a UUIDv4-style task identifier.
func RandomTaskID() (domain.TaskID, error) {
	s, err := randomUUID()
	if err != nil {
		return "", err
	}
	return domain.TaskID(s), nil
}

// RandomUserID mints a UUIDv4-style user identifier, sharing the generator
// used for task IDs.
func RandomUserID() (domain.UserID, error) {
	s, err := randomUUID()
	if err != nil {
		return domain.UserID(""), err
	}
	return domain.UserID(s), nil
}

// RandomAPIKey mints a 256-bit cryptographically random API key, hex-encoded
// to 64 characters. Unique-index collision is astronomically unlikely, and
// the repository enforces uniqueness regardless (spec NFR-3).
func RandomAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
