// Package sqldb holds the shared relational implementation of the
// TaskRepository port. TaskRepositorySQL is parameterized by a small
// Dialect interface (dialect.go) so one code path serves SQLite, Postgres,
// and Oracle; each backend package becomes a thin Open shim over this type.
//
// Storage conventions: timestamps are Unix seconds; deadlines are stored as
// 0 when absent (epoch 0); query text is written with `?` placeholders for
// the Dialect to rebind.
//
// Public API:
//   - Types:  TaskRepositorySQL
//   - Funcs:  NewTaskRepositorySQL
//   - Methods: the seven TaskRepository methods
//
// Private:
//   - taskColumns, countWhere, mapTask, encodeDeadline, decodeDeadline
package sqldb

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

const taskColumns = "id, title, description, status, priority, deadline, created_at, updated_at"

// TaskRepositorySQL is the shared relational implementation of the
// TaskRepository port, parameterized by a Dialect. Every SQL backend (SQLite,
// Postgres, Oracle, ...) becomes a thin Open() shim over this type.
type TaskRepositorySQL struct {
	db *sql.DB
	d  Dialect
}

// NewTaskRepositorySQL wires the shared implementation onto a live sql.DB
// handle.
func NewTaskRepositorySQL(db *sql.DB, d Dialect) repository.TaskRepository {
	return &TaskRepositorySQL{db: db, d: d}
}

// Create inserts a new task. Unique-constraint violations are mapped to
// domain.Conflict; any other failure maps to domain.Storage.
func (r *TaskRepositorySQL) Create(ctx context.Context, t domain.Task) error {
	query := r.d.Rebind("INSERT INTO tasks (" + taskColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	_, err := r.db.ExecContext(ctx, query,
		t.ID().String(), t.Title(), t.Description(), t.Status().String(),
		t.Priority(), encodeDeadline(t.Deadline()), t.CreatedAt().Unix(), t.UpdatedAt().Unix())
	if err != nil {
		if r.d.IsUniqueViolation(err) {
			return domain.Conflict("task " + t.ID().String() + " already exists")
		}
		return domain.Storage("insert failed: " + err.Error())
	}
	return nil
}

// ByID returns the task with the given ID, or domain.NotFound.
func (r *TaskRepositorySQL) ByID(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	query := r.d.Rebind(`SELECT ` + taskColumns + ` FROM tasks WHERE id = ?`)
	rows, err := r.db.QueryContext(ctx, query, id.String())
	if err != nil {
		return domain.Task{}, domain.Storage("query failed: " + err.Error())
	}
	defer rows.Close()
	if !rows.Next() {
		return domain.Task{}, domain.NotFound("task " + id.String() + " not found")
	}
	return mapTask(rows)
}

// List returns the tasks matching the filter, ordered by priority
// descending, then created_at descending, with paging applied.
func (r *TaskRepositorySQL) List(ctx context.Context, f repository.TaskFilter) ([]domain.Task, error) {
	var clauses []string
	var args []any
	if f.Status != nil {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status.String())
	}
	if f.Search != "" {
		clauses = append(clauses,
			"("+r.d.CaseInsensitiveMatch("title", "?")+" OR "+
				r.d.CaseInsensitiveMatch("description", "?")+")")
		pattern := "%" + strings.ToLower(f.Search) + "%"
		args = append(args, pattern, pattern)
	}
	var where string
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	query := `SELECT ` + taskColumns + ` FROM tasks` + where +
		` ORDER BY priority DESC, created_at DESC ` + r.d.Pagination(limit, offset)
	query = r.d.Rebind(query)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return []domain.Task{}, domain.Storage("list failed: " + err.Error())
	}
	defer rows.Close()

	var out []domain.Task
	for rows.Next() {
		t, err := mapTask(rows)
		if err != nil {
			return out, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return []domain.Task{}, domain.Storage(err.Error())
	}
	return out, nil
}

// Count returns the total number of tasks.
func (r *TaskRepositorySQL) Count(ctx context.Context) (int, error) {
	return r.countWhere(ctx, "")
}

// CountByStatus returns the number of tasks in the given status.
func (r *TaskRepositorySQL) CountByStatus(ctx context.Context, status domain.Status) (int, error) {
	return r.countWhere(ctx, " WHERE status = ?", status.String())
}

// Update persists all mutable fields of an existing task; returning
// domain.NotFound when no row matches the ID.
func (r *TaskRepositorySQL) Update(ctx context.Context, t domain.Task) error {
	query := r.d.Rebind(`UPDATE tasks SET title = ?, description = ?, status = ?, priority = ?, deadline = ?, updated_at = ? WHERE id = ?`)
	result, err := r.db.ExecContext(ctx, query,
		t.Title(), t.Description(), t.Status().String(), t.Priority(),
		encodeDeadline(t.Deadline()), t.UpdatedAt().Unix(), t.ID().String())
	if err != nil {
		return domain.Storage("update failed: " + err.Error())
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Storage("update failed: " + err.Error())
	}
	if affected == 0 {
		return domain.NotFound("task " + t.ID().String() + " not found")
	}
	return nil
}

// Delete removes a task by ID, returning domain.NotFound when no row
// matches.
func (r *TaskRepositorySQL) Delete(ctx context.Context, id domain.TaskID) error {
	query := r.d.Rebind(`DELETE FROM tasks WHERE id = ?`)
	result, err := r.db.ExecContext(ctx, query, id.String())
	if err != nil {
		return domain.Storage("delete failed: " + err.Error())
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.NotFound("task " + id.String() + " not found")
	}
	return nil
}

// ---- helpers ----

// countWhere runs SELECT count(*) with an optional WHERE clause and its
// (already rebindable) arguments.
func (r *TaskRepositorySQL) countWhere(ctx context.Context, where string, args ...any) (int, error) {
	query := r.d.Rebind(`SELECT count(*) AS n FROM tasks` + where)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, domain.Storage("count failed: " + err.Error())
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, domain.Storage(err.Error())
		}
	}
	return n, nil
}

// mapTask scans one result row and rebuilds a domain.Task through the
// hydrator, translating scan/hydration failures into domain.Storage.
func mapTask(rows *sql.Rows) (domain.Task, error) {
	var (
		id, title, description, status string
		priority                       int
		deadline                       int64
		createdAt, updatedAt           int64
	)
	if err := rows.Scan(&id, &title, &description, &status, &priority, &deadline, &createdAt, &updatedAt); err != nil {
		return domain.Task{}, domain.Storage("scan failed: " + err.Error())
	}
	t, err := domain.HydrateTask(
		domain.TaskID(id), title, description, domain.Status(status), priority,
		decodeDeadline(deadline), time.Unix(createdAt, 0), time.Unix(updatedAt, 0))
	if err != nil {
		return domain.Task{}, domain.Storage("corrupted row: " + err.Error())
	}
	return t, nil
}

// encodeDeadline stores a deadline as a Unix timestamp; nil becomes 0,
// which the backends treat as "no deadline".
func encodeDeadline(d *time.Time) int64 {
	if d == nil {
		return 0
	}
	return d.Unix()
}

// decodeDeadline rebuilds a deadline from its stored epoch; 0 is decoded as
// nil (no deadline).
func decodeDeadline(epoch int64) *time.Time {
	if epoch == 0 {
		return nil
	}
	t := time.Unix(epoch, 0)
	return &t
}
