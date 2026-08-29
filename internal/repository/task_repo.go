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
package repository

import (
	"context"

	"hexarch/internal/domain"
)

// TaskFilter is a value object describing how to narrow a list query.
type TaskFilter struct {
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
	// Count returns the total number of tasks.
	Count(ctx context.Context) (int, error)
	// CountByStatus returns the number of tasks in a given status.
	CountByStatus(ctx context.Context, status domain.Status) (int, error)
	// Update persists all mutable fields of an existing task.
	Update(ctx context.Context, task domain.Task) error
	// Delete removes a task; returns NotFound if absent.
	Delete(ctx context.Context, id domain.TaskID) error
}
