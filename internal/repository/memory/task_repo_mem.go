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
	store map[string]domain.Task
}

func NewTaskRepositoryMem() repository.TaskRepository {
	return &TaskRepositoryMem{store: map[string]domain.Task{}}
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

// List returns the tasks matching the filter, ordered by priority
// descending, then created_at descending, with paging applied. Search is
// case-insensitive substring matching on title and description.
func (m *TaskRepositoryMem) List(_ context.Context, f repository.TaskFilter) ([]domain.Task, error) {
	var out []domain.Task
	for _, t := range m.store {
		if f.Status != nil && t.Status() != *f.Status {
			continue
		}
		if f.Search != "" {
			needle := strings.ToLower(f.Search)
			if !strings.Contains(strings.ToLower(t.Title()), needle) &&
				!strings.Contains(strings.ToLower(t.Description()), needle) {
				continue
			}
		}
		out = append(out, t)
	}

	// Same ordering contract as the SQLite adapter:
	// priority desc, then created_at desc.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority() != b.Priority() {
			return a.Priority() > b.Priority()
		}
		return a.CreatedAt().Compare(b.CreatedAt()) > 0
	})

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

// Count returns the total number of stored tasks.
func (m *TaskRepositoryMem) Count(_ context.Context) (int, error) {
	return len(m.store), nil
}

// CountByStatus returns the number of stored tasks in the given status.
func (m *TaskRepositoryMem) CountByStatus(_ context.Context, status domain.Status) (int, error) {
	n := 0
	for _, t := range m.store {
		if t.Status() == status {
			n++
		}
	}
	return n, nil
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
