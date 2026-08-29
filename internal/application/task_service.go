// Package application implements the use cases ("application services") of
// the hexagon. It owns orchestration — ID generation, the clock, and the
// read/validate/persist sequences — and depends only on the domain package
// and on the repository.TaskRepository interface, never on a concrete
// storage engine.
//
// This file (task_service.go) declares the inbound port every adapter talks
// to (TaskService), the input structures adapters pass in (CreateTaskInput),
// and the reporting value object (TaskStats).
//
// Public API:
//   - Interface: TaskService
//   - Types:     CreateTaskInput, TaskStats
package application

import (
	"context"
	"time"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// CreateTaskInput is the driver-provided data for creating a task.
type CreateTaskInput struct {
	Title       string
	Description string
	Priority    int
	Deadline    *time.Time
}

// TaskStats is a small reporting value object.
type TaskStats struct {
	Total, Todo, InProgress, Done, Archived int
}

// TaskService is the inbound-facing application boundary. Adapters (CLI,
// tests, or future HTTP handlers) talk to this.
type TaskService interface {
	CreateTask(ctx context.Context, input CreateTaskInput) (domain.Task, error)
	GetTask(ctx context.Context, id domain.TaskID) (domain.Task, error)
	ListTasks(ctx context.Context, filter repository.TaskFilter) ([]domain.Task, error)
	RenameTask(ctx context.Context, id domain.TaskID, title string) (domain.Task, error)
	ChangeStatus(ctx context.Context, id domain.TaskID, status domain.Status) (domain.Task, error)
	ChangePriority(ctx context.Context, id domain.TaskID, priority int) (domain.Task, error)
	SetDeadline(ctx context.Context, id domain.TaskID, deadline time.Time) (domain.Task, error)
	ClearDeadline(ctx context.Context, id domain.TaskID) (domain.Task, error)
	DeleteTask(ctx context.Context, id domain.TaskID) error
	Stats(ctx context.Context) (TaskStats, error)
}
