// Package domain holds the pure business core of the hexagon: the Task
// entity, its status state machine, validation rules, and typed errors. It
// imports no other internal package and knows nothing about databases,
// terminals, or network protocols, so it can be tested and reused anywhere.
//
// This file (task.go) defines the Task value object: the TaskID and Status
// types, the legal status transitions, the creation/hydration factories, and
// the immutable "return a copy" business methods. A Task is owned by exactly
// one User (1-to-many User→Task); the owner is stamped at creation and never
// changes.
//
// Public API:
//   - Types:   TaskID, Status, Task, UserID, User, MinPriority, MaxPriority
//   - Functions: NewTask, HydrateTask
//   - Methods: value accessors; CanEdit; and the copy-returning mutators
//     WithStatus, Rename, WithPriority, WithDeadline, ClearDeadline
//
// Unexported:
//   - allowedTransitions (the transition table), Task.cannotEdit
package domain

import (
	"fmt"
	"time"
)

// TaskID is the domain-level identity of a task. It is opaque to the core;
// how IDs are minted is an application concern (see idgen.go).
type TaskID string

// String returns the ID as a plain string.
func (id TaskID) String() string { return string(id) }

// Status is the lifecycle state machine of a task.
type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusArchived   Status = "archived"
)

// String returns the status as its canonical token (e.g. "in_progress").
func (s Status) String() string { return string(s) }

// AllowedTransitions is the single source of truth for legal transitions.
var allowedTransitions = map[Status][]Status{
	StatusTodo:       {StatusInProgress},
	StatusInProgress: {StatusTodo, StatusDone},
	StatusDone:       {StatusArchived},
	StatusArchived:   {},
}

const (
	// MinPriority is the lowest legal priority value (lowest urgency).
	MinPriority = 0
	// MaxPriority is the highest legal priority value (highest urgency).
	MaxPriority = 5
)

// Task is the core domain entity (an immutable-style value object). It has
// no dependency on any storage engine, network, or UI framework.
type Task struct {
	id        TaskID
	userid    UserID
	title     string
	desc      string
	status    Status
	priority  int
	deadline  *time.Time
	createdAt time.Time
	updatedAt time.Time
}

// ---- Accessors ----
//
// Each accessor returns an immutable snapshot of the task's value. Because
// Task exposes no pointers to mutable state, any layer can read a task
// safely without copying the whole struct.

// ID returns the task's opaque identity.
func (t Task) ID() TaskID { return t.id }

// UserID returns the owner's identity (always non-empty; every task has an
// owner).
func (t Task) UserID() UserID { return t.userid }

// Title returns the task's title.
func (t Task) Title() string { return t.title }

// Description returns the task's optional description ("" when unset).
func (t Task) Description() string { return t.desc }

// Status returns the task's current lifecycle status.
func (t Task) Status() Status { return t.status }

// Priority returns the task's priority, in the inclusive range
// [MinPriority, MaxPriority].
func (t Task) Priority() int { return t.priority }

// Deadline returns the task's deadline, or nil when no deadline is set.
func (t Task) Deadline() *time.Time { return t.deadline }

// CreatedAt returns the time the task was created.
func (t Task) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt returns the time the task was last modified.
func (t Task) UpdatedAt() time.Time { return t.updatedAt }

// ---- Factories ----

// NewTask builds a brand-new task. It is used by the application layer when
// creating a task; status is always "todo". The owner (userid) is required —
// every task belongs to exactly one user.
func NewTask(id TaskID, userid UserID, title, description string, priority int, deadline *time.Time, now time.Time) (Task, error) {
	if userid == "" {
		return Task{}, Invalid("task must have an owner")
	}
	if title == "" {
		return Task{}, Invalid("title must not be empty")
	}
	if priority < MinPriority || priority > MaxPriority {
		return Task{}, Invalid(fmt.Sprintf("priority must be between %d and %d", MinPriority, MaxPriority))
	}
	return Task{
		id: id, userid: userid, title: title, desc: description,
		status: StatusTodo, priority: priority, deadline: deadline,
		createdAt: now, updatedAt: now,
	}, nil
}

// HydrateTask rebuilds an existing task from a durable representation. It is
// the inverse of NewTask and validates the stored status string.
func HydrateTask(id TaskID, userid UserID, title, description string, status Status, priority int, deadline *time.Time, createdAt, updatedAt time.Time) (Task, error) {
	if userid == "" {
		return Task{}, Invalid("corrupted record: empty owner")
	}
	if title == "" {
		return Task{}, Invalid("corrupted record: empty title")
	}
	return Task{
		id: id, userid: userid, title: title, desc: description,
		status: status, priority: priority, deadline: deadline,
		createdAt: createdAt, updatedAt: updatedAt,
	}, nil
}

// ---- Business methods ----

// WithStatus returns a copy with the given status if the transition is
// lawful; otherwise it returns a Conflict error.
func (t Task) WithStatus(next Status, now time.Time) (Task, error) {
	if t.status == next {
		return t, nil
	}
	for _, allowed := range allowedTransitions[t.status] {
		if allowed == next {
			t.status = next
			t.updatedAt = now
			return t, nil
		}
	}
	return Task{}, Conflict(fmt.Sprintf("cannot transition from %s to %s", t.status, next))
}

// cannotEdit reports whether the task is in a terminal state (done or
// archived). Terminal tasks reject further edits; only status moves that
// leave the terminal state are handled by WithStatus.
func (t Task) cannotEdit() bool {
	return (t.status == StatusDone || t.status == StatusArchived)
}

func (t Task) CanEdit() bool {
	return !t.cannotEdit()
}

// Rename returns a copy with a new non-empty title.
func (t Task) Rename(title string, now time.Time) (Task, error) {
	if t.cannotEdit() {
		return t, Invalid("invalid operation on status")
	}
	if title == "" {
		return t, Invalid("title must not be empty")
	}
	t.title = title
	t.updatedAt = now
	return t, nil
}

// WithPriority returns a copy with a validated priority.
func (t Task) WithPriority(priority int, now time.Time) (Task, error) {
	if t.cannotEdit() {
		return t, Invalid("invalid operation on status")
	}
	if priority < MinPriority || priority > MaxPriority {
		return t, Invalid(fmt.Sprintf("priority must be between %d and %d", MinPriority, MaxPriority))
	}
	t.priority = priority
	t.updatedAt = now
	return t, nil
}

// WithDeadline sets a deadline.
func (t Task) WithDeadline(deadline time.Time, now time.Time) (Task, error) {
	if t.cannotEdit() {
		return t, Invalid("invalid operation on status")
	}
	t.deadline = &deadline
	t.updatedAt = now
	return t, nil
}

// ClearDeadline removes any deadline.
func (t Task) ClearDeadline(now time.Time) (Task, error) {
	if t.cannotEdit() {
		return t, Invalid("invalid operation on status")
	}
	t.deadline = nil
	t.updatedAt = now
	return t, nil
}
