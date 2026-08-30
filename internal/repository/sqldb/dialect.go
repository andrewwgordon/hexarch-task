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

	"hexarch/internal/domain"
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
	// Per-connection FK enforcement; modernc.org/sqlite also accepts the
	// _pragma dsn parameter (set by the provider for pool-wide effect).
	_, _ = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	const createUsers = `
    CREATE TABLE IF NOT EXISTS users (
        id       TEXT    NOT NULL PRIMARY KEY,
        email    TEXT    NOT NULL UNIQUE,
        password TEXT    NOT NULL,   -- bcrypt hash only
        apikey   TEXT    NOT NULL UNIQUE,
        isadmin  INTEGER NOT NULL DEFAULT 0
    );`
	if _, err := db.ExecContext(ctx, createUsers); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, createUsers); err != nil {
		return err
	}
	// Seed the admin BEFORE migrating tasks, so the backfill target exists.
	if err := seedAdmin(ctx, db); err != nil {
		return err
	}
	const createTasks = `
    CREATE TABLE IF NOT EXISTS tasks (
        id          TEXT    NOT NULL PRIMARY KEY,
        user_id     TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        title       TEXT    NOT NULL,
        description TEXT    NOT NULL DEFAULT '',
        status      TEXT    NOT NULL,
        priority    INTEGER NOT NULL,
        deadline    INTEGER NOT NULL DEFAULT 0,  /* 0 == no deadline */
        created_at  INTEGER NOT NULL,
        updated_at  INTEGER NOT NULL
    );`
	if _, err := db.ExecContext(ctx, createTasks); err != nil {
		return err
	}
	adminID, err := firstAdminID(ctx, db)
	if err != nil {
		return err
	}
	return migrateSQLiteTasks(ctx, db, adminID)
}

// migrateSQLiteTasks upgrades pre-multiuser databases in place (spec §3.6):
// add tasks.user_id when absent, then backfill NULLs to the seeded admin's
// id. SQLite cannot ADD COLUMN with NOT NULL and no default, so the column
// is added nullable; every task row is immediately backfilled to the seeded
// admin, which restores the app-level NOT NULL invariant. A full table
// rebuild is explicitly out of scope (spec §3.11).
func migrateSQLiteTasks(ctx context.Context, db *sql.DB, adminID domain.UserID) error {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('tasks')`)
	if err != nil {
		return fmt.Errorf("sqlite: inspect tasks: %w", err)
	}
	hasUserID := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if strings.EqualFold(name, "user_id") {
			hasUserID = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasUserID {
		if _, err := db.ExecContext(ctx, `ALTER TABLE tasks ADD COLUMN user_id TEXT REFERENCES users(id) ON DELETE CASCADE`); err != nil {
			return fmt.Errorf("sqlite: add user_id: %w", err)
		}
	}
	// Backfill any NULL owner to the seeded admin.
	if _, err := db.ExecContext(ctx,
		`UPDATE tasks SET user_id = ? WHERE user_id IS NULL OR user_id = ''`, adminID); err != nil {
		return fmt.Errorf("sqlite: backfill user_id: %w", err)
	}
	return nil
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
	const createUsers = `
    CREATE TABLE IF NOT EXISTS users (
        id       TEXT    NOT NULL PRIMARY KEY,
        email    TEXT    NOT NULL UNIQUE,
        password TEXT    NOT NULL,   -- bcrypt hash only
        apikey   TEXT    NOT NULL UNIQUE,
        isadmin  BOOLEAN NOT NULL DEFAULT false
    );`
	if _, err := db.ExecContext(ctx, createUsers); err != nil {
		return err
	}
	if err := seedAdmin(ctx, db); err != nil {
		return err
	}
	const create = `
    CREATE TABLE IF NOT EXISTS tasks (
        id          TEXT    NOT NULL PRIMARY KEY,
        user_id     TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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
	// PL/SQL block and swallow ORA-00955 ("name is already used"). Inline
	// column-clause ON DELETE CASCADE is supported since Oracle 8i (the
	// 12c floor is for OFFSET ... FETCH pagination, not FKs).
	const createUsers = `
    BEGIN
        EXECUTE IMMEDIATE 'CREATE TABLE users (
            id       VARCHAR2(64)  NOT NULL PRIMARY KEY,
            email    VARCHAR2(255) NOT NULL UNIQUE,
            password VARCHAR2(64)  NOT NULL,
            apikey   VARCHAR2(64)  NOT NULL UNIQUE,
            isadmin  NUMBER(1)     NOT NULL DEFAULT 0
        )';
    EXCEPTION WHEN OTHERS THEN
        IF SQLCODE != -955 THEN RAISE; END IF;
    END;`
	if _, err := db.ExecContext(ctx, createUsers); err != nil {
		return err
	}
	if err := seedAdmin(ctx, db); err != nil {
		return err
	}
	const create = `
    BEGIN
        EXECUTE IMMEDIATE 'CREATE TABLE tasks (
            id          VARCHAR2(64)  NOT NULL PRIMARY KEY,
            user_id     VARCHAR2(64)  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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
