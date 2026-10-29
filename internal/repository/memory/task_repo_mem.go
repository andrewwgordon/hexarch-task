// Package memory implements the in-memory repository backend.
//
// The store is a plain map guarded by no locks: it is intended for tests,
// the default storage-swap demo, and single-process development — not
// concurrent production use. It is registered with the factory as
// TypeMemory and serves as the reference behavior for the conformance
// suite (`internal/repository/conformance`).
//
// Public API:
//   - Types:  TaskRepositoryMem
//   - Funcs:  NewTaskRepositoryMem
//   - Methods: the seven TaskRepository methods
//
// Private:
//   - provider (self-registration)
package memory

import (
	"context"
	"io"
	"sort"
	"strings"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// Registration: importing this package makes the memory backend available to
// the repository factory.
func init() {
	repository.Register(repository.TypeMemory, provider{})
}

// provider implements repository.Provider for the in-memory backend. It has
// no resources to release, so the closer is a no-op.
type provider struct{}

func (provider) Open(_ context.Context, _ string) (repository.TaskRepository, io.Closer, error) {
	return NewTaskRepositoryMem(), repository.NopCloser(), nil
}

// TaskRepositoryMem is an in-memory implementation of the TaskRepository
// port. It demonstrates that the application layer has no dependency on the
// storage engine.
type TaskRepositoryMem struct {
	store  map[string]domain.Task
	users  map[string]domain.User
	seeded bool
}

func NewTaskRepositoryMem() repository.TaskRepository {
	return &TaskRepositoryMem{
		store: map[string]domain.Task{},
		users: map[string]domain.User{},
	}
}

// seedOnce lazily runs the bootstrap-admin seeding (no-op after the first
// successful seed). Idempotent per spec FR-U5.
func (m *TaskRepositoryMem) seedOnce() {
	if m.seeded {
		return
	}
	m.seeded = true
	m.seed(context.Background())
}

// Create inserts a new task, returning domain.Conflict when the ID is
// already stored.
func (m *TaskRepositoryMem) Create(_ context.Context, t domain.Task) error {
	key := t.ID().String()
	if _, exists := m.store[key]; exists {
		return domain.Conflict("task " + key + " already exists")
	}
	m.store[key] = t
	return nil
}

// ByID returns the task with the given ID, or domain.NotFound.
func (m *TaskRepositoryMem) ByID(_ context.Context, id domain.TaskID) (domain.Task, error) {
	key := id.String()
	t, exists := m.store[key]
	if !exists {
		return domain.Task{}, domain.NotFound("task " + key + " not found")
	}
	return t, nil
}

// List returns the tasks matching the filter, using the effective sort/order
// and paging. Search is case-insensitive substring matching on title and
// description. A non-nil f.UserID scopes the list to that owner's tasks.
func (m *TaskRepositoryMem) List(_ context.Context, f repository.TaskFilter) ([]domain.Task, error) {
	var out []domain.Task
	for _, t := range m.store {
		if m.matchesFilter(t, f) {
			out = append(out, t)
		}
	}
	m.sortTasks(out, f)

	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	start := min(offset, len(out))
	end := min(offset+limit, len(out))
	return out[start:end], nil
}

// Count returns the number of stored tasks matching every filter field except
// paging and sorting.
func (m *TaskRepositoryMem) Count(_ context.Context, f repository.TaskFilter) (int, error) {
	n := 0
	for _, t := range m.store {
		if m.matchesFilter(t, f) {
			n++
		}
	}
	return n, nil
}

// CountByStatus returns the number of stored tasks in the given status,
// honoring the same filters as Count. The status argument takes precedence.
func (m *TaskRepositoryMem) CountByStatus(ctx context.Context, status domain.Status, f repository.TaskFilter) (int, error) {
	f.Status = &status
	f.Statuses = nil
	return m.Count(ctx, f)
}

// matchesFilter is the single in-memory predicate shared by List and Count.
func (m *TaskRepositoryMem) matchesFilter(t domain.Task, f repository.TaskFilter) bool {
	if f.UserID != nil && t.UserID() != *f.UserID {
		return false
	}
	if f.Status != nil {
		if t.Status() != *f.Status {
			return false
		}
	} else if len(f.Statuses) > 0 {
		matched := false
		for _, s := range f.Statuses {
			if t.Status() == s {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if f.Search != "" {
		needle := strings.ToLower(f.Search)
		if !strings.Contains(strings.ToLower(t.Title()), needle) &&
			!strings.Contains(strings.ToLower(t.Description()), needle) {
			return false
		}
	}
	if f.DeadlineFrom != nil || f.DeadlineTo != nil {
		d := t.Deadline()
		if d == nil {
			return false
		}
		if f.DeadlineFrom != nil && d.Before(*f.DeadlineFrom) {
			return false
		}
		if f.DeadlineTo != nil && !d.Before(*f.DeadlineTo) {
			return false
		}
	}
	return true
}

// sortTasks applies the same ordering contract as the SQL and MongoDB
// backends: deterministic tie-breakers, case-insensitive titles, and missing
// deadlines last.
func (m *TaskRepositoryMem) sortTasks(tasks []domain.Task, f repository.TaskFilter) {
	sortField, order := f.EffectiveSort()
	asc := order == repository.SortAsc
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		var cmp int
		switch sortField {
		case repository.SortCreated:
			cmp = a.CreatedAt().Compare(b.CreatedAt())
		case repository.SortUpdated:
			cmp = a.UpdatedAt().Compare(b.UpdatedAt())
		case repository.SortDeadline:
			ad, bd := a.Deadline(), b.Deadline()
			switch {
			case ad == nil && bd == nil:
				cmp = 0
			case ad == nil:
				return false // missing deadlines always last
			case bd == nil:
				return true
			default:
				cmp = ad.Compare(*bd)
			}
		case repository.SortTitle:
			cmp = strings.Compare(strings.ToLower(a.Title()), strings.ToLower(b.Title()))
		default: // priority
			cmp = a.Priority() - b.Priority()
		}
		if cmp != 0 {
			if asc {
				return cmp < 0
			}
			return cmp > 0
		}
		// Tie-breakers: created_at desc, then id asc.
		if c := a.CreatedAt().Compare(b.CreatedAt()); c != 0 {
			return c > 0
		}
		return a.ID() < b.ID()
	})
}

// Update replaces the stored task, returning domain.NotFound when the ID
// is absent.
func (m *TaskRepositoryMem) Update(_ context.Context, t domain.Task) error {
	key := t.ID().String()
	if _, exists := m.store[key]; !exists {
		return domain.NotFound("task " + key + " not found")
	}
	m.store[key] = t
	return nil
}

// Delete removes the task with the given ID, returning domain.NotFound
// when it was already absent.
func (m *TaskRepositoryMem) Delete(_ context.Context, id domain.TaskID) error {
	key := id.String()
	if _, exists := m.store[key]; !exists {
		return domain.NotFound("task " + key + " not found")
	}
	delete(m.store, key)
	return nil
}
