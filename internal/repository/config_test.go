// config_test.go covers ConfigFromEnv and Config.Validate: defaults, the
// legacy HEXARCH_DB_PATH fallback (SQLite only, overridden by
// HEXARCH_DB_URI), the URI requirement for network backends, and rejection
// of unsupported types. Environment variables are blanked in each test so
// the suite is hermetic regardless of the developer's shell.
package repository_test

import (
	"strings"
	"testing"

	"hexarch/internal/repository"
)

// unsetEnv blanks every database environment variable so tests are hermetic
// regardless of the developer's shell.
func unsetEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HEXARCH_DB_TYPE", "HEXARCH_DB_URI", "HEXARCH_DB_PATH"} {
		t.Setenv(k, "")
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	unsetEnv(t)
	cfg, err := repository.ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Type != repository.TypeSQLite {
		t.Errorf("Type = %s, want sqlite", cfg.Type)
	}
	if cfg.URI != "tasks.db" {
		t.Errorf("URI = %q, want tasks.db", cfg.URI)
	}
}

func TestConfigFromEnvTypeAndURI(t *testing.T) {
	unsetEnv(t)
	t.Setenv("HEXARCH_DB_TYPE", "mongodb")
	t.Setenv("HEXARCH_DB_URI", "mongodb://localhost:27017/hexarch")
	cfg, err := repository.ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Type != repository.TypeMongoDB {
		t.Errorf("Type = %s, want mongodb", cfg.Type)
	}
	if cfg.URI != "mongodb://localhost:27017/hexarch" {
		t.Errorf("URI = %q", cfg.URI)
	}
}

func TestConfigFromEnvLegacyPathForSQLite(t *testing.T) {
	unsetEnv(t)
	t.Setenv("HEXARCH_DB_PATH", "/tmp/legacy.db")
	cfg, err := repository.ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.URI != "/tmp/legacy.db" {
		t.Errorf("URI = %q, want legacy path", cfg.URI)
	}
}

func TestConfigFromEnvLegacyPathIgnoredWhenURISet(t *testing.T) {
	unsetEnv(t)
	t.Setenv("HEXARCH_DB_URI", "tasks2.db")
	t.Setenv("HEXARCH_DB_PATH", "/tmp/legacy.db")
	cfg, err := repository.ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.URI != "tasks2.db" {
		t.Errorf("URI = %q, want HEXARCH_DB_URI to win", cfg.URI)
	}
}

func TestConfigFromEnvLegacyPathIgnoredForNonSQLite(t *testing.T) {
	unsetEnv(t)
	t.Setenv("HEXARCH_DB_TYPE", "memory")
	t.Setenv("HEXARCH_DB_PATH", "/tmp/legacy.db")
	cfg, err := repository.ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.URI != "tasks.db" {
		t.Errorf("URI = %q, want default (legacy path must not leak into other backends)", cfg.URI)
	}
}

func TestConfigFromEnvUnsupportedType(t *testing.T) {
	unsetEnv(t)
	t.Setenv("HEXARCH_DB_TYPE", "mysql")
	_, err := repository.ConfigFromEnv()
	if err == nil {
		t.Fatal("ConfigFromEnv: want error for unsupported type")
	}
	if !strings.Contains(err.Error(), "mysql") {
		t.Errorf("error = %q, want it to name the offending type", err)
	}
}

func TestConfigFromEnvMissingURIForURIBackends(t *testing.T) {
	for _, typ := range []string{"postgres", "oracle", "mongodb"} {
		t.Run(typ, func(t *testing.T) {
			unsetEnv(t)
			t.Setenv("HEXARCH_DB_TYPE", typ)
			_, err := repository.ConfigFromEnv()
			if err == nil {
				t.Fatalf("want error: %s backend requires HEXARCH_DB_URI", typ)
			}
		})
	}
}

func TestValidateURIRequired(t *testing.T) {
	for _, typ := range []repository.DBType{
		repository.TypePostgres, repository.TypeOracle, repository.TypeMongoDB,
	} {
		cfg := repository.Config{Type: typ, URI: ""}
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate(%s): want error for empty URI", typ)
		}
	}
}

func TestConfigFromEnvMemoryNoURIRequired(t *testing.T) {
	unsetEnv(t)
	t.Setenv("HEXARCH_DB_TYPE", "memory")
	if _, err := repository.ConfigFromEnv(); err != nil {
		t.Fatalf("memory backend should not require a URI: %v", err)
	}
}
