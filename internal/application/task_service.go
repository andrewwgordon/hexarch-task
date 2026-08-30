// Package application implements the use cases ("application services") of
// the hexagon. It owns orchestration — ID generation, the clock, and the
// read/validate/persist sequences — and depends only on the domain package
// and on the repository.TaskRepository interface, never on a concrete
// storage engine.
//
// This file (task_service.go) declares the inbound port every adapter talks
// to (TaskService), the input structures adapters pass in (CreateTaskInput,
// CreateUserInput, UpdateUserInput), and the reporting value object
// (TaskStats).
//
// Public API:
//   - Interface: TaskService
//   - Types:     CreateTaskInput, CreateUserInput, UpdateUserInput, TaskStats
//   - Funcs:     DefaultPasswordCost, TestPasswordCost
package application

import (
	"context"
	"time"

	"golang.org/x/crypto/bcrypt"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// CreateTaskInput is the driver-provided data for creating a task.
// UserID is the owner of the task: it is required (non-empty) and must
// reference an existing user; adapters fill it from the authenticated user.
type CreateTaskInput struct {
	UserID      domain.UserID
	Title       string
	Description string
	Priority    int
	Deadline    *time.Time
}

// CreateUserInput is the driver-provided data for creating a user. Password
// arrives in clear text and is hashed by the service before persistence.
type CreateUserInput struct {
	Email    string
	Password string // clear text; hashed by the service before persistence
	IsAdmin  bool
}

// UpdateUserInput carries the mutable fields of a user. Nil pointers mean
// "unchanged"; a non-nil Password is hashed by the service before storing.
type UpdateUserInput struct {
	Email    *string // nil = unchanged
	Password *string // nil = unchanged; if set, hashed by the service
	IsAdmin  *bool   // nil = unchanged
}

// TaskStats is a small reporting value object.
type TaskStats struct {
	Total, Todo, InProgress, Done, Archived int
}

// Caller is the authenticated identity performing a task operation. Task
// access control lives in the application layer (spec docs/auth.md FR-U4):
// a caller may only read or modify tasks they own, unless they are an admin,
// who may manage every user's tasks.
type Caller struct {
	UserID  domain.UserID
	IsAdmin bool
}

// CallerOf builds the Caller identity from an authenticated user. Adapters
// resolve the user via their auth flow and pass the result into every task
// operation.
func CallerOf(u domain.User) Caller {
	return Caller{UserID: u.ID(), IsAdmin: u.IsAdmin()}
}

// TaskService is the inbound-facing application boundary. Adapters (CLI,
// tests, or future HTTP handlers) talk to this.
type TaskService interface {
	CreateTask(ctx context.Context, input CreateTaskInput) (domain.Task, error)
	// GetTask returns the task, honoring caller ownership (FR-U4): a
	// non-admin may only read their own tasks; a task owned by another user
	// surfaces as NotFound so its existence is never disclosed.
	GetTask(ctx context.Context, id domain.TaskID, caller Caller) (domain.Task, error)
	ListTasks(ctx context.Context, filter repository.TaskFilter) ([]domain.Task, error)
	// RenameTask, ChangeStatus, ChangePriority, SetDeadline, ClearDeadline and
	// DeleteTask all enforce the same FR-U4 ownership rule as GetTask.
	RenameTask(ctx context.Context, id domain.TaskID, title string, caller Caller) (domain.Task, error)
	ChangeStatus(ctx context.Context, id domain.TaskID, status domain.Status, caller Caller) (domain.Task, error)
	ChangePriority(ctx context.Context, id domain.TaskID, priority int, caller Caller) (domain.Task, error)
	SetDeadline(ctx context.Context, id domain.TaskID, deadline time.Time, caller Caller) (domain.Task, error)
	ClearDeadline(ctx context.Context, id domain.TaskID, caller Caller) (domain.Task, error)
	DeleteTask(ctx context.Context, id domain.TaskID, caller Caller) error
	// Stats returns per-status counts scoped by f.UserID: regular users must
	// pass their own ID so the summary reflects only their tasks; admins pass
	// nil to span every user (spec docs/auth.md §3.4).
	Stats(ctx context.Context, f repository.TaskFilter) (TaskStats, error)

	// ---- Users (spec docs/auth.md §3.4) ----
	// CreateUser mints id + apikey, hashes the password, and persists the
	// user. Conflict on duplicate email.
	CreateUser(ctx context.Context, input CreateUserInput) (domain.User, error)
	// UpdateUser applies only the non-nil fields of input. Conflict when the
	// change would create a duplicate email, remove the last admin, or
	// (nothing else) — self-demotion is allowed unless the user is the last
	// admin.
	UpdateUser(ctx context.Context, id domain.UserID, input UpdateUserInput) (domain.User, error)
	// DeleteUser removes a user and their tasks. Conflict when deleting the
	// signed-in account would leave zero admins is NOT enforced here — see
	// below: self-delete is blocked by the adapters; DeleteUser blocks only
	// the last-admin case.
	DeleteUser(ctx context.Context, id domain.UserID) error
	// ListUsers returns all users ordered by email.
	ListUsers(ctx context.Context) ([]domain.User, error)
	// UserByID returns the user or NotFound.
	UserByID(ctx context.Context, id domain.UserID) (domain.User, error)
	// AuthUser verifies an email/password pair. Unknown email and wrong
	// password produce the SAME generic error (no account enumeration).
	AuthUser(ctx context.Context, email, password string) (domain.User, error)
	// UserByAPIKey looks a user up by API key (X-API-Key auth path).
	UserByAPIKey(ctx context.Context, key string) (domain.User, error)
}

const (
	// DefaultPasswordCost is the bcrypt cost for production hashing.
	DefaultPasswordCost = bcrypt.DefaultCost
	// TestPasswordCost is the reduced cost used by test suites to keep them
	// fast while still exercising the real bcrypt code path.
	TestPasswordCost = bcrypt.MinCost
)
