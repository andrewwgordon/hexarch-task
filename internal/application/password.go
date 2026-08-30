// password.go centralizes password hashing for the application layer. The
// domain stores only hashes; this is the single place that turns clear
// passwords into bcrypt hashes and verifies them (spec docs/auth.md §3.4,
// NFR-1).
//
// Public API:
//   - HashPassword(clear string, cost int) (string, error)
//   - CheckPassword(hash, clear string) bool
//
// (Costs: DefaultPasswordCost / TestPasswordCost live in task_service.go.)
package application

import (
	"golang.org/x/crypto/bcrypt"

	"hexarch/internal/domain"
)

// HashPassword hashes a clear password with bcrypt at the given cost. The
// domain and repository layers never see clear passwords.
func HashPassword(clear string, cost int) (string, error) {
	if clear == "" {
		return "", domain.Invalid("password must not be empty")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(clear), cost)
	if err != nil {
		return "", domain.Storage("hash password: " + err.Error())
	}
	return string(h), nil
}

// CheckPassword verifies a clear password against a stored bcrypt hash.
// It returns false for any mismatch (including malformed hashes) and never
// leaks timing information beyond what bcrypt itself provides.
func CheckPassword(hash, clear string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(clear)) == nil
}

// dummyHash is a valid bcrypt hash of an unguessable random value, compared
// against when the email is unknown so that AuthUser's response time does
// not reveal whether an account exists (anti-enumeration, spec FR-U3).
const dummyHash = "$2a$04$X64jKbP6kJbubwOHQ3b4QO6OWQC.zl4FTLcZrdQn1O8DR9PCBGOdO"
