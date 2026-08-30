// factory_test.go exercises the repository factory end to end: opening the
// sqlite and memory backends through the registry, rejecting unsupported
// types, and registering a bespoke backend at runtime.
package repository_test

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"hexarch/internal/domain"

	"hexarch/internal/repository"

	// Register the tested backends with the factory.
	_ "hexarch/internal/repository/memory"
	_ "hexarch/internal/repository/sqlite"
)

func mustTime() time.Time {
	return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
}

// seedOwnerUser creates the fixture task owner (u-owner) on the given repo so
// the tasks.user_id foreign key accepts fixture tasks.
func seedOwnerUser(t *testing.T, repo repository.TaskRepository) {
	t.Helper()
	u, err := domain.NewUser("u-owner", "owner@fixture.test", "$2a$04$fixturefixturefixturefixturefixturefixturefixturefix", "owner-apikey-fixture", false)
	if err != nil {
		t.Fatalf("NewUser(owner): %v", err)
	}
	if err := repo.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser(owner): %v", err)
	}
}

func TestNewSQLiteViaFactory(t *testing.T) {
	uri := filepath.Join(t.TempDir(), "tasks.db")
	repo, closer, err := repository.New(context.Background(),
		repository.Config{Type: repository.TypeSQLite, URI: uri})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if repo == nil || closer == nil {
		t.Fatal("New returned nil repo or closer")
	}
	defer closer.Close()

	seedOwnerUser(t, repo)
	task, err := domain.NewTask("f1", "u-owner", "via factory", "", 1, nil, mustTime())
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.ByID(context.Background(), "f1")
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Title() != "via factory" {
		t.Fatalf("title = %q", got.Title())
	}
}

func TestNewSQLiteRejectsEmptyURIAsDefault(t *testing.T) {
	// Provider must fall back to the default file for an empty URI, proving
	// the factory path is complete even without explicit configuration.
	t.Chdir(t.TempDir()) // keep the default tasks.db out of the workspace
	repo, closer, err := repository.New(context.Background(),
		repository.Config{Type: repository.TypeSQLite, URI: ""})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer closer.Close()
	if repo == nil {
		t.Fatal("repo is nil")
	}
}

func TestNewMemoryViaFactory(t *testing.T) {
	repo, closer, err := repository.New(context.Background(),
		repository.Config{Type: repository.TypeMemory})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer closer.Close()

	seedOwnerUser(t, repo)
	task, err := domain.NewTask("m1", "u-owner", "mem", "", 1, nil, mustTime())
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.ByID(context.Background(), "m1"); err != nil {
		t.Fatalf("ByID: %v", err)
	}
}

func TestNewUnsupportedType(t *testing.T) {
	_, _, err := repository.New(context.Background(),
		repository.Config{Type: repository.DBType("redis")})
	if err == nil {
		t.Fatal("New: want error for unsupported type")
	}
}

func TestRegisterCustomBackend(t *testing.T) {
	const custom = repository.DBType("custom-test")
	if _, _, err := repository.New(context.Background(), repository.Config{Type: custom}); err == nil {
		t.Fatal("New: want error before Register")
	}

	repository.Register(custom, wrappingProvider{inner: repository.TypeMemory})

	repo, closer, err := repository.New(context.Background(), repository.Config{Type: custom})
	if err != nil {
		t.Fatalf("New after Register: %v", err)
	}
	defer closer.Close()
	seedOwnerUser(t, repo)
	task, err := domain.NewTask("c1", "u-owner", "custom", "", 1, nil, mustTime())
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// wrappingProvider is a bespoke backend that delegates to a registered one.
type wrappingProvider struct{ inner repository.DBType }

func (p wrappingProvider) Open(ctx context.Context, uri string) (repository.TaskRepository, io.Closer, error) {
	return repository.New(ctx, repository.Config{Type: p.inner})
}
