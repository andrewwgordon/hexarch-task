// dialect.go defines the Dialect interface that isolates the SQL variance
// between the relational backends (SQLite, Postgres, Oracle): placeholder
// style, pagination syntax, unique-violation detection, case-insensitive
// matching, and idempotent schema creation. The shared core in repo.go only
// ever depends on this small interface.
//
// Public API:
//   - Interface: Dialect
//   - Types:     SQLite, Postgres, Oracle (each a Dialect implementation)
package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
)

// Dialect isolates the SQL variance between relational backends so the shared
// repository implementation (repo.go) only ever sees this small interface.
//
// The current sqlite adapter hardcodes four things that differ by backend:
// placeholder style, pagination syntax, unique-violation mapping and DDL.
// Those four — plus case-insensitive matching — are exactly this interface.
type Dialect interface {
	// Rebind rewrites `?` placeholders into the backend's preferred style
	// (e.g. $1, :1). Queries in repo.go are written with `?`.
	Rebind(query string) string
	// Pagination returns the trailing paging clause. limit/offset are ints
	// already sanitized by the caller.
	Pagination(limit, offset int) string
	// IsUniqueViolation reports whether err is a unique-constraint violation,
	// so Create can map it to domain.Conflict.
	IsUniqueViolation(err error) bool
	// CaseInsensitiveMatch returns a boolean expression matching column
	// against placeholder, case-insensitively.
	CaseInsensitiveMatch(column, placeholder string) string
	// CreateSchema idempotently ensures the tasks table exists.
	CreateSchema(ctx context.Context, db *sql.DB) error
}

// ---- SQLite ----

// SQLite implements Dialect for modernc.org/sqlite.
type SQLite struct{}

func (SQLite) Rebind(q string) string { return q }

func (SQLite) Pagination(limit, offset int) string {
	return fmt.Sprintf("LIMIT %d OFFSET %d", limit, offset)
}

func (SQLite) IsUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return (sqliteErr.Code() & 0xFF) == 19 // SQLITE_CONSTRAINT
}

func (SQLite) CaseInsensitiveMatch(column, placeholder string) string {
	return fmt.Sprintf("lower(%s) LIKE %s", column, placeholder)
}

func (SQLite) CreateSchema(ctx context.Context, db *sql.DB) error {
	const create = `
    CREATE TABLE IF NOT EXISTS tasks (
        id          TEXT    NOT NULL PRIMARY KEY,
        title       TEXT    NOT NULL,
        description TEXT    NOT NULL DEFAULT '',
        status      TEXT    NOT NULL,
        priority    INTEGER NOT NULL,
        deadline    INTEGER NOT NULL DEFAULT 0,  /* 0 == no deadline */
        created_at  INTEGER NOT NULL,
        updated_at  INTEGER NOT NULL
    );`
	_, err := db.ExecContext(ctx, create)
	return err
}

// ---- Postgres ----

// Postgres implements Dialect for the pgx stdlib driver.
type Postgres struct{}

func (Postgres) Rebind(q string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteByte(q[i])
		}
	}
	return b.String()
}

func (Postgres) Pagination(limit, offset int) string {
	return fmt.Sprintf("LIMIT %d OFFSET %d", limit, offset)
}

func (Postgres) IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (Postgres) CaseInsensitiveMatch(column, placeholder string) string {
	return fmt.Sprintf("%s ILIKE %s", column, placeholder)
}

func (Postgres) CreateSchema(ctx context.Context, db *sql.DB) error {
	const create = `
    CREATE TABLE IF NOT EXISTS tasks (
        id          TEXT    NOT NULL PRIMARY KEY,
        title       TEXT    NOT NULL,
        description TEXT    NOT NULL DEFAULT '',
        status      TEXT    NOT NULL,
        priority    INTEGER NOT NULL,
        deadline    BIGINT  NOT NULL DEFAULT 0,  /* 0 == no deadline */
        created_at  BIGINT  NOT NULL,
        updated_at  BIGINT  NOT NULL
    );`
	_, err := db.ExecContext(ctx, create)
	return err
}

// ---- Oracle (12c+) ----

// Oracle implements Dialect for go-ora. Requires Oracle 12c or later for the
// OFFSET ... FETCH pagination syntax.
type Oracle struct{}

func (Oracle) Rebind(q string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			fmt.Fprintf(&b, ":%d", n)
		} else {
			b.WriteByte(q[i])
		}
	}
	return b.String()
}

func (Oracle) Pagination(limit, offset int) string {
	return fmt.Sprintf("OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", offset, limit)
}

func (Oracle) IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "ORA-00001")
}

func (Oracle) CaseInsensitiveMatch(column, placeholder string) string {
	return fmt.Sprintf("lower(%s) LIKE %s", column, placeholder)
}

func (Oracle) CreateSchema(ctx context.Context, db *sql.DB) error {
	// Oracle has no CREATE TABLE IF NOT EXISTS, so embed the DDL in a
	// PL/SQL block and swallow ORA-00955 ("name is already used").
	const create = `
    BEGIN
        EXECUTE IMMEDIATE 'CREATE TABLE tasks (
            id          VARCHAR2(64)  NOT NULL PRIMARY KEY,
            title       VARCHAR2(4000) NOT NULL,
            description VARCHAR2(4000) NOT NULL DEFAULT ''''',
            status      VARCHAR2(32)  NOT NULL,
            priority    NUMBER        NOT NULL,
            deadline    NUMBER        NOT NULL DEFAULT 0,
            created_at  NUMBER        NOT NULL,
            updated_at  NUMBER        NOT NULL
        )';
    EXCEPTION WHEN OTHERS THEN
        IF SQLCODE != -955 THEN RAISE; END IF;
    END;`
	_, err := db.ExecContext(ctx, create)
	return err
}
