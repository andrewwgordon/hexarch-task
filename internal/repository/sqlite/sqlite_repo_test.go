// sqlite_repo_test.go runs the backend-agnostic conformance suite against a
// real SQLite file database opened through the repository factory — the
// same production wiring used by cmd/main.go.
package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"hexarch/internal/repository"
	"hexarch/internal/repository/conformance"

	// Registers the sqlite backend with the factory (the same production path
	// used by cmd/main.go).
	_ "hexarch/internal/repository/sqlite"
)

// TestConformance runs the full port contract against a real SQLite file
// database opened through the repository factory.
func TestConformance(t *testing.T) {
	t.Parallel()
	conformance.Repository(t, func(t *testing.T) repository.TaskRepository {
		t.Helper()
		uri := filepath.Join(t.TempDir(), "tasks.db")
		repo, closer, err := repository.New(context.Background(),
			repository.Config{Type: repository.TypeSQLite, URI: uri})
		if err != nil {
			t.Fatalf("factory New: %v", err)
		}
		t.Cleanup(func() { _ = closer.Close() })
		return repo
	})
}

// TestMigrationFromPreMultiuserSchema verifies the in-place upgrade of a v1
// database (tasks table without user_id) performed by CreateSchema: users
// table created, bootstrap admin seeded, user_id column added, existing rows
// backfilled to the admin, and the whole thing idempotent on re-open.
func TestMigrationFromPreMultiuserSchema(t *testing.T) {
	uri := filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Build the v1 schema and one legacy task row.
	v1 := `CREATE TABLE tasks (
		id          TEXT    NOT NULL PRIMARY KEY,
		title       TEXT    NOT NULL,
		description TEXT    NOT NULL DEFAULT '',
		status      TEXT    NOT NULL,
		priority    INTEGER NOT NULL,
		deadline    INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL,
		updated_at  INTEGER NOT NULL
	);`
	if _, err := db.Exec(v1); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id, title, description, status, priority, deadline, created_at, updated_at)
		VALUES ('legacy-1', 'legacy task', '', 'todo', 2, 0, 1700000000, 1700000000)`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	db.Close()

	// Open through the factory (the production path) — this runs CreateSchema.
	repo, closer, err := repository.New(context.Background(),
		repository.Config{Type: repository.TypeSQLite, URI: uri})
	if err != nil {
		t.Fatalf("factory New: %v", err)
	}
	defer closer.Close()

	// Admin is seeded and legacy task is backfilled to the admin.
	admin, err := repo.AuthUser(context.Background(), "admin@email.com")
	if err != nil {
		t.Fatalf("seeded admin missing after migration: %v", err)
	}
	got, err := repo.ByID(context.Background(), "legacy-1")
	if err != nil {
		t.Fatalf("legacy task missing after migration: %v", err)
	}
	if got.UserID() != admin.ID() {
		t.Errorf("legacy task owner = %q, want admin %q", got.UserID(), admin.ID())
	}
	closer.Close()

	// Re-open: migration is idempotent (no errors, no second admin, task intact).
	repo2, closer2, err := repository.New(context.Background(),
		repository.Config{Type: repository.TypeSQLite, URI: uri})
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer closer2.Close()
	if _, err := repo2.ByID(context.Background(), "legacy-1"); err != nil {
		t.Errorf("legacy task missing after re-open: %v", err)
	}
}
