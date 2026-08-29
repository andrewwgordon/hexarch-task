// task_repo_mem_test.go runs the backend-agnostic conformance suite against
// the in-memory repository opened through the factory, pinning the
// reference behavior every other backend must match.
package memory_test

import (
	"context"
	"testing"

	"hexarch/internal/repository"
	"hexarch/internal/repository/conformance"

	// Registers the memory backend with the factory.
	_ "hexarch/internal/repository/memory"
)

// TestConformance runs the full port contract against the in-memory backend,
// opened through the repository factory. It pins the reference behavior that
// every other backend must match.
func TestConformance(t *testing.T) {
	t.Parallel()
	conformance.Repository(t, func(t *testing.T) repository.TaskRepository {
		t.Helper()
		repo, closer, err := repository.New(context.Background(),
			repository.Config{Type: repository.TypeMemory})
		if err != nil {
			t.Fatalf("factory New: %v", err)
		}
		t.Cleanup(func() { _ = closer.Close() })
		return repo
	})
}
