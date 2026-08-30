// Package cli implements the command-line inbound adapter of the hexagon.
// It parses argv into options and positionals, calls the application
// service, and formats domain results as human-readable text. It is one of
// three inbound adapters sharing the same TaskService.
//
// This file (cli.go) contains the Cli adapter: subcommand dispatch (Run),
// the per-subcommand handlers, and the usage text. Every task subcommand
// requires --email/--password (or HEXARCH_EMAIL/HEXARCH_PASSWORD) and calls
// AuthUser before performing work (spec docs/auth.md FR-A1).
//
// Public API:
//   - Types:  Cli
//   - Funcs:  NewCli
//   - Methods: Cli.Run
//
// Private:
//   - handlers: help, create, list, get, rename, changeStatus, priority,
//     deadline, remove, stats, whoami
//   - auth:    authenticate (pre-flight over all subcommands)
//   - parsing:  parseStatus (token -> domain.Status)
package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"hexarch/internal/adapters"
	"hexarch/internal/application"
	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// Cli is the inbound adapter: it parses argv, calls the application service,
// and formats results back to the user. It depends only on the application
// boundary, never on a concrete storage engine.
type Cli struct {
	*adapters.AppBase
}

func NewCli(svc application.TaskService) *Cli {
	return &Cli{AppBase: adapters.NewAppBase("cli", svc)}
}

// Run dispatches the first token as a subcommand. `help` is the only
// unauthenticated subcommand; everything else authenticates first.
func (c *Cli) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		c.help()
		return nil
	}
	command := args[0]
	rest := args[1:]

	switch command {
	case "help", "-h", "--help":
		c.help()
		return nil
	}

	// Authentication pre-flight (spec FR-A1): --email/--password with env
	// fallbacks, then AuthUser. The authenticated identity scopes every
	// subsequent task operation to that user's own tasks. authenticate also
	// strips the auth options from the tail, so by-ID subcommands (get,
	// rename, start, done, ...) see only their positionals.
	user, rest, err := c.authenticate(ctx, rest)
	if err != nil {
		return err
	}

	switch command {
	case "whoami":
		role := "user"
		if user.IsAdmin() {
			role = "admin"
		}
		fmt.Printf("%s (%s)\n", user.Email(), role)
		return nil
	case "create":
		return c.create(ctx, user, rest)
	case "list":
		return c.list(ctx, user, rest)
	case "get":
		return c.get(ctx, user, rest)
	case "rename":
		return c.rename(ctx, user, rest)
	case "start":
		return c.changeStatus(ctx, user, rest, domain.StatusInProgress)
	case "done":
		return c.changeStatus(ctx, user, rest, domain.StatusDone)
	case "priority":
		return c.priority(ctx, user, rest)
	case "deadline":
		return c.deadline(ctx, user, rest)
	case "remove":
		return c.remove(ctx, user, rest)
	case "stats":
		return c.stats(ctx, user, rest)
	default:
		return cliError{msg: fmt.Sprintf("unknown command %q; try 'help'", command)}
	}
}

// authenticate resolves --email/--password (env fallbacks HEXARCH_EMAIL /
// HEXARCH_PASSWORD), strips those options from the tail, and verifies the
// credentials via AuthUser. Failure returns the generic message; it never
// leaks whether the email or the password was wrong (spec NFR-2). The
// returned tail keeps every other token (positionals and unrelated options
// like --title) so subcommand handlers see exactly the arguments they
// documented.
func (c *Cli) authenticate(ctx context.Context, args []string) (domain.User, []string, error) {
	parsed, perr := parseArgs(args)
	if perr != nil {
		return domain.User{}, args, perr
	}
	email := getOpt(parsed.options, "email", os.Getenv("HEXARCH_EMAIL"))
	password := getOpt(parsed.options, "password", os.Getenv("HEXARCH_PASSWORD"))
	if email == "" || password == "" {
		return domain.User{}, args, cliError{msg: "authentication required: pass --email and --password (or set HEXARCH_EMAIL / HEXARCH_PASSWORD)"}
	}
	user, err := c.Svc.AuthUser(ctx, email, password)
	if err != nil {
		return domain.User{}, args, domain.Invalid("invalid email or password")
	}
	return user, stripAuthOptions(args), nil
}

// stripAuthOptions removes the --email / --password tokens (both "--key v"
// and "--key=v" forms) and their values from a raw argument tail.
func stripAuthOptions(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--email" || tok == "--password" {
			i++ // consume the option's value
			continue
		}
		if strings.HasPrefix(tok, "--email=") || strings.HasPrefix(tok, "--password=") {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// help prints the subcommand usage text.
func (c *Cli) help() {
	fmt.Println("usage:")
	fmt.Println("  Auth is required for every subcommand except help:")
	fmt.Println("    --email E --password P   (or HEXARCH_EMAIL / HEXARCH_PASSWORD)")
	fmt.Println("  create    --title T [--desc D] [--priority N] [--deadline 2026-08-30]")
	fmt.Println("  list      [--status todo|in_progress|done|archived] [--search q] [--limit n] [--offset n]")
	fmt.Println("  get       <id>")
	fmt.Println("  rename    <id> \"new title\"")
	fmt.Println("  priority  <id> <n>")
	fmt.Println("  start     <id>      (mark in_progress)")
	fmt.Println("  done      <id>      (mark done)")
	fmt.Println("  deadline  <id> YYYY-MM-DD|done")
	fmt.Println("  remove    <id>")
	fmt.Println("  stats")
	fmt.Println("  whoami              (print the authenticated user)")
}

// create handles the `create` subcommand: parses --title/--desc/--priority/
// --deadline options, calls CreateTask owned by the authenticated user, and
// reports the new task.
func (c *Cli) create(ctx context.Context, user domain.User, args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}
	title, err := requiredOpt(opts.options, "title", "required")
	if err != nil {
		return err
	}
	priority, err := parseIntOpt(opts.options, "priority", 0)
	if err != nil {
		return err
	}

	var deadline *time.Time
	if hasOpt(opts.options, "deadline") {
		t, perr := time.Parse(time.DateOnly, opts.options["deadline"])
		if perr != nil {
			return domain.Invalid("deadline must look like 2026-08-01")
		}
		deadline = &t
	}

	task, err := c.Svc.CreateTask(ctx, application.CreateTaskInput{
		UserID:      user.ID(),
		Title:       title,
		Description: getOpt(opts.options, "desc", ""),
		Priority:    priority,
		Deadline:    deadline,
	})
	if err != nil {
		return err
	}
	fmt.Printf("created %s: %s (%s)\n", task.ID(), task.Title(), task.Status())
	return nil
}

// list handles the `list` subcommand: builds a TaskFilter from the
// --status/--search/--limit/--offset options — scoped to the authenticated
// user's own tasks (CLI scope decision, spec docs/auth.md) — queries the
// service, and prints one line per task.
func (c *Cli) list(ctx context.Context, user domain.User, args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	filter := repository.DefaultTaskFilter()
	uid := user.ID()
	filter.UserID = &uid
	if hasOpt(opts.options, "status") {
		st, perr := parseStatus(opts.options["status"])
		if perr != nil {
			return perr
		}
		filter.Status = &st
	}
	if hasOpt(opts.options, "search") {
		filter.Search = opts.options["search"]
	}
	if n, perr := parseIntOpt(opts.options, "limit", 100); perr != nil {
		return perr
	} else {
		filter.Limit = n
	}
	if n, perr := parseIntOpt(opts.options, "offset", 0); perr != nil {
		return perr
	} else {
		filter.Offset = n
	}

	tasks, err := c.Svc.ListTasks(ctx, filter)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("(no tasks)")
		return nil
	}
	for _, t := range tasks {
		fmt.Printf("%-36s  %-20s  p%d  %s\n", t.ID(), t.Title(), t.Priority(), t.Status())
	}
	return nil
}

// get handles the `get` subcommand: prints every field of one task. Caller
// ownership is enforced by the service (FR-U4).
func (c *Cli) get(ctx context.Context, user domain.User, args []string) error {
	if len(args) != 1 {
		return domain.Invalid("usage: get <id>")
	}
	t, err := c.Svc.GetTask(ctx, domain.TaskID(args[0]), application.CallerOf(user))
	if err != nil {
		return err
	}
	fmt.Printf("id:        %s\n", t.ID())
	fmt.Printf("title:     %s\n", t.Title())
	fmt.Printf("desc:      %s\n", t.Description())
	fmt.Printf("status:    %s\n", t.Status())
	fmt.Printf("priority:  %d\n", t.Priority())
	if d := t.Deadline(); d != nil {
		fmt.Printf("deadline:  %s\n", d.Format(time.DateOnly))
	} else {
		fmt.Println("deadline:  none")
	}
	fmt.Printf("created:   %s\n", t.CreatedAt().String())
	fmt.Printf("updated:   %s\n", t.UpdatedAt().String())
	return nil
}

// rename handles the `rename` subcommand: sets a new title. Caller
// ownership is enforced by the service (FR-U4).
func (c *Cli) rename(ctx context.Context, user domain.User, args []string) error {
	if len(args) != 2 {
		return domain.Invalid(`usage: rename <id> "new title"`)
	}
	t, err := c.Svc.RenameTask(ctx, domain.TaskID(args[0]), args[1], application.CallerOf(user))
	if err != nil {
		return err
	}
	fmt.Printf("renamed to: %s\n", t.Title())
	return nil
}

// changeStatus backs the `start` and `done` subcommands: it moves a task
// through the domain state machine (todo -> in_progress, in_progress ->
// done). Caller ownership is enforced by the service (FR-U4).
func (c *Cli) changeStatus(ctx context.Context, user domain.User, args []string, status domain.Status) error {
	if len(args) != 1 {
		return domain.Invalid("usage: start|done <id>")
	}
	t, err := c.Svc.ChangeStatus(ctx, domain.TaskID(args[0]), status, application.CallerOf(user))
	if err != nil {
		return err
	}
	fmt.Printf("%s is now %s\n", t.ID(), t.Status())
	return nil
}

// priority handles the `priority` subcommand: re-prioritizes a task. Caller
// ownership is enforced by the service (FR-U4).
func (c *Cli) priority(ctx context.Context, user domain.User, args []string) error {
	if len(args) != 2 {
		return domain.Invalid("usage: priority <id> <n>")
	}
	n, err := strconv.Atoi(args[1])
	if err != nil {
		return domain.Invalid("not an integer: " + args[1])
	}
	t, err := c.Svc.ChangePriority(ctx, domain.TaskID(args[0]), n, application.CallerOf(user))
	if err != nil {
		return err
	}
	fmt.Printf("%s priority -> %d\n", t.ID(), t.Priority())
	return nil
}

// deadline handles the `deadline` subcommand: sets a YYYY-MM-DD deadline,
// or clears it when the argument is the literal "done". Caller ownership is
// enforced by the service (FR-U4).
func (c *Cli) deadline(ctx context.Context, user domain.User, args []string) error {
	if len(args) != 2 {
		return domain.Invalid("usage: deadline <id> YYYY-MM-DD|done")
	}
	if args[1] == "done" {
		t, err := c.Svc.ClearDeadline(ctx, domain.TaskID(args[0]), application.CallerOf(user))
		if err != nil {
			return err
		}
		fmt.Printf("%s deadline cleared\n", t.ID())
		return nil
	}
	d, err := time.Parse(time.DateOnly, args[1])
	if err != nil {
		return domain.Invalid("deadline must look like 2026-08-01")
	}
	t, err := c.Svc.SetDeadline(ctx, domain.TaskID(args[0]), d, application.CallerOf(user))
	if err != nil {
		return err
	}
	fmt.Printf("%s deadline -> %s\n", t.ID(), d.Format(time.DateOnly))
	return nil
}

// remove handles the `remove` subcommand: deletes a task by ID. Caller
// ownership is enforced by the service (FR-U4).
func (c *Cli) remove(ctx context.Context, user domain.User, args []string) error {
	if len(args) != 1 {
		return domain.Invalid("usage: remove <id>")
	}
	if err := c.Svc.DeleteTask(ctx, domain.TaskID(args[0]), application.CallerOf(user)); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}

// stats handles the `stats` subcommand: prints the per-status counts,
// scoped to the authenticated user's own tasks (same scope decision as
// `list`, spec docs/auth.md).
func (c *Cli) stats(ctx context.Context, user domain.User, _ []string) error {
	filter := repository.DefaultTaskFilter()
	uid := user.ID()
	filter.UserID = &uid
	s, err := c.Svc.Stats(ctx, filter)
	if err != nil {
		return err
	}
	fmt.Printf("total:       %d\n", s.Total)
	fmt.Printf("todo:        %d\n", s.Todo)
	fmt.Printf("in progress: %d\n", s.InProgress)
	fmt.Printf("done:        %d\n", s.Done)
	fmt.Printf("archived:    %d\n", s.Archived)
	return nil
}

// parseStatus maps a CLI status token onto a domain.Status.
func parseStatus(s string) (domain.Status, error) {
	switch s {
	case "todo":
		return domain.StatusTodo, nil
	case "in_progress":
		return domain.StatusInProgress, nil
	case "done":
		return domain.StatusDone, nil
	case "archived":
		return domain.StatusArchived, nil
	default:
		return "", domain.Invalid("invalid status '" + s + "'")
	}
}
