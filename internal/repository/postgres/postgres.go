// Package postgres implements the Postgres backend of the hexagon. It opens
// a database/sql handle through the pgx stdlib driver and delegates all
// queries to the shared sqldb.TaskRepositorySQL core parameterized by the
// Postgres dialect. The provider is registered with the factory as
// TypePostgres.
//
// Public API: (none — the package is imported for its registration side
// effect; provider is unexported)
//
// Private:
//   - provider with Open
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"

	"hexarch/internal/repository"
	"hexarch/internal/repository/sqldb"
)

// Registration: importing this package makes the postgres backend available
// to the repository factory.
func init() {
	repository.Register(repository.TypePostgres, provider{})
}

// provider implements repository.Provider for Postgres. The URI is a
// postgres:// DSN understood by pgx.
type provider struct{}

func (provider) Open(ctx context.Context, uri string) (repository.TaskRepository, io.Closer, error) {
	if uri == "" {
		return nil, nil, fmt.Errorf("postgres: HEXARCH_DB_URI is required")
	}
	db, err := sql.Open("pgx", uri)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: open: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("postgres: ping: %w", err)
	}
	if err := (sqldb.Postgres{}).CreateSchema(ctx, db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("postgres: schema: %w", err)
	}
	return sqldb.NewTaskRepositorySQL(db, sqldb.Postgres{}), db, nil
}
