// Command hexarch is the composition root of the hexagonal task manager: it
// selects a storage backend from the environment, builds the application
// service, chooses an inbound adapter from the first CLI argument (cli,
// httpapi, or httpweb), and runs it.
//
// All backend packages are imported for their registration side effects, so
// swapping persistence is purely a configuration matter
// (HEXARCH_DB_TYPE / HEXARCH_DB_URI).
//
// Public API:
//   - NewApp, NewTaskRepository
//
// Private:
//   - main, help
package main

import (
	"context"
	"fmt"
	"os"

	"hexarch/internal/adapters"
	"hexarch/internal/adapters/cli"
	"hexarch/internal/adapters/httpapi"
	"hexarch/internal/adapters/httpweb"
	"hexarch/internal/application"
	"hexarch/internal/repository"

	// Register every backend with the repository factory. Importing a
	// backend package is what makes it selectable via HEXARCH_DB_TYPE.
	_ "hexarch/internal/repository/memory"
	_ "hexarch/internal/repository/mongo"
	_ "hexarch/internal/repository/oracle"
	_ "hexarch/internal/repository/postgres"
	_ "hexarch/internal/repository/sqlite"
)

// NewApp builds the inbound adapter for the given name. The switch here is
// deliberately the only place that knows which adapter types exist; the same
// application service is shared by all of them.
func NewApp(name string, svc application.TaskService) (adapters.Adapter, error) {
	switch name {
	case "cli":
		return cli.NewCli(svc), nil
	case "httpapi":
		return httpapi.NewApi(svc), nil
	case "httpweb":
		return httpweb.NewWeb(svc), nil
	default:
		return nil, fmt.Errorf("Unknown Applicaton")
	}
}

// NewTaskRepository is the composition root for storage: it reads the
// backend selection from the environment and asks the repository factory for
// the port plus a closer.
func NewTaskRepository(ctx context.Context) (repository.TaskRepository, func() error, error) {
	cfg, err := repository.ConfigFromEnv()
	if err != nil {
		return nil, nil, err
	}
	repo, closer, err := repository.New(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return repo, closer.Close, nil
}

// help prints the top-level usage text.
func help() {
	fmt.Println("Usage:")
	fmt.Println("  cli [command] [options]")
	fmt.Println("  httpapi [ip:port]")
	fmt.Println("  httpweb [ip:port]")
}

// main is the entry point: it resolves the repository, builds the service,
// and dispatches to the adapter named in the first argument.
func main() {
	if len(os.Args) < 2 {
		help()
		os.Exit(0)
	}

	repo, closeRepo, err := NewTaskRepository(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open database: "+err.Error())
		os.Exit(1)
	}
	defer closeRepo()

	service := application.NewTaskService(repo)
	app, err := NewApp(os.Args[1], service)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if err := app.Run(context.Background(), os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}
