// user_repo.go implements the user half of the TaskRepository port for the
// shared SQL core: the seven user methods, ordered by email, with unique-
// violation mapping and the owner-scoped task filtering done in List.
//
// Public API: (none — methods on TaskRepositorySQL)
package sqldb

import (
	"context"
	"database/sql"
	"strings"

	"hexarch/internal/domain"
)

// CreateUser inserts a new user; duplicate email/apikey → domain.Conflict.
func (r *TaskRepositorySQL) CreateUser(ctx context.Context, u domain.User) error {
	query := r.d.Rebind("INSERT INTO users (" + userColumns + ") VALUES (?, ?, ?, ?, ?)")
	_, err := r.db.ExecContext(ctx, query,
		u.ID().String(), u.Email(), u.Password(), u.APIKey(), boolToInt(u.IsAdmin()))
	if err != nil {
		if r.d.IsUniqueViolation(err) {
			return domain.Conflict("user " + u.Email() + " already exists")
		}
		return domain.Storage("insert user failed: " + err.Error())
	}
	return nil
}

// UpdateUser persists email/password/isadmin; NotFound when absent,
// Conflict on duplicate email.
func (r *TaskRepositorySQL) UpdateUser(ctx context.Context, u domain.User) error {
	query := r.d.Rebind(`UPDATE users SET email = ?, password = ?, apikey = ?, isadmin = ? WHERE id = ?`)
	result, err := r.db.ExecContext(ctx, query,
		u.Email(), u.Password(), u.APIKey(), boolToInt(u.IsAdmin()), u.ID().String())
	if err != nil {
		if r.d.IsUniqueViolation(err) {
			return domain.Conflict("email " + u.Email() + " already exists")
		}
		return domain.Storage("update user failed: " + err.Error())
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Storage("update user failed: " + err.Error())
	}
	if affected == 0 {
		return domain.NotFound("user " + u.ID().String() + " not found")
	}
	return nil
}

// DeleteUser removes a user; owned tasks disappear via FK cascade.
func (r *TaskRepositorySQL) DeleteUser(ctx context.Context, id domain.UserID) error {
	query := r.d.Rebind(`DELETE FROM users WHERE id = ?`)
	result, err := r.db.ExecContext(ctx, query, id.String())
	if err != nil {
		return domain.Storage("delete user failed: " + err.Error())
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return domain.NotFound("user " + id.String() + " not found")
	}
	return nil
}

// ListUsers returns every user ordered by email (repo contract).
func (r *TaskRepositorySQL) ListUsers(ctx context.Context) ([]domain.User, error) {
	query := `SELECT ` + userColumns + ` FROM users ORDER BY email`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, domain.Storage("list users failed: " + err.Error())
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := mapUser(rows)
		if err != nil {
			return out, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return []domain.User{}, domain.Storage(err.Error())
	}
	return out, nil
}

// UserByID returns one user or domain.NotFound.
func (r *TaskRepositorySQL) UserByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	query := r.d.Rebind(`SELECT ` + userColumns + ` FROM users WHERE id = ?`)
	rows, err := r.db.QueryContext(ctx, query, id.String())
	if err != nil {
		return domain.User{}, domain.Storage("query failed: " + err.Error())
	}
	defer rows.Close()
	if !rows.Next() {
		return domain.User{}, domain.NotFound("user " + id.String() + " not found")
	}
	return mapUser(rows)
}

// AuthUser is a LOOKUP ONLY by lower-cased email; no password verification
// happens here (application layer owns bcrypt comparison).
func (r *TaskRepositorySQL) AuthUser(ctx context.Context, email string) (domain.User, error) {
	query := r.d.Rebind(`SELECT ` + userColumns + ` FROM users WHERE lower(email) = ?`)
	rows, err := r.db.QueryContext(ctx, query, lower(email))
	if err != nil {
		return domain.User{}, domain.Storage("query failed: " + err.Error())
	}
	defer rows.Close()
	if !rows.Next() {
		return domain.User{}, domain.NotFound("user " + email + " not found")
	}
	return mapUser(rows)
}

// UserByAPIKey looks a user up by API key, or domain.NotFound.
func (r *TaskRepositorySQL) UserByAPIKey(ctx context.Context, key string) (domain.User, error) {
	query := r.d.Rebind(`SELECT ` + userColumns + ` FROM users WHERE apikey = ?`)
	rows, err := r.db.QueryContext(ctx, query, key)
	if err != nil {
		return domain.User{}, domain.Storage("query failed: " + err.Error())
	}
	defer rows.Close()
	if !rows.Next() {
		return domain.User{}, domain.NotFound("user by apikey not found")
	}
	return mapUser(rows)
}

// ---- helpers ----

// mapUser scans one users row into a domain.User via the hydrator.
func mapUser(rows *sql.Rows) (domain.User, error) {
	var (
		id, email, password, apikey string
		isadmin                     any // BOOLEAN (pg) or INTEGER/NUMBER (sqlite/oracle)
	)
	if err := rows.Scan(&id, &email, &password, &apikey, &isadmin); err != nil {
		return domain.User{}, domain.Storage("scan failed: " + err.Error())
	}
	u, err := domain.HydrateUser(domain.UserID(id), email, password, apikey, truthy(isadmin))
	if err != nil {
		return domain.User{}, domain.Storage("corrupted user row: " + err.Error())
	}
	return u, nil
}

// truthy interprets the driver's boolean representation across dialects:
// Go bool (pgx BOOLEAN), int64/float64 (SQLite INTEGER), and string digits
// (Oracle NUMBER).
func truthy(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case int:
		return v != 0
	case float64:
		return v != 0
	case string:
		return v == "1" || v == "t" || v == "true"
	default:
		return false
	}
}

// lower normalizes an email for the AuthUser lookup.
func lower(s string) string { return strings.ToLower(s) }

// boolToInt stores a Go bool as the SQL 0/1 used by every dialect
// (Oracle NUMBER(1), SQLite INTEGER; Postgres BOOLEAN is scanned via any).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
