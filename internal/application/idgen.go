// idgen.go provides the identifier generator used by the application layer.
//
// The domain treats TaskID as opaque, so the choice of format — here a
// UUIDv4-style string — is an application-level concern. RandomTaskID is
// normally wired in by NewTaskService; tests replace it with a deterministic
// source via NewTaskServiceWith.
//
// Public API:
//   - RandomTaskID
package application

import (
	"crypto/rand"
	"fmt"

	"hexarch/internal/domain"
)

// RandomTaskID mints a UUIDv4-style identifier. The domain treats IDs as
// opaque, so the application is free to choose the format.
func RandomTaskID() (domain.TaskID, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	uuidStr := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return domain.TaskID(uuidStr), nil
}
