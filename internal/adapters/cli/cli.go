// Package cli implements the command-line inbound adapter of the hexagon.
// It parses argv into options and positionals, calls the application
// service, and formats domain results as human-readable text. It is one of
// three inbound adapters sharing the same TaskService.
//
// This file (cli.go) contains the Cli adapter: subcommand dispatch (Run),
// the per-subcommand handlers, and the usage text.
//
// Public API:
//   - Types:  Cli
//   - Funcs:  NewCli
//   - Methods: Cli.Run
//
// Private:
//   - handlers: help, create, list, get, rename, changeStatus, priority,
//     deadline, remove, stats
//   - parsing:  parseStatus (token -> domain.Status)
package cli

import (
	"context"
	"fmt"
	"strconv"
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

// Run dispatches the first token as a subcommand.
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
	case "create":
		return c.create(ctx, rest)
	case "list":
		return c.list(ctx, rest)
	case "get":
		return c.get(ctx, rest)
	case "rename":
		return c.rename(ctx, rest)
	case "start":
		return c.changeStatus(ctx, rest, domain.StatusInProgress)
	case "done":
		return c.changeStatus(ctx, rest, domain.StatusDone)
	case "priority":
		return c.priority(ctx, rest)
	case "deadline":
		return c.deadline(ctx, rest)
	case "remove":
		return c.remove(ctx, rest)
	case "stats":
		return c.stats(ctx, rest)
	default:
		return cliError{msg: fmt.Sprintf("unknown command %q; try 'help'", command)}
	}
}

// help prints the subcommand usage text.
func (c *Cli) help() {
	fmt.Println("usage:")
	fmt.Println("  create   --title T [--desc D] [--priority N] [--deadline 2026-08-30]")
	fmt.Println("  list     [--status todo|in_progress|done|archived] [--search q] [--limit n] [--offset n]")
	fmt.Println("  get      <id>")
	fmt.Println("  rename   <id> \"new title\"")
	fmt.Println("  priority <id> <n>")
	fmt.Println("  start    <id>      (mark in_progress)")
	fmt.Println("  done     <id>      (mark done)")
	fmt.Println("  deadline <id> YYYY-MM-DD|done")
	fmt.Println("  remove   <id>")
	fmt.Println("  stats")
}

// create handles the `create` subcommand: parses --title/--desc/--priority/
// --deadline options, calls CreateTask, and reports the new task.
func (c *Cli) create(ctx context.Context, args []string) error {
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
// --status/--search/--limit/--offset options, queries the service, and
// prints one line per task.
func (c *Cli) list(ctx context.Context, args []string) error {
	opts, err := parseArgs(args)
	if err != nil {
		return err
	}

	filter := repository.DefaultTaskFilter()
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

// get handles the `get` subcommand: prints every field of one task.
func (c *Cli) get(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return domain.Invalid("usage: get <id>")
	}
	t, err := c.Svc.GetTask(ctx, domain.TaskID(args[0]))
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

// rename handles the `rename` subcommand: sets a new title.
func (c *Cli) rename(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return domain.Invalid(`usage: rename <id> "new title"`)
	}
	t, err := c.Svc.RenameTask(ctx, domain.TaskID(args[0]), args[1])
	if err != nil {
		return err
	}
	fmt.Printf("renamed to: %s\n", t.Title())
	return nil
}

// changeStatus backs the `start` and `done` subcommands: it moves a task
// through the domain state machine (todo -> in_progress, in_progress ->
// done).
func (c *Cli) changeStatus(ctx context.Context, args []string, status domain.Status) error {
	if len(args) != 1 {
		return domain.Invalid("usage: start|done <id>")
	}
	t, err := c.Svc.ChangeStatus(ctx, domain.TaskID(args[0]), status)
	if err != nil {
		return err
	}
	fmt.Printf("%s is now %s\n", t.ID(), t.Status())
	return nil
}

// priority handles the `priority` subcommand: re-prioritizes a task.
func (c *Cli) priority(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return domain.Invalid("usage: priority <id> <n>")
	}
	n, err := strconv.Atoi(args[1])
	if err != nil {
		return domain.Invalid("not an integer: " + args[1])
	}
	t, err := c.Svc.ChangePriority(ctx, domain.TaskID(args[0]), n)
	if err != nil {
		return err
	}
	fmt.Printf("%s priority -> %d\n", t.ID(), t.Priority())
	return nil
}

// deadline handles the `deadline` subcommand: sets a YYYY-MM-DD deadline,
// or clears it when the argument is the literal "done".
func (c *Cli) deadline(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return domain.Invalid("usage: deadline <id> YYYY-MM-DD|done")
	}
	if args[1] == "done" {
		t, err := c.Svc.ClearDeadline(ctx, domain.TaskID(args[0]))
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
	t, err := c.Svc.SetDeadline(ctx, domain.TaskID(args[0]), d)
	if err != nil {
		return err
	}
	fmt.Printf("%s deadline -> %s\n", t.ID(), d.Format(time.DateOnly))
	return nil
}

// remove handles the `remove` subcommand: deletes a task by ID.
func (c *Cli) remove(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return domain.Invalid("usage: remove <id>")
	}
	if err := c.Svc.DeleteTask(ctx, domain.TaskID(args[0])); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}

// stats handles the `stats` subcommand: prints the per-status counts.
func (c *Cli) stats(ctx context.Context, _ []string) error {
	s, err := c.Svc.Stats(ctx)
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
