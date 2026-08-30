// cli_test.go exercises the CLI adapter's authentication pre-flight and
// ownership stamping against the in-memory repository (which seeds the
// bootstrap admin).
package cli_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"hexarch/internal/adapters/cli"
	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository/memory"
)

func newApp(t *testing.T) *cli.Cli {
	t.Helper()
	repo := memory.NewTaskRepositoryMem()
	svc := application.NewTaskService(repo)
	return cli.NewCli(svc)
}

func TestCLIMissingCredentials(t *testing.T) {
	t.Setenv("HEXARCH_EMAIL", "")
	t.Setenv("HEXARCH_PASSWORD", "")
	err := newApp(t).Run(context.Background(), []string{"list"})
	if err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("want auth-required error, got %v", err)
	}
}

func TestCLIAuthFailureGeneric(t *testing.T) {
	err := newApp(t).Run(context.Background(), []string{
		"list", "--email", "admin@email.com", "--password", "wrong"})
	if err == nil || err.Error() != "invalid email or password" {
		t.Fatalf("want generic auth error, got %v", err)
	}
}

func TestCLICreateListScopedToOwner(t *testing.T) {
	app := newApp(t)
	ctx := context.Background()
	admin := []string{"--email", "admin@email.com", "--password", "admin"}

	if err := app.Run(ctx, append([]string{"create"}, append(admin, "--title", "admin task")...)); err != nil {
		t.Fatalf("create as admin: %v", err)
	}
	if err := app.Run(ctx, append([]string{"list"}, admin...)); err != nil {
		t.Fatalf("list as admin: %v", err)
	}
}

func TestCLIStatsScopedToOwner(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewTaskRepositoryMem()
	svc := application.NewTaskService(repo)
	app := cli.NewCli(svc)

	// A brand-new regular user (so the bootstrap admin is seeded first).
	if _, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "u@x.com", Password: "pw"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	// Admin creates one task.
	if err := app.Run(ctx, []string{"create", "--email", "admin@email.com", "--password", "admin", "--title", "admin task"}); err != nil {
		t.Fatalf("create as admin: %v", err)
	}

	// The regular user's stats must count zero tasks — never the admin's.
	out := runCLICapture(t, app, []string{"stats", "--email", "u@x.com", "--password", "pw"})
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[1] != "0" {
		t.Errorf("regular user stats = %q, want total 0", out)
	}
	// The admin's own stats still count their own task.
	adminOut := runCLICapture(t, app, []string{"stats", "--email", "admin@email.com", "--password", "admin"})
	adminFields := strings.Fields(adminOut)
	if len(adminFields) < 2 || adminFields[1] != "1" {
		t.Errorf("admin stats = %q, want total 1", adminOut)
	}
}

// runCLICapture runs the CLI and returns its stdout.
func runCLICapture(t *testing.T, app *cli.Cli, args []string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fnerr := app.Run(context.Background(), args)
	w.Close()
	out, _ := io.ReadAll(r)
	os.Stdout = old
	if fnerr != nil {
		t.Fatalf("run %v: %v", args, fnerr)
	}
	return string(out)
}

func TestCLIWhoami(t *testing.T) {
	app := newApp(t)
	err := app.Run(context.Background(), []string{
		"whoami", "--email", "admin@email.com", "--password", "admin"})
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
}

// TestCLITaskIDOwnership guards FR-U4 on the CLI by-ID subcommands: a
// regular user cannot read or mutate another user's task, and the admin's
// task survives the attempts. The owner (and an admin) keep working access.
func TestCLITaskIDOwnership(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewTaskRepositoryMem()
	svc := application.NewTaskService(repo)
	app := cli.NewCli(svc)

	// Seeds the bootstrap admin; creates the regular user.
	if _, err := svc.CreateUser(ctx, application.CreateUserInput{Email: "u@x.com", Password: "pw"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := app.Run(ctx, []string{"create", "--email", "admin@email.com", "--password", "admin", "--title", "admin task"}); err != nil {
		t.Fatalf("create as admin: %v", err)
	}

	// Determine the admin task's ID from the admin's scoped list output.
	taskOut := runCLICapture(t, app, []string{"list", "--email", "admin@email.com", "--password", "admin"})
	fields := strings.Fields(taskOut)
	if len(fields) < 1 {
		t.Fatalf("admin list gave no rows: %q", taskOut)
	}
	taskID := fields[0]

	// The regular user cannot get, start, rename, re-prioritize, or remove
	// the admin's task — every attempt fails with the not-found error.
	for _, args := range [][]string{
		{"get", taskID},
		{"start", taskID},
		{"done", taskID},
		{"rename", taskID, "hijacked"},
		{"priority", taskID, "5"},
		{"deadline", taskID, "2026-12-31"},
		{"remove", taskID},
	} {
		full := append([]string{args[0]}, append(args[1:], "--email", "u@x.com", "--password", "pw")...)
		err := app.Run(ctx, full)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("cli %v on foreign task: err = %v, want not found", args[0], err)
		}
	}

	// The admin's task survives untouched.
	got, err := svc.GetTask(ctx, domain.TaskID(taskID), application.CallerOf(mustAdmin(t, svc)))
	if err != nil {
		t.Fatalf("reload admin task: %v", err)
	}
	if got.Title() != "admin task" || got.Status() != domain.StatusTodo {
		t.Errorf("admin task was modified: %+v", got)
	}

	// The owner performs the same operations on their own task — need a task
	// owned by the regular user first.
	if err := app.Run(ctx, []string{"create", "--email", "u@x.com", "--password", "pw", "--title", "user task"}); err != nil {
		t.Fatalf("create as user: %v", err)
	}
	ownOut := runCLICapture(t, app, []string{"list", "--email", "u@x.com", "--password", "pw"})
	ownFields := strings.Fields(ownOut)
	if len(ownFields) < 1 {
		t.Fatalf("user list gave no rows: %q", ownOut)
	}
	ownID := ownFields[0]
	if err := app.Run(ctx, []string{"start", "--email", "u@x.com", "--password", "pw", ownID}); err != nil {
		t.Fatalf("user start own task: %v", err)
	}
	if err := app.Run(ctx, []string{"done", "--email", "u@x.com", "--password", "pw", ownID}); err != nil {
		t.Fatalf("user done own task: %v", err)
	}
	if err := app.Run(ctx, []string{"remove", "--email", "u@x.com", "--password", "pw", ownID}); err != nil {
		t.Fatalf("user remove own task: %v", err)
	}
}

// mustAdmin re-authenticates the seeded bootstrap admin.
func mustAdmin(t *testing.T, svc application.TaskService) domain.User {
	t.Helper()
	u, err := svc.AuthUser(context.Background(), "admin@email.com", "admin")
	if err != nil {
		t.Fatalf("admin auth: %v", err)
	}
	return u
}
