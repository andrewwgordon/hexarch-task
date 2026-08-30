// mongo_repo_test.go runs the backend-agnostic conformance suite against a
// live MongoDB when one is available. Skipped unless HEXARCH_TEST_MONGO is
// set to a mongodb:// URI, mirroring the skip pattern used for the other
// optional backends.
package mongo_test

import (
	"context"
	"os"
	"testing"

	"hexarch/internal/repository"
	"hexarch/internal/repository/conformance"

	_ "hexarch/internal/repository/mongo"
)

func TestConformanceMongo(t *testing.T) {
	uri := os.Getenv("HEXARCH_TEST_MONGO")
	if uri == "" {
		t.Skip("HEXARCH_TEST_MONGO not set; skipping live MongoDB conformance")
	}
	conformance.Repository(t, func(t *testing.T) repository.TaskRepository {
		t.Helper()
		repo, closer, err := repository.New(context.Background(),
			repository.Config{Type: repository.TypeMongoDB, URI: uri})
		if err != nil {
			t.Fatalf("factory New: %v", err)
		}
		t.Cleanup(func() { _ = closer.Close() })
		return repo
	})
}
