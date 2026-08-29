// domain_test.go exercises the domain core from an external test package, so
// it can only rely on the exported API — exactly the way every other layer
// depends on the domain.
//
// Coverage:
//   - NewTask validation: empty titles and out-of-range priorities are rejected.
//   - Status state machine: illegal jumps (todo -> done) are rejected while
//     legal transitions (todo -> in_progress -> done) succeed.
//   - Rename rejects empty titles.
package domain_test

import (
	"testing"
	"time"

	"hexarch/internal/domain"
)

func TestCreateRejectsEmptyTitle(t *testing.T) {
	_, err := domain.NewTask(domain.TaskID("id"), "", "d", 0, nil, time.Now())
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestCreateRejectsOutOfRangePriority(t *testing.T) {
	_, err := domain.NewTask(domain.TaskID("id"), "ok", "d", 9, nil, time.Now())
	if err == nil {
		t.Fatal("expected error for priority 9")
	}
}

func TestStateMachineRejectsIllegalJump(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	task, err := domain.NewTask(domain.TaskID("id1"), "fix bug", "d", 0, nil, now)
	if err != nil {
		t.Fatalf("unexpected creation error: %v", err)
	}

	// todo -> done is illegal
	if _, err := task.WithStatus(domain.StatusDone, now); err == nil {
		t.Fatal("expected todo->done transition to be rejected")
	}

	// todo -> in_progress is legal
	inProgress, err := task.WithStatus(domain.StatusInProgress, now)
	if err != nil {
		t.Fatalf("todo->in_progress should be allowed: %v", err)
	}
	if inProgress.Status() != domain.StatusInProgress {
		t.Fatalf("expected status in_progress, got %s", inProgress.Status())
	}

	// in_progress -> done is legal
	done, err := inProgress.WithStatus(domain.StatusDone, now)
	if err != nil {
		t.Fatalf("in_progress->done should be allowed: %v", err)
	}
	if done.Status() != domain.StatusDone {
		t.Fatalf("expected status done, got %s", done.Status())
	}
}

func TestRenameRejectsEmptyTitle(t *testing.T) {
	now := time.Now()
	task, err := domain.NewTask(domain.TaskID("id2"), "keep me", "d", 0, nil, now)
	if err != nil {
		t.Fatalf("unexpected creation error: %v", err)
	}
	if _, err := task.Rename("", now); err == nil {
		t.Fatal("expected rename to empty title to be rejected")
	}
}
