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
	"time"

	"hexarch/internal/domain"
)

// DueBucket is the semantic due-date filter vocabulary shared by adapters.
// The application resolves it into absolute DeadlineFrom/DeadlineTo bounds so
// repositories never depend on the wall clock or a timezone.
type DueBucket string

const (
	DueOverdue DueBucket = "overdue"
	DueToday   DueBucket = "today"
	DueSoon    DueBucket = "soon"
)

// SortField is the list-sort vocabulary shared by adapters.
type SortField string

const (
	SortPriority SortField = "priority"
	SortCreated  SortField = "created"
	SortUpdated  SortField = "updated"
	SortDeadline SortField = "deadline"
	SortTitle    SortField = "title"
)

// SortOrder is the list-sort direction.
type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

// TaskFilter is a value object describing how to narrow a list query.
// UserID scopes the query to one owner; nil means "all users" (admin scope).
//
// Due is semantic and is resolved by the application into DeadlineFrom and
// DeadlineTo. Statuses is used when Status is nil (for example active-only due
// queries). If both Status and Statuses are set, Status takes precedence.
type TaskFilter struct {
	UserID   *domain.UserID
	Status   *domain.Status
	Statuses []domain.Status
	Search   string

	Due          *DueBucket
	DeadlineFrom *time.Time // inclusive, resolved by the application
	DeadlineTo   *time.Time // exclusive, resolved by the application

	Sort  SortField
	Order SortOrder

	Limit  int
	Offset int
}

// Validate rejects unknown due/sort/order tokens. Adapters parse input into
// these types, but the application validates defensively before querying.
func (f TaskFilter) Validate() error {
	if f.Due != nil {
		switch *f.Due {
		case DueOverdue, DueToday, DueSoon:
		default:
			return domain.Invalid("invalid due filter " + string(*f.Due) + " (want overdue|today|soon)")
		}
	}
	switch f.Sort {
	case "", SortPriority, SortCreated, SortUpdated, SortDeadline, SortTitle:
	default:
		return domain.Invalid("invalid sort " + string(f.Sort) + " (want priority|created|updated|deadline|title)")
	}
	switch f.Order {
	case "", SortAsc, SortDesc:
	default:
		return domain.Invalid("invalid sort order " + string(f.Order) + " (want asc|desc)")
	}
	return nil
}

// EffectiveSort applies the documented defaults. An explicit order is always
// honored; otherwise date/priority sorts default to desc and title to asc.
func (f TaskFilter) EffectiveSort() (SortField, SortOrder) {
	sort := f.Sort
	if sort == "" {
		sort = SortPriority
	}
	order := f.Order
	if order == "" {
		if sort == SortTitle {
			order = SortAsc
		} else {
			order = SortDesc
		}
	}
	return sort, order
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
	// List returns tasks matching the filter, using the filter's effective
	// sort/order and paging. With no sort/order it preserves the default
	// priority desc, created_at desc ordering.
	List(ctx context.Context, filter TaskFilter) ([]domain.Task, error)
	// Count returns the number of tasks matching every filter field except
	// paging and sorting. UserID nil means "all users" (admin scope).
	Count(ctx context.Context, f TaskFilter) (int, error)
	// CountByStatus returns the number of tasks in a given status, honoring
	// the same filter fields as Count.
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
