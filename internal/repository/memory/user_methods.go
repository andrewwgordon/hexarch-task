// user_methods.go implements the seven user methods of the TaskRepository
// port for the in-memory backend, plus owner-scoped task filtering and the
// delete-user cascade. It is the reference implementation for the conformance
// suite.
//
// Public API: (none — methods on TaskRepositoryMem)
package memory

import (
	"context"
	"sort"
	"strings"

	"hexarch/internal/domain"
)

// CreateUser inserts a new user, returning domain.Conflict when the email or
// apikey is already stored.
func (m *TaskRepositoryMem) CreateUser(_ context.Context, u domain.User) error {
	m.seedOnce()
	for _, existing := range m.users {
		if u.Email() == existing.Email() {
			return domain.Conflict("user email " + u.Email() + " already exists")
		}
		if existing.APIKey() == u.APIKey() {
			return domain.Conflict("apikey already exists")
		}
	}
	m.users[u.ID().String()] = u
	return nil
}

// UpdateUser replaces the stored user (email/password/isadmin), returning
// domain.NotFound when absent and domain.Conflict on duplicate email.
func (m *TaskRepositoryMem) UpdateUser(_ context.Context, u domain.User) error {
	m.seedOnce()
	key := u.ID().String()
	if _, exists := m.users[key]; !exists {
		return domain.NotFound("user " + key + " not found")
	}
	for _, other := range m.users {
		if other.ID() != u.ID() && other.Email() == u.Email() {
			return domain.Conflict("email " + u.Email() + " already exists")
		}
	}
	m.users[key] = u
	return nil
}

// DeleteUser removes the user and all tasks owned by them (cascade).
func (m *TaskRepositoryMem) DeleteUser(_ context.Context, id domain.UserID) error {
	m.seedOnce()
	key := id.String()
	if _, exists := m.users[key]; !exists {
		return domain.NotFound("user " + key + " not found")
	}
	delete(m.users, key)
	for tid, t := range m.store {
		if t.UserID() == id {
			delete(m.store, tid)
		}
	}
	return nil
}

// ListUsers returns every user ordered by email (the repo contract).
func (m *TaskRepositoryMem) ListUsers(_ context.Context) ([]domain.User, error) {
	m.seedOnce()
	out := make([]domain.User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Email() < out[j].Email()
	})
	return out, nil
}

// UserByID returns the user with the given ID, or domain.NotFound.
func (m *TaskRepositoryMem) UserByID(_ context.Context, id domain.UserID) (domain.User, error) {
	m.seedOnce()
	u, exists := m.users[id.String()]
	if !exists {
		return domain.User{}, domain.NotFound("user " + id.String() + " not found")
	}
	return u, nil
}

// AuthUser is a LOOKUP ONLY by lower-cased email; no password verification
// happens here (that is the application layer's job).
func (m *TaskRepositoryMem) AuthUser(_ context.Context, email string) (domain.User, error) {
	m.seedOnce()
	needle := strings.ToLower(email)
	for _, u := range m.users {
		if u.Email() == needle {
			return u, nil
		}
	}
	return domain.User{}, domain.NotFound("user " + email + " not found")
}

// UserByAPIKey looks a user up by API key, or domain.NotFound.
func (m *TaskRepositoryMem) UserByAPIKey(_ context.Context, key string) (domain.User, error) {
	m.seedOnce()
	for _, u := range m.users {
		if u.APIKey() == key {
			return u, nil
		}
	}
	return domain.User{}, domain.NotFound("user by apikey not found")
}
