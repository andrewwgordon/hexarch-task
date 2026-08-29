// service_test.go tests the application use cases from an external test
// package, using the in-memory repository, a deterministic ID generator, and
// a fixed clock. No database or network is involved, so the tests are fast
// and fully reproducible.
package application_test

import (
	"context"
	"testing"
	"time"

	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository/memory"
)

func TestCreateAndCompleteLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repo := memory.NewTaskRepositoryMem()
	svc := application.NewTaskServiceWith(
		repo,
		func() (domain.TaskID, error) { return domain.TaskID("fixed-id"), nil },
		func() time.Time { return now },
	)

	task, err := svc.CreateTask(ctx, application.CreateTaskInput{
		Title:       "Implement hexagon",
		Description: "a reference",
		Priority:    4,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if task.Status() != domain.StatusTodo {
		t.Fatalf("new task should be todo, got %s", task.Status())
	}
	if task.Priority() != 4 {
		t.Fatalf("expected priority 4, got %d", task.Priority())
	}

	started, err := svc.ChangeStatus(ctx, task.ID(), domain.StatusInProgress)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if started.Status() != domain.StatusInProgress {
		t.Fatalf("expected in_progress, got %s", started.Status())
	}

	done, err := svc.ChangeStatus(ctx, task.ID(), domain.StatusDone)
	if err != nil {
		t.Fatalf("done failed: %v", err)
	}
	if done.Status() != domain.StatusDone {
		t.Fatalf("expected done, got %s", done.Status())
	}

	stats, err := svc.Stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.Total != 1 {
		t.Errorf("expected 1 total task, got %d", stats.Total)
	}
	if stats.Done != 1 {
		t.Errorf("expected 1 done task, got %d", stats.Done)
	}
}
