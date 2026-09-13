// Package sqlite implements the default SQLite backend of the hexagon: a
// database/sql handle over the pure-Go modernc.org/sqlite driver, wired to
// the shared sqldb.TaskRepositorySQL core with the SQLite dialect. The
// provider is registered with the repository factory as TypeSQLite.
//
// Public API: (none — the package is imported for its registration side
// effect; provider is unexported)
//
// Private:
//   - provider with Open (self-registration)
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"hexarch/internal/repository"
	"hexarch/internal/repository/sqldb"
)

// Registration: importing this package makes the sqlite backend available to
// the repository factory.
func init() {
	repository.Register(repository.TypeSQLite, provider{})
}

// provider implements repository.Provider for SQLite. It owns connection
// pooling, ping, and idempotent schema setup; the returned closer is the
// *sql.DB itself.
type provider struct{}

func (provider) Open(ctx context.Context, uri string) (repository.TaskRepository, io.Closer, error) {
	if uri == "" {
		uri = repository.DefaultURI
	}
	// Enforce foreign keys on every pooled connection via the dsn pragma
	// (per-connection default is OFF); cascade deletes depend on it.
	if !strings.Contains(uri, "?") {
		uri += "?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: open %q: %w", uri, err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	if err := (sqldb.SQLite{}).CreateSchema(ctx, db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("sqlite: schema: %w", err)
	}
	return sqldb.NewTaskRepositorySQL(db, sqldb.SQLite{}), db, nil
}
