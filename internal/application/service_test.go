// service_test.go tests the application use cases from an external test
// package, using the in-memory repository, a deterministic ID generator, and
// a fixed clock. No database or network is involved, so the tests are fast
// and fully reproducible.
package application_test

import (
	"context"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
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

	// The memory repo seeds one bootstrap admin; its tasks are owned by it.
	seeded, err := svc.AuthUser(ctx, "admin@email.com", "admin")
	if err != nil {
		t.Fatalf("seeded admin auth failed: %v", err)
	}

	task, err := svc.CreateTask(ctx, application.CreateTaskInput{
		UserID:      seeded.ID(),
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

	started, err := svc.ChangeStatus(ctx, task.ID(), domain.StatusInProgress, application.CallerOf(seeded))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if started.Status() != domain.StatusInProgress {
		t.Fatalf("expected in_progress, got %s", started.Status())
	}

	done, err := svc.ChangeStatus(ctx, task.ID(), domain.StatusDone, application.CallerOf(seeded))
	if err != nil {
		t.Fatalf("done failed: %v", err)
	}
	if done.Status() != domain.StatusDone {
		t.Fatalf("expected done, got %s", done.Status())
	}

	stats, err := svc.Stats(ctx, repository.TaskFilter{})
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

// ---- idgen (phase 3 of docs/auth-plan.md) ----

func TestRandomUserIDFormat(t *testing.T) {
	id, err := application.RandomUserID()
	if err != nil {
		t.Fatalf("RandomUserID: %v", err)
	}
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !re.MatchString(string(id)) {
		t.Errorf("RandomUserID = %q, want UUIDv4 format", id)
	}
}

func TestRandomAPIKey(t *testing.T) {
	a, err := application.RandomAPIKey()
	if err != nil {
		t.Fatalf("RandomAPIKey: %v", err)
	}
	b, err := application.RandomAPIKey()
	if err != nil {
		t.Fatalf("RandomAPIKey: %v", err)
	}
	if len(a) != 64 {
		t.Errorf("len = %d, want 64 hex chars", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("apikey not hex: %v", err)
	}
	if a == b {
		t.Error("two apikeys are identical")
	}
}

// ---- phase 5 of docs/auth-plan.md: user use cases ----

func newUserService(t *testing.T) (application.TaskService, repository.TaskRepository) {
	t.Helper()
	repo := memory.NewTaskRepositoryMem()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	var useq int
	svc := application.NewTaskServiceWith(repo,
		func() (domain.TaskID, error) { return domain.TaskID("t"), nil },
		func() time.Time { return now })
	// Inject deterministic user/apikey generators via the exported API is not
	// available; the random ones are fine for these assertions.
	_ = useq
	return svc, repo
}

func TestServiceCreateUserAndAuth(t *testing.T) {
	svc, _ := newUserService(t)
	ctx := context.Background()

	u, err := svc.CreateUser(ctx, application.CreateUserInput{
		Email: "Alice@Example.COM", Password: "secret123", IsAdmin: false,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Email() != "alice@example.com" {
		t.Errorf("email not lower-cased: %q", u.Email())
	}
	if u.Password() == "secret123" {
		t.Error("stored password must be a hash, not clear text")
	}
	if u.APIKey() == "" {
		t.Error("apikey must be minted at creation")
	}

	got, err := svc.AuthUser(ctx, "ALICE@example.com", "secret123")
	if err != nil {
		t.Fatalf("AuthUser: %v", err)
	}
	if got.ID() != u.ID() {
		t.Errorf("AuthUser id = %s, want %s", got.ID(), u.ID())
	}
}

func TestServiceAuthGenericFailure(t *testing.T) {
	svc, _ := newUserService(t)
	ctx := context.Background()
	if _, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "u@x.com", Password: "pw"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	wrongPw, err1 := svc.AuthUser(ctx, "u@x.com", "wrong")
	unknown, err2 := svc.AuthUser(ctx, "nobody@x.com", "pw")
	if err1 == nil || err2 == nil {
		t.Fatalf("expected both failures, got %v / %v", err1, err2)
	}
	if err1.Error() != err2.Error() || err1.Error() != "invalid email or password" {
		t.Errorf("auth errors differ or leak details: %q vs %q", err1, err2)
	}
	if wrongPw.ID() != "" || unknown.ID() != "" {
		t.Error("failed auth must not return a user")
	}
}

func TestServiceCreateUserDuplicateEmailConflict(t *testing.T) {
	svc, _ := newUserService(t)
	ctx := context.Background()
	if _, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "dup@x.com", Password: "pw"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "DUP@x.com", Password: "other"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want conflict, got %v", err)
	}
}

func TestServiceCreateTaskRequiresOwner(t *testing.T) {
	svc, _ := newUserService(t)
	ctx := context.Background()
	if _, err := svc.CreateTask(ctx, application.CreateTaskInput{Title: "orphan"}); err == nil {
		t.Fatal("expected error creating a task without an owner")
	}
	// Unknown owner also rejected.
	if _, err := svc.CreateTask(ctx, application.CreateTaskInput{UserID: "no-such-user", Title: "x"}); err == nil {
		t.Fatal("expected error for unknown owner")
	}
}

func TestServiceTaskIsolationBetweenUsers(t *testing.T) {
	svc, _ := newUserService(t)
	ctx := context.Background()

	a, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "a@x.com", Password: "pw"})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "b@x.com", Password: "pw"})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	created, err := svc.CreateTask(ctx, application.CreateTaskInput{UserID: a.ID(), Title: "a's task"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// B does not see A's task.
	bTasks, err := svc.ListTasks(ctx, repository.TaskFilter{UserID: &[]domain.UserID{b.ID()}[0]})
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if len(bTasks) != 0 {
		t.Errorf("b sees %d tasks, want 0", len(bTasks))
	}
	// A sees exactly their own.
	aTasks, err := svc.ListTasks(ctx, repository.TaskFilter{UserID: &[]domain.UserID{a.ID()}[0]})
	if err != nil {
		t.Fatalf("list a: %v", err)
	}
	if len(aTasks) != 1 {
		t.Errorf("a sees %d tasks, want 1", len(aTasks))
	}

	// Stats leak the same way when unscoped — regular users must pass their
	// own ID: B's summary counts zero, A's counts exactly their one task.
	bStats, err := svc.Stats(ctx, repository.TaskFilter{UserID: &[]domain.UserID{b.ID()}[0]})
	if err != nil {
		t.Fatalf("stats b: %v", err)
	}
	if bStats.Total != 0 {
		t.Errorf("b stats total = %d, want 0", bStats.Total)
	}
	aStats, err := svc.Stats(ctx, repository.TaskFilter{UserID: &[]domain.UserID{a.ID()}[0]})
	if err != nil {
		t.Fatalf("stats a: %v", err)
	}
	if aStats.Total != 1 || aStats.Todo != 1 {
		t.Errorf("a stats = %+v, want total=1 todo=1", aStats)
	}

	// Direct by-ID access is ownership-checked at the service level (FR-U4):
	// B cannot read, mutate, or delete A's task — the attempt surfaces as
	// the same NotFound as a missing task. A (the owner) and an admin can.
	taskID := created.ID()
	bCaller := application.CallerOf(b)
	aCaller := application.CallerOf(a)

	if _, err := svc.GetTask(ctx, taskID, bCaller); !strings.Contains(err.Error(), "not found") {
		t.Errorf("b get foreign task: err = %v, want not found", err)
	}
	if _, err := svc.RenameTask(ctx, taskID, "hijacked", bCaller); !strings.Contains(err.Error(), "not found") {
		t.Errorf("b rename foreign task: err = %v, want not found", err)
	}
	if _, err := svc.ChangeStatus(ctx, taskID, domain.StatusInProgress, bCaller); !strings.Contains(err.Error(), "not found") {
		t.Errorf("b status-change foreign task: err = %v, want not found", err)
	}
	if _, err := svc.ChangePriority(ctx, taskID, 5, bCaller); !strings.Contains(err.Error(), "not found") {
		t.Errorf("b priority-change foreign task: err = %v, want not found", err)
	}
	if _, err := svc.SetDeadline(ctx, taskID, time.Now(), bCaller); !strings.Contains(err.Error(), "not found") {
		t.Errorf("b set-deadline foreign task: err = %v, want not found", err)
	}
	if _, err := svc.ClearDeadline(ctx, taskID, bCaller); !strings.Contains(err.Error(), "not found") {
		t.Errorf("b clear-deadline foreign task: err = %v, want not found", err)
	}
	if err := svc.DeleteTask(ctx, taskID, bCaller); !strings.Contains(err.Error(), "not found") {
		t.Errorf("b delete foreign task: err = %v, want not found", err)
	}

	// The task is untouched by B's attempts, and A (the owner) still sees it.
	if got, err := svc.GetTask(ctx, taskID, aCaller); err != nil || got.Title() != "a's task" {
		t.Errorf("a get own task after b's attempts: err=%v task=%+v", err, got)
	}
	// An admin can read and modify any task.
	admin, err := svc.AuthUser(ctx, "admin@email.com", "admin")
	if err != nil {
		t.Fatalf("seeded admin auth: %v", err)
	}
	if _, err := svc.GetTask(ctx, taskID, application.CallerOf(admin)); err != nil {
		t.Errorf("admin get foreign task: %v", err)
	}
	if _, err := svc.RenameTask(ctx, taskID, "admin renamed", application.CallerOf(admin)); err != nil {
		t.Errorf("admin rename foreign task: %v", err)
	}
}

func TestServiceLastAdminGuards(t *testing.T) {
	svc, _ := newUserService(t)
	ctx := context.Background()

	admin, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "admin@x.com", Password: "pw", IsAdmin: true})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	// The memory backend seeds its own bootstrap admin, so two admins exist.
	// Demote/delete every admin except `admin` and then expect the guards on
	// the last one.
	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	demote := false
	for _, u := range users {
		if u.IsAdmin() && u.ID() != admin.ID() {
			if _, err := svc.UpdateUser(ctx, u.ID(), application.UpdateUserInput{IsAdmin: &demote}); err != nil {
				t.Fatalf("demote seeded admin: %v", err)
			}
		}
	}
	// Last admin cannot be demoted.
	if _, err := svc.UpdateUser(ctx, admin.ID(), application.UpdateUserInput{IsAdmin: &demote}); err == nil {
		t.Fatal("expected conflict demoting the last admin")
	}
	// Last admin cannot be deleted.
	if err := svc.DeleteUser(ctx, admin.ID()); err == nil {
		t.Fatal("expected conflict deleting the last admin")
	}

	// Once a second admin exists, demote and delete the first admin.
	second, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "admin2@x.com", Password: "pw", IsAdmin: true})
	if err != nil {
		t.Fatalf("create second admin: %v", err)
	}
	if _, err := svc.UpdateUser(ctx, admin.ID(), application.UpdateUserInput{IsAdmin: &demote}); err != nil {
		t.Fatalf("demote with second admin: %v", err)
	}
	if err := svc.DeleteUser(ctx, admin.ID()); err != nil {
		t.Fatalf("delete demoted user: %v", err)
	}
	if _, err := svc.UserByID(ctx, second.ID()); err != nil {
		t.Fatalf("second admin must remain: %v", err)
	}
}

func TestServiceUpdateUserPasswordRehash(t *testing.T) {
	svc, _ := newUserService(t)
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "u@x.com", Password: "old"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newPw := "new"
	if _, err := svc.UpdateUser(ctx, u.ID(), application.UpdateUserInput{Password: &newPw}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := svc.AuthUser(ctx, "u@x.com", "new"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
	if _, err := svc.AuthUser(ctx, "u@x.com", "old"); err == nil {
		t.Error("old password still accepted after rehash")
	}
}
