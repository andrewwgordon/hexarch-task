// sqlite_repo_test.go runs the backend-agnostic conformance suite against a
// real SQLite file database opened through the repository factory — the
// same production wiring used by cmd/main.go.
package sqlite_test

import (
	"context"
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
