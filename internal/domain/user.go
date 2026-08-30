// Package domain holds the pure business core of the hexagon: the Task
// entity, its status state machine, validation rules, and typed errors. It
// imports no other internal package and knows nothing about databases,
// terminals, or network protocols, so it can be tested and reused anywhere.
//
// This file (user.go) defines the User value object: the UserID type, the
// User entity with its accessors, and the creation/hydration factories.
// Password holds only a bcrypt hash — the clear password never reaches the
// domain; hashing is an application concern (see application/password.go).
//
// Public API:
//   - Types:   UserID, User
//   - Methods: value accessors (ID, Email, Password, APIKey, IsAdmin)
//   - Funcs:   NewUser, HydrateUser
package domain

import (
	"strings"
)

// UserID is the domain-level identity of a user. It is opaque to the core;
// how IDs are minted is an application concern (see idgen.go).
type UserID string

// String returns the ID as a plain string.
func (id UserID) String() string { return string(id) }

// User is the core domain entity for an application user. The password field
// carries only a bcrypt hash — never the clear password. Like Task, it has no
// dependency on any storage engine, network, or UI framework.
type User struct {
	id       UserID
	email    string
	password string // bcrypt hash, never clear text
	apikey   string
	isAdmin  bool
}

// ---- Accessors ----

// ID returns the user's opaque identity.
func (u User) ID() UserID { return u.id }

// Email returns the (lower-cased) email address.
func (u User) Email() string { return u.email }

// Password returns the stored bcrypt hash — never the clear password.
func (u User) Password() string { return u.password }

// APIKey returns the user's API key token.
func (u User) APIKey() string { return u.apikey }

// IsAdmin reports whether the user has the admin role.
func (u User) IsAdmin() bool { return u.isAdmin }

// ---- Factories ----

// NewUser builds a brand-new user. The caller (application layer) hashes the
// clear password with bcrypt and passes only the hash. Email is normalized to
// lowercase. All identifiers must be non-empty.
func NewUser(id UserID, email, passwordHash, apiKey string, isAdmin bool) (User, error) {
	if id == "" {
		return User{}, Invalid("user id must not be empty")
	}
	if email == "" {
		return User{}, Invalid("email must not be empty")
	}
	if !containsAt(email) {
		return User{}, Invalid("email must contain '@'")
	}
	if passwordHash == "" {
		return User{}, Invalid("password hash must not be empty")
	}
	if apiKey == "" {
		return User{}, Invalid("apikey must not be empty")
	}
	email = toLowerEmail(email)
	return User{
		id: id, email: email, password: passwordHash,
		apikey: apiKey, isAdmin: isAdmin,
	}, nil
}

// HydrateUser rebuilds an existing user from a durable representation. It is
// the inverse of NewUser and applies the same validation.
func HydrateUser(id UserID, email, passwordHash, apiKey string, isAdmin bool) (User, error) {
	return NewUser(id, email, passwordHash, apiKey, isAdmin)
}

// containsAt reports whether the email has an '@' with content on both sides.
func containsAt(email string) bool {
	for i := 0; i < len(email); i++ {
		if email[i] == '@' {
			return i > 0 && i < len(email)-1
		}
	}
	return false
}

// toLowerEmail normalizes an email to lowercase. Kept as a tiny helper so
// both factories share the exact normalization rule.
func toLowerEmail(s string) string { return strings.ToLower(s) }
