// Package conformance holds the backend-agnostic contract suite for the
// TaskRepository port. Any implementation (sqlite, memory, and — later —
// postgres, oracle, mongodb) must pass Repository(). This is the guard
// against SQL drift between dialects and against port-contract regressions.
//
// This file (conformance.go) is the entire package.
//
// Public API:
//   - Repository — runs the whole contract suite against a freshly created,
//     empty repository
//
// Private:
//   - scaffolding: now, mustTask, kind, assertKind, requireTask, requireIDs
//   - shared ctx
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

var ctx = context.Background()

func now(t time.Time) time.Time {
	return t.UTC()
}

// fixed times are 1+ minutes apart so that the Unix-second storage used by the
// SQL backends preserves ordering unambiguously.
func mustTask(t *testing.T, id domain.TaskID, title string, priority int, at time.Time) domain.Task {
	t.Helper()
	task, err := domain.NewTask(id, title, "", priority, nil, now(at))
	if err != nil {
		t.Fatalf("NewTask(%s): %v", id, err)
	}
	return task
}

func kind(t *testing.T, err error) domain.Kind {
	t.Helper()
	var de *domain.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *domain.DomainError, got %T: %v", err, err)
	}
	return de.Kind()
}

// assertKind fails the test unless err is a *domain.DomainError with the
// given Kind.
func assertKind(t *testing.T, err error, want domain.Kind) {
	t.Helper()
	if got := kind(t, err); got != want {
		t.Fatalf("error kind = %s, want %s (err=%v)", got, want, err)
	}
}

func requireTask(t *testing.T, got domain.Task, want domain.Task) {
	t.Helper()
	if got.ID() != want.ID() ||
		got.Title() != want.Title() ||
		got.Description() != want.Description() ||
		got.Status() != want.Status() ||
		got.Priority() != want.Priority() ||
		got.CreatedAt().Unix() != want.CreatedAt().Unix() ||
		got.UpdatedAt().Unix() != want.UpdatedAt().Unix() {
		t.Fatalf("task mismatch:\n got %+v\nwant %+v", got, want)
	}
	if (got.Deadline() == nil) != (want.Deadline() == nil) {
		t.Fatalf("deadline presence mismatch: got %v, want %v", got.Deadline(), want.Deadline())
	}
	if got.Deadline() != nil && want.Deadline() != nil && !got.Deadline().Equal(*want.Deadline()) {
		t.Fatalf("deadline mismatch: got %v, want %v", got.Deadline(), want.Deadline())
	}
}

func requireIDs(t *testing.T, got []domain.Task, want ...domain.TaskID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID() != id {
			t.Fatalf("task[%d].ID = %s, want %s", i, got[i].ID(), id)
		}
	}
}

// Repository runs the complete port contract against a fresh, empty
// repository produced by newRepo. Each subtest receives its own repository.
func Repository(t *testing.T, newRepo func(t *testing.T) repository.TaskRepository) {
	t.Helper()

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("CreateAndByID", func(t *testing.T) {
		repo := newRepo(t)
		want := mustTask(t, "t1", "First task", 3, base)
		if err := repo.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.ByID(ctx, "t1")
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		requireTask(t, got, want)
	})

	t.Run("CreateConflict", func(t *testing.T) {
		repo := newRepo(t)
		want := mustTask(t, "t1", "dup", 1, base)
		if err := repo.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		err := repo.Create(ctx, want)
		assertKind(t, err, domain.KindConflict)
	})

	t.Run("ByIDNotFound", func(t *testing.T) {
		repo := newRepo(t)
		_, err := repo.ByID(ctx, "missing")
		assertKind(t, err, domain.KindNotFound)
	})

	t.Run("DeadlineRoundTrip", func(t *testing.T) {
		repo := newRepo(t)
		deadline := base.Add(72 * time.Hour)
		task, err := domain.NewTask("d1", "dated", "", 2, &deadline, now(base))
		if err != nil {
			t.Fatalf("NewTask: %v", err)
		}
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.ByID(ctx, "d1")
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		requireTask(t, got, task)
	})

	t.Run("ClearDeadlineRoundTrip", func(t *testing.T) {
		repo := newRepo(t)
		deadline := base.Add(24 * time.Hour)
		task, err := domain.NewTask("c1", "cleared", "", 2, &deadline, now(base))
		if err != nil {
			t.Fatalf("NewTask: %v", err)
		}
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("Create: %v", err)
		}
		cleared, err := task.ClearDeadline(now(base.Add(time.Hour)))
		if err != nil {
			t.Fatalf("ClearDeadline: %v", err)
		}
		if err := repo.Update(ctx, cleared); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err := repo.ByID(ctx, "c1")
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		requireTask(t, got, cleared)
		if got.Deadline() != nil {
			t.Fatalf("deadline = %v, want nil", got.Deadline())
		}
	})

	t.Run("ListOrdering", func(t *testing.T) {
		repo := newRepo(t)
		tasks := []domain.Task{
			mustTask(t, "a", "low", 1, base.Add(4*time.Minute)),
			mustTask(t, "b", "high", 5, base.Add(2*time.Minute)),
			mustTask(t, "c", "mid", 3, base.Add(3*time.Minute)),
			mustTask(t, "d", "tie-oldest", 5, base.Add(time.Minute)),
			mustTask(t, "e", "tie-newest", 5, base.Add(5*time.Minute)),
		}
		for _, task := range tasks {
			if err := repo.Create(ctx, task); err != nil {
				t.Fatalf("Create %s: %v", task.ID(), err)
			}
		}
		// priority desc, then created_at desc; limit must not truncate.
		got, err := repo.List(ctx, repository.TaskFilter{Limit: 100})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		requireIDs(t, got, "e", "b", "d", "c", "a")
	})

	t.Run("ListStatusFilter", func(t *testing.T) {
		repo := newRepo(t)
		todo := mustTask(t, "s1", "todo", 1, base)
		if err := repo.Create(ctx, todo); err != nil {
			t.Fatalf("Create: %v", err)
		}
		inprog := mustTask(t, "s2", "in progress", 1, base.Add(time.Minute))
		moved, err := inprog.WithStatus(domain.StatusInProgress, now(base.Add(2*time.Minute)))
		if err != nil {
			t.Fatalf("WithStatus: %v", err)
		}
		if err := repo.Create(ctx, moved); err != nil {
			t.Fatalf("Create: %v", err)
		}
		status := domain.StatusInProgress
		got, err := repo.List(ctx, repository.TaskFilter{Status: &status})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		requireIDs(t, got, "s2")
	})

	t.Run("ListSearchCaseInsensitive", func(t *testing.T) {
		repo := newRepo(t)
		if err := repo.Create(ctx, mustTask(t, "m1", "Write the REPORT", 1, base)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.Create(ctx, mustTask(t, "m2", "todo", 1, base)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.List(ctx, repository.TaskFilter{Search: "report"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		requireIDs(t, got, "m1")
	})

	t.Run("ListPagination", func(t *testing.T) {
		repo := newRepo(t)
		for i := 0; i < 6; i++ {
			id := domain.TaskID(string(rune('p' + i)))
			if err := repo.Create(ctx, mustTask(t, id, "page", 1, base.Add(time.Duration(i)*time.Minute))); err != nil {
				t.Fatalf("Create %s: %v", id, err)
			}
		}
		// created_at desc: newest first.
		got, err := repo.List(ctx, repository.TaskFilter{Limit: 2, Offset: 1})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		requireIDs(t, got, "t", "s")
	})

	t.Run("CountAndCountByStatus", func(t *testing.T) {
		repo := newRepo(t)
		todo := mustTask(t, "n1", "one", 1, base)
		if err := repo.Create(ctx, todo); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.Create(ctx, mustTask(t, "n2", "two", 2, base.Add(time.Minute))); err != nil {
			t.Fatalf("Create: %v", err)
		}
		third := mustTask(t, "n3", "three", 3, base.Add(2*time.Minute))
		inprog, err := third.WithStatus(domain.StatusInProgress, now(base.Add(3*time.Minute)))
		if err != nil {
			t.Fatalf("WithStatus: %v", err)
		}
		if err := repo.Create(ctx, inprog); err != nil {
			t.Fatalf("Create: %v", err)
		}
		total, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if total != 3 {
			t.Fatalf("Count = %d, want 3", total)
		}
		todoN, err := repo.CountByStatus(ctx, domain.StatusTodo)
		if err != nil {
			t.Fatalf("CountByStatus: %v", err)
		}
		if todoN != 2 {
			t.Fatalf("CountByStatus(todo) = %d, want 2", todoN)
		}
	})

	t.Run("UpdatePersistsFields", func(t *testing.T) {
		repo := newRepo(t)
		task := mustTask(t, "u1", "original", 1, base)
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("Create: %v", err)
		}
		renamed, err := task.Rename("updated title", now(base.Add(time.Minute)))
		if err != nil {
			t.Fatalf("Rename: %v", err)
		}
		repriced, err := renamed.WithPriority(5, now(base.Add(2*time.Minute)))
		if err != nil {
			t.Fatalf("WithPriority: %v", err)
		}
		inprog, err := repriced.WithStatus(domain.StatusInProgress, now(base.Add(3*time.Minute)))
		if err != nil {
			t.Fatalf("WithStatus: %v", err)
		}
		if err := repo.Update(ctx, inprog); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err := repo.ByID(ctx, "u1")
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		requireTask(t, got, inprog)
	})

	t.Run("UpdateNotFound", func(t *testing.T) {
		repo := newRepo(t)
		missing := mustTask(t, "gone", "nope", 1, base)
		err := repo.Update(ctx, missing)
		assertKind(t, err, domain.KindNotFound)
	})

	t.Run("DeleteAndDeleteNotFound", func(t *testing.T) {
		repo := newRepo(t)
		task := mustTask(t, "x1", "delete me", 1, base)
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.Delete(ctx, "x1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repo.ByID(ctx, "x1"); !errors.As(err, new(*domain.DomainError)) {
			t.Fatalf("ByID after delete: got %v, want domain error", err)
		}
		err := repo.Delete(ctx, "x1")
		assertKind(t, err, domain.KindNotFound)
	})
}
