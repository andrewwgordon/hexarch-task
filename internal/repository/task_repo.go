// Package repository defines the outbound (right-side) port of the hexagon
// — the TaskRepository interface and TaskFilter — plus the configuration
// and the provider registry/factory that turn a DBType into a live port.
// Backend packages (sqlite, postgres, oracle, mongo, memory) self-register
// here and are selected at runtime via the HEXARCH_DB_* environment
// variables.
//
// This file (task_repo.go) declares the port itself.
//
// Public API:
//   - Types:  TaskFilter
//   - Interface: TaskRepository
//   - Funcs:  DefaultTaskFilter
//
// User methods (phase 4 of docs/auth-plan.md): CreateUser, UpdateUser,
// DeleteUser, ListUsers, UserByID, AuthUser (a lookup only — bcrypt password
// verification happens in the application layer), UserByAPIKey.
package repository

import (
	"context"

	"hexarch/internal/domain"
)

// TaskFilter is a value object describing how to narrow a list query.
// UserID scopes the query to one owner; nil means "all users" (admin scope).
type TaskFilter struct {
	UserID *domain.UserID
	Status *domain.Status
	Search string
	Limit  int
	Offset int
}

// DefaultTaskFilter returns the standard filter: every task, no offset, at
// most 100 results. Adapters start from this and override the fields they
// care about.
func DefaultTaskFilter() TaskFilter {
	return TaskFilter{Limit: 100, Offset: 0}
}

// TaskRepository is the outbound port of the hexagon. The application layer
// depends only on this interface, never on a concrete database.
type TaskRepository interface {
	// Create inserts a new task. It returns Conflict if the ID already exists.
	Create(ctx context.Context, task domain.Task) error
	// ByID returns the task or a NotFound error.
	ByID(ctx context.Context, id domain.TaskID) (domain.Task, error)
	// List returns tasks ordered by (priority desc, created_at desc),
	// with the given paging and filters.
	List(ctx context.Context, filter TaskFilter) ([]domain.Task, error)
	// Count returns the total number of tasks, honoring the filter's UserID
	// scope (nil = all users, admin scope).
	Count(ctx context.Context, f TaskFilter) (int, error)
	// CountByStatus returns the number of tasks in a given status, honoring
	// the filter's UserID scope (nil = all users, admin scope).
	CountByStatus(ctx context.Context, status domain.Status, f TaskFilter) (int, error)
	// Update persists all mutable fields of an existing task.
	Update(ctx context.Context, task domain.Task) error
	// Delete removes a task; returns NotFound if absent.
	Delete(ctx context.Context, id domain.TaskID) error

	// ---- Users (spec docs/auth.md §3.5) ----
	// NOTE: none of the user methods hash or verify passwords. Password
	// hashing/comparison lives exclusively in the application layer.

	// CreateUser inserts a new user. It returns Conflict if the email or
	// apikey already exists.
	CreateUser(ctx context.Context, user domain.User) error
	// UpdateUser persists all mutable fields (email, password hash, isadmin)
	// of an existing user; NotFound if absent, Conflict on duplicate email.
	UpdateUser(ctx context.Context, user domain.User) error
	// DeleteUser removes a user and (per backend contract) all tasks owned by
	// that user; returns NotFound if absent.
	DeleteUser(ctx context.Context, id domain.UserID) error
	// ListUsers returns all users ordered by email.
	ListUsers(ctx context.Context) ([]domain.User, error)
	// UserByID returns the user or a NotFound error.
	UserByID(ctx context.Context, id domain.UserID) (domain.User, error)
	// AuthUser is a LOOKUP ONLY by lower-cased email; it performs no password
	// verification. Returns NotFound for unknown emails.
	AuthUser(ctx context.Context, email string) (domain.User, error)
	// UserByAPIKey looks a user up by API key; returns NotFound if absent.
	// Used by the X-API-Key authentication path.
	UserByAPIKey(ctx context.Context, key string) (domain.User, error)
}
