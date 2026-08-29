// Package oracle implements the Oracle (12c+) backend of the hexagon. It
// opens a database/sql handle through the pure-Go go-ora driver and
// delegates all queries to the shared sqldb.TaskRepositorySQL core
// parameterized by the Oracle dialect. The provider is registered with the
// factory as TypeOracle.
//
// Public API: (none — the package is imported for its registration side
// effect; provider is unexported)
//
// Private:
//   - provider with Open
package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	// Registers the "oracle" database/sql driver (pure-Go, no CGO).
	_ "github.com/sijms/go-ora/v2"

	"hexarch/internal/repository"
	"hexarch/internal/repository/sqldb"
)

// Registration: importing this package makes the oracle backend available to
// the repository factory.
func init() {
	repository.Register(repository.TypeOracle, provider{})
}

// provider implements repository.Provider for Oracle (12c+). The URI is a
// oracle://user:pass@host:port/service DSN understood by go-ora.
type provider struct{}

func (provider) Open(ctx context.Context, uri string) (repository.TaskRepository, io.Closer, error) {
	if uri == "" {
		return nil, nil, fmt.Errorf("oracle: HEXARCH_DB_URI is required")
	}
	db, err := sql.Open("oracle", uri)
	if err != nil {
		return nil, nil, fmt.Errorf("oracle: open: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("oracle: ping: %w", err)
	}
	if err := (sqldb.Oracle{}).CreateSchema(ctx, db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("oracle: schema: %w", err)
	}
	return sqldb.NewTaskRepositorySQL(db, sqldb.Oracle{}), db, nil
}
