// config.go defines the configuration inputs of the repository factory: the
// DBType enum, the Config value, and ConfigFromEnv — the only place in the
// codebase that reads the HEXARCH_DB_* environment variables, including the
// legacy HEXARCH_DB_PATH fallback for SQLite.
//
// Public API:
//   - Types:  DBType, Config
//   - Values: TypeSQLite, TypePostgres, TypeOracle, TypeMongoDB, TypeMemory,
//     DefaultType, DefaultURI
//   - Vars:   SupportedTypes
//   - Funcs:  DefaultConfig, ConfigFromEnv, SupportedTypesString
//   - Methods: DBType.String, Config.Validate
//
// Private:
//   - env keys (envType, envURI, envPath), requiresURI
package repository

import (
	"fmt"
	"os"
	"strings"
)

// DBType identifies a supported storage backend. The set of runnable
// backends is open-ended: the factory defines the built-ins, adapter
// packages register themselves via Register, and the factory only resolves
// the requested type.
type DBType string

const (
	TypeSQLite   DBType = "sqlite"
	TypePostgres DBType = "postgres"
	TypeOracle   DBType = "oracle"
	TypeMongoDB  DBType = "mongodb"
	TypeMemory   DBType = "memory"
)

func (t DBType) String() string { return string(t) }

// SupportedTypes lists every built-in backend the factory can open.
var SupportedTypes = []DBType{TypeSQLite, TypePostgres, TypeOracle, TypeMongoDB, TypeMemory}

const (
	// DefaultType is the backend used when HEXARCH_DB_TYPE is unset.
	DefaultType = TypeSQLite
	// DefaultURI is used when HEXARCH_DB_URI is unset (a SQLite file).
	DefaultURI = "tasks.db"

	envType = "HEXARCH_DB_TYPE"
	envURI  = "HEXARCH_DB_URI"
	// envPath is the legacy SQLite-specific variable, superseded by
	// HEXARCH_DB_URI. It is honored only when the chosen backend is SQLite.
	envPath = "HEXARCH_DB_PATH"
)

// Config is the parsed, validated input to the repository factory. It is
// produced by ConfigFromEnv, but tests and embedders may build it directly —
// the factory is a pure function of Config.
type Config struct {
	Type DBType
	URI  string
}

// DefaultConfig returns the configuration used when no environment is set.
func DefaultConfig() Config {
	return Config{Type: DefaultType, URI: DefaultURI}
}

// ConfigFromEnv reads HEXARCH_DB_TYPE and HEXARCH_DB_URI, applies the legacy
// HEXARCH_DB_PATH fallback for SQLite, and validates the result. This is the
// only place in the codebase that touches the database environment.
func ConfigFromEnv() (Config, error) {
	cfg := DefaultConfig()
	if v := os.Getenv(envType); v != "" {
		cfg.Type = DBType(v)
	}
	uriSet := false
	if v := os.Getenv(envURI); v != "" {
		cfg.URI = v
		uriSet = true
	}
	// Legacy variable: honored only for SQLite and only when the modern
	// HEXARCH_DB_URI was not set.
	if p := os.Getenv(envPath); p != "" && cfg.Type == TypeSQLite && !uriSet {
		cfg.URI = p
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if requiresURI(cfg.Type) && !uriSet {
		return Config{}, fmt.Errorf("repository: %s backend requires HEXARCH_DB_URI", cfg.Type)
	}
	return cfg, nil
}

// requiresURI reports whether a backend needs an explicit connection URI
// rather than the SQLite file default.
func requiresURI(t DBType) bool {
	switch t {
	case TypePostgres, TypeOracle, TypeMongoDB:
		return true
	default:
		return false
	}
}

// Validate checks that the type is supported and that backends needing a
// connection URI have one. Unknown types are accepted here only when they
// were registered with the factory (see Register).
func (c Config) Validate() error {
	switch c.Type {
	case TypeSQLite, TypeMemory:
		// No URI requirement.
	case TypePostgres, TypeOracle, TypeMongoDB:
		if strings.TrimSpace(c.URI) == "" {
			return fmt.Errorf("repository: %s backend requires HEXARCH_DB_URI", c.Type)
		}
	default:
		if _, ok := lookup(c.Type); !ok {
			return fmt.Errorf("repository: unsupported HEXARCH_DB_TYPE %q (supported: %s)",
				c.Type, SupportedTypesString())
		}
	}
	return nil
}

// SupportedTypesString returns a human-readable list for error messages.
func SupportedTypesString() string {
	names := make([]string, len(SupportedTypes))
	for i, t := range SupportedTypes {
		names[i] = t.String()
	}
	return strings.Join(names, ", ")
}
