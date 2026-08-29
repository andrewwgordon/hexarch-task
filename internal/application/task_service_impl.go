// task_service_impl.go provides the concrete TaskService implementation.
//
// TaskServiceImpl is the only place in this layer that composes the domain
// rules with the repository port. ID generation and the clock are injected
// as functions so tests can substitute deterministic sources
// (see NewTaskServiceWith).
//
// Public API:
//   - Types:   TaskServiceImpl
//   - Funcs:   NewTaskService, NewTaskServiceWith
//   - Methods: the ten TaskService interface methods
//
// Private:
//   - fields: repo, idGen, clock
package application

import (
	"context"
	"time"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// TaskServiceImpl implements the use cases. It has no idea whether the
// repository is backed by SQLite, Postgres, or an in-memory map.
type TaskServiceImpl struct {
	repo  repository.TaskRepository
	idGen func() (domain.TaskID, error)
	clock func() time.Time
}

func NewTaskService(repo repository.TaskRepository) *TaskServiceImpl {
	return &TaskServiceImpl{
		repo:  repo,
		idGen: RandomTaskID,
		clock: time.Now,
	}
}

// NewTaskServiceWith allows tests to inject deterministic ID and clock sources.
func NewTaskServiceWith(repo repository.TaskRepository, idGen func() (domain.TaskID, error), clock func() time.Time) *TaskServiceImpl {
	return &TaskServiceImpl{repo: repo, idGen: idGen, clock: clock}
}

// CreateTask mints a fresh ID, validates and builds the task through the
// domain factory, then persists it. New tasks always start in StatusTodo.
func (s *TaskServiceImpl) CreateTask(ctx context.Context, input CreateTaskInput) (domain.Task, error) {
	id, err := s.idGen()
	if err != nil {
		return domain.Task{}, err
	}
	task, err := domain.NewTask(id, input.Title, input.Description, input.Priority, input.Deadline, s.clock())
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.Create(ctx, task); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

// GetTask retrieves a single task by ID; a missing task yields a NotFound
// domain error.
func (s *TaskServiceImpl) GetTask(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	return s.repo.ByID(ctx, id)
}

// ListTasks returns the tasks matching the filter, ordered by the
// repository's contract (priority desc, then created_at desc).
func (s *TaskServiceImpl) ListTasks(ctx context.Context, filter repository.TaskFilter) ([]domain.Task, error) {
	return s.repo.List(ctx, filter)
}

// RenameTask loads the task, applies the domain's rename rule, and persists
// the updated copy.
func (s *TaskServiceImpl) RenameTask(ctx context.Context, id domain.TaskID, title string) (domain.Task, error) {
	task, err := s.repo.ByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	updated, err := task.Rename(title, s.clock())
	if err != nil {
		return domain.Task{}, err
	}
	return updated, s.repo.Update(ctx, updated)
}

// ChangeStatus loads the task and applies the domain state machine (e.g.
// todo -> in_progress); illegal transitions surface as a Conflict.
func (s *TaskServiceImpl) ChangeStatus(ctx context.Context, id domain.TaskID, status domain.Status) (domain.Task, error) {
	task, err := s.repo.ByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	updated, err := task.WithStatus(status, s.clock())
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return domain.Task{}, err
	}
	return updated, nil
}

// ChangePriority loads the task and applies the domain's priority rule,
// persisting the updated copy.
func (s *TaskServiceImpl) ChangePriority(ctx context.Context, id domain.TaskID, priority int) (domain.Task, error) {
	task, err := s.repo.ByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	updated, err := task.WithPriority(priority, s.clock())
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return domain.Task{}, err
	}
	return updated, nil
}

// SetDeadline loads the task and assigns the given deadline.
func (s *TaskServiceImpl) SetDeadline(ctx context.Context, id domain.TaskID, deadline time.Time) (domain.Task, error) {
	task, err := s.repo.ByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	updated, err := task.WithDeadline(deadline, s.clock())
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return domain.Task{}, err
	}
	return updated, nil
}

// ClearDeadline loads the task and removes any deadline.
func (s *TaskServiceImpl) ClearDeadline(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	task, err := s.repo.ByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	updated, err := task.ClearDeadline(s.clock())
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return domain.Task{}, err
	}
	return updated, nil
}

// DeleteTask removes a task by ID; a missing task yields a NotFound domain
// error.
func (s *TaskServiceImpl) DeleteTask(ctx context.Context, id domain.TaskID) error {
	return s.repo.Delete(ctx, id)
}

// Stats aggregates per-status counts into a single TaskStats value. The
// total is derived independently so a storage error in one counter never
// corrupts the others.
func (s *TaskServiceImpl) Stats(ctx context.Context) (TaskStats, error) {
	total, err := s.repo.Count(ctx)
	if err != nil {
		return TaskStats{}, err
	}
	todo, err := s.repo.CountByStatus(ctx, domain.StatusTodo)
	if err != nil {
		return TaskStats{}, err
	}
	inProgress, err := s.repo.CountByStatus(ctx, domain.StatusInProgress)
	if err != nil {
		return TaskStats{}, err
	}
	done, err := s.repo.CountByStatus(ctx, domain.StatusDone)
	if err != nil {
		return TaskStats{}, err
	}
	archived, err := s.repo.CountByStatus(ctx, domain.StatusArchived)
	if err != nil {
		return TaskStats{}, err
	}
	return TaskStats{Total: total, Todo: todo, InProgress: inProgress, Done: done, Archived: archived}, nil
}
