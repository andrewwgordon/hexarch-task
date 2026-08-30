// task_service_impl.go provides the concrete TaskService implementation.
//
// TaskServiceImpl is the only place in this layer that composes the domain
// rules with the repository port. ID generation (task, user, apikey) and the
// clock are injected as functions so tests can substitute deterministic
// sources (see NewTaskServiceWith). Password hashing lives in password.go —
// repositories only ever store and look up hashes.
//
// Public API:
//   - Types:   TaskServiceImpl
//   - Funcs:   NewTaskService, NewTaskServiceWith
//   - Methods: the ten TaskService task methods and the seven user methods
//
// Private:
//   - fields: repo, idGen, userIDGen, apiKeyGen, clock
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
	repo       repository.TaskRepository
	idGen      func() (domain.TaskID, error)
	userIDGen  func() (domain.UserID, error)
	apiKeyGen  func() (string, error)
	clock      func() time.Time
	bCryptCost int
}

func NewTaskService(repo repository.TaskRepository) *TaskServiceImpl {
	return &TaskServiceImpl{
		repo:       repo,
		idGen:      RandomTaskID,
		userIDGen:  RandomUserID,
		apiKeyGen:  RandomAPIKey,
		clock:      time.Now,
		bCryptCost: DefaultPasswordCost,
	}
}

// NewTaskServiceWith allows tests to inject deterministic ID and clock
// sources. userIDGen and apiKeyGen default to the random generators when
// nil, so existing tests keep compiling unchanged.
func NewTaskServiceWith(repo repository.TaskRepository, idGen func() (domain.TaskID, error), clock func() time.Time) *TaskServiceImpl {
	return &TaskServiceImpl{
		repo:       repo,
		idGen:      idGen,
		userIDGen:  RandomUserID,
		apiKeyGen:  RandomAPIKey,
		clock:      clock,
		bCryptCost: TestPasswordCost,
	}
}

// CreateTask mints a fresh ID, validates and builds the task through the
// domain factory, then persists it. New tasks always start in StatusTodo.
func (s *TaskServiceImpl) CreateTask(ctx context.Context, input CreateTaskInput) (domain.Task, error) {
	id, err := s.idGen()
	if err != nil {
		return domain.Task{}, err
	}
	// The owner must be supplied by the driver (the authenticated user).
	// Unauthenticated creation is a programming error — no placeholder.
	if input.UserID == "" {
		return domain.Task{}, domain.Invalid("task must have an owner: authenticate first")
	}
	if _, err := s.repo.UserByID(ctx, input.UserID); err != nil {
		return domain.Task{}, domain.Invalid("task owner does not exist")
	}
	task, err := domain.NewTask(id, input.UserID, input.Title, input.Description, input.Priority, input.Deadline, s.clock())
	if err != nil {
		return domain.Task{}, err
	}
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.Create(ctx, task); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

// GetTask retrieves a single task by ID, honoring caller ownership (FR-U4):
// a non-admin may only read tasks they own; another user's task — and a
// missing task — both surface as NotFound.
func (s *TaskServiceImpl) GetTask(ctx context.Context, id domain.TaskID, caller Caller) (domain.Task, error) {
	return s.loadOwned(ctx, id, caller)
}

// ListTasks returns the tasks matching the filter, ordered by the
// repository's contract (priority desc, then created_at desc).
func (s *TaskServiceImpl) ListTasks(ctx context.Context, filter repository.TaskFilter) ([]domain.Task, error) {
	return s.repo.List(ctx, filter)
}

// RenameTask loads the task, applies the domain's rename rule, and persists
// the updated copy. Caller ownership is enforced (FR-U4); admins may rename
// any task.
func (s *TaskServiceImpl) RenameTask(ctx context.Context, id domain.TaskID, title string, caller Caller) (domain.Task, error) {
	task, err := s.loadOwned(ctx, id, caller)
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
// todo -> in_progress); illegal transitions surface as a Conflict. Caller
// ownership is enforced (FR-U4); admins may move any task.
func (s *TaskServiceImpl) ChangeStatus(ctx context.Context, id domain.TaskID, status domain.Status, caller Caller) (domain.Task, error) {
	task, err := s.loadOwned(ctx, id, caller)
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
// persisting the updated copy. Caller ownership is enforced (FR-U4); admins
// may re-prioritize any task.
func (s *TaskServiceImpl) ChangePriority(ctx context.Context, id domain.TaskID, priority int, caller Caller) (domain.Task, error) {
	task, err := s.loadOwned(ctx, id, caller)
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

// SetDeadline loads the task and assigns the given deadline. Caller
// ownership is enforced (FR-U4); admins may set any task's deadline.
func (s *TaskServiceImpl) SetDeadline(ctx context.Context, id domain.TaskID, deadline time.Time, caller Caller) (domain.Task, error) {
	task, err := s.loadOwned(ctx, id, caller)
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

// ClearDeadline loads the task and removes any deadline. Caller ownership is
// enforced (FR-U4); admins may clear any task's deadline.
func (s *TaskServiceImpl) ClearDeadline(ctx context.Context, id domain.TaskID, caller Caller) (domain.Task, error) {
	task, err := s.loadOwned(ctx, id, caller)
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
// error. Caller ownership is enforced (FR-U4): a non-admin may only delete
// their own tasks; admins may delete any task.
func (s *TaskServiceImpl) DeleteTask(ctx context.Context, id domain.TaskID, caller Caller) error {
	if _, err := s.loadOwned(ctx, id, caller); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// loadOwned fetches the task and enforces FR-U4 ownership: admin callers may
// operate on any task; everyone else only on their own. A task the caller
// may not access produces the same NotFound as a missing task, so the
// existence of other users' tasks is never disclosed.
func (s *TaskServiceImpl) loadOwned(ctx context.Context, id domain.TaskID, caller Caller) (domain.Task, error) {
	task, err := s.repo.ByID(ctx, id)
	if err != nil {
		return domain.Task{}, err
	}
	if !caller.IsAdmin && task.UserID() != caller.UserID {
		return domain.Task{}, domain.NotFound("task " + id.String() + " not found")
	}
	return task, nil
}

// Stats aggregates per-status counts into a single TaskStats value, scoped
// by f.UserID (nil = all users, admin scope; regular users must pass their
// own ID — specs docs/auth.md §3.4). The total is derived independently so
// a storage error in one counter never corrupts the others.
func (s *TaskServiceImpl) Stats(ctx context.Context, f repository.TaskFilter) (TaskStats, error) {
	total, err := s.repo.Count(ctx, f)
	if err != nil {
		return TaskStats{}, err
	}
	todo, err := s.repo.CountByStatus(ctx, domain.StatusTodo, f)
	if err != nil {
		return TaskStats{}, err
	}
	inProgress, err := s.repo.CountByStatus(ctx, domain.StatusInProgress, f)
	if err != nil {
		return TaskStats{}, err
	}
	done, err := s.repo.CountByStatus(ctx, domain.StatusDone, f)
	if err != nil {
		return TaskStats{}, err
	}
	archived, err := s.repo.CountByStatus(ctx, domain.StatusArchived, f)
	if err != nil {
		return TaskStats{}, err
	}
	return TaskStats{Total: total, Todo: todo, InProgress: inProgress, Done: done, Archived: archived}, nil
}

// ---- Users (phase 5 of docs/auth-plan.md) ----

// CreateUser mints a user ID and API key, hashes the clear password with
// bcrypt, and persists the user. Duplicate email/apikey surface as Conflict.
func (s *TaskServiceImpl) CreateUser(ctx context.Context, input CreateUserInput) (domain.User, error) {
	id, err := s.userIDGen()
	if err != nil {
		return domain.User{}, err
	}
	apikey, err := s.apiKeyGen()
	if err != nil {
		return domain.User{}, err
	}
	hash, err := HashPassword(input.Password, s.bCryptCost)
	if err != nil {
		return domain.User{}, err
	}
	user, err := domain.NewUser(id, input.Email, hash, apikey, input.IsAdmin)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.repo.CreateUser(ctx, user); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

// UpdateUser loads the user and applies only the non-nil fields of input,
// rehashing when a new password is supplied. The last remaining admin cannot
// be demoted or turned non-admin.
func (s *TaskServiceImpl) UpdateUser(ctx context.Context, id domain.UserID, input UpdateUserInput) (domain.User, error) {
	user, err := s.repo.UserByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	// Guard: the last remaining admin cannot be demoted.
	if input.IsAdmin != nil && !*input.IsAdmin && user.IsAdmin() {
		users, lerr := s.ListUsers(ctx)
		if lerr != nil {
			return domain.User{}, lerr
		}
		admins := 0
		for _, u := range users {
			if u.IsAdmin() {
				admins++
			}
		}
		if admins <= 1 {
			return domain.User{}, domain.Conflict("cannot demote the last admin")
		}
	}
	updated := user
	if input.Email != nil {
		u, err := domain.NewUser(user.ID(), *input.Email, user.Password(), user.APIKey(), user.IsAdmin())
		if err != nil {
			return domain.User{}, err
		}
		updated = u
	}
	if input.Password != nil {
		h, herr := HashPassword(*input.Password, s.bCryptCost)
		if herr != nil {
			return domain.User{}, herr
		}
		u, uerr := domain.NewUser(updated.ID(), updated.Email(), h, updated.APIKey(), updated.IsAdmin())
		if uerr != nil {
			return domain.User{}, uerr
		}
		updated = u
	}
	if input.IsAdmin != nil {
		u, uerr := domain.NewUser(updated.ID(), updated.Email(), updated.Password(), updated.APIKey(), *input.IsAdmin)
		if uerr != nil {
			return domain.User{}, uerr
		}
		updated = u
	}

	if err := s.repo.UpdateUser(ctx, updated); err != nil {
		return domain.User{}, err
	}
	return updated, nil
}

// DeleteUser removes a user and (per repo contract) their tasks. Deleting
// the last remaining admin is refused with a Conflict so the system always
// keeps at least one admin. Self-delete is enforced by the adapters, which
// know the signed-in identity; the service cannot see the caller.
func (s *TaskServiceImpl) DeleteUser(ctx context.Context, id domain.UserID) error {
	user, err := s.repo.UserByID(ctx, id)
	if err != nil {
		return err
	}
	if user.IsAdmin() {
		users, err := s.ListUsers(ctx)
		if err != nil {
			return err
		}
		admins := 0
		for _, u := range users {
			if u.IsAdmin() {
				admins++
			}
		}
		if admins <= 1 {
			return domain.Conflict("cannot delete the last admin")
		}
	}
	return s.repo.DeleteUser(ctx, id)
}

// ListUsers returns every user ordered by email (repo contract).
func (s *TaskServiceImpl) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.repo.ListUsers(ctx)
}

// UserByID returns one user.
func (s *TaskServiceImpl) UserByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	return s.repo.UserByID(ctx, id)
}

// AuthUser verifies an email/password pair. Unknown email and wrong password
// produce the identical generic error so the endpoint cannot be used to
// enumerate accounts (spec FR-U3, NFR-2). Unknown emails consume a dummy
// bcrypt comparison to keep timing uniform.
func (s *TaskServiceImpl) AuthUser(ctx context.Context, email, password string) (domain.User, error) {
	user, err := s.repo.AuthUser(ctx, email)
	if err != nil {
		// Burn one bcrypt comparison so unknown emails cost the same as
		// known ones.
		_ = CheckPassword(dummyHash, password)
		return domain.User{}, domain.Invalid("invalid email or password")
	}
	if !CheckPassword(user.Password(), password) {
		return domain.User{}, domain.Invalid("invalid email or password")
	}
	return user, nil
}

// UserByAPIKey resolves a user by API key (X-API-Key auth path).
func (s *TaskServiceImpl) UserByAPIKey(ctx context.Context, key string) (domain.User, error) {
	return s.repo.UserByAPIKey(ctx, key)
}
