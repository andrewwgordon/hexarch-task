// seed.go implements the bootstrap-admin seeding for all SQL dialects
// (spec docs/auth.md FR-U5, §3.6): after table creation, exactly one admin
// user is inserted when the users table is empty.
//
// Ownership decision (spec §3.6): seeding — including the bcrypt hash of the
// default "admin" password — is computed inside the sqldb package. This keeps
// Dialect.CreateSchema signatures unchanged, avoids the import cycle
// (repository cannot import application), and needs no Provider.Open change.
// The clear password appears here only as the bcrypt input; it is never
// stored, logged, or emitted as a literal hash.
//
// Public API:
//   - seedAdmin (called by the dialect CreateSchema implementations)
//
// Private:
//   - mintUUID, mintAPIKey (local idgen mirrors: sqldb cannot import the
//     application layer — application imports repository)
package sqldb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"hexarch/internal/domain"
)

// seedAdminEmail and seedAdminPassword are the spec-mandated bootstrap
// credentials (docs/auth.md FR-U5). The password exists only as bcrypt's
// input; only the resulting hash is stored.
const (
	seedAdminEmail = "admin@email.com"
	seedAdminClear = "admin"
)

// bcryptAdminHash is computed once at package init: bcrypt("admin") at the
// minimum cost. Seeding is a one-time operation per database; cost 4 keeps
// startup fast while remaining a genuine bcrypt hash.
var seedAdminHash = mustHash("admin")

func mustHash(clear string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(clear), bcrypt.MinCost)
	if err != nil {
		// bcrypt cannot fail on MinCost with valid input; panic keeps the
		// package var initializer honest.
		panic("sqldb: cannot hash seed admin password: " + err.Error())
	}
	return string(h)
}

// mintUUID mints a UUIDv4-style string. It mirrors application.RandomTaskID
// (which sqldb must not import — application imports repository, and this
// package is beneath repository).
func mintUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// mintAPIKey returns a 256-bit random hex token (spec NFR-3).
func mintAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// seedAdmin inserts the bootstrap admin (admin@email.com / bcrypt("admin") /
// isadmin=1) only when the users table is empty. Called by every dialect's
// CreateSchema right after the tables are created. Idempotent per FR-U5.
func seedAdmin(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return fmt.Errorf("seed: count users: %w", err)
	}
	if n > 0 {
		return nil
	}
	u, err := domain.NewUser(
		domain.UserID(mintUUID()),
		seedAdminEmail,
		seedAdminHash,
		mintAPIKey(),
		true,
	)
	if err != nil {
		return fmt.Errorf("seed: build admin: %w", err)
	}
	stmt := `INSERT INTO users (id, email, password, apikey, isadmin) ` +
		`SELECT ?, ?, ?, ?, 1 WHERE NOT EXISTS (SELECT 1 FROM users)`
	if _, err := db.ExecContext(ctx, stmt, u.ID().String(), u.Email(), u.Password(), u.APIKey()); err != nil {
		return fmt.Errorf("seed: insert admin: %w", err)
	}
	return nil
}

// firstAdminID returns the id of the (single, seeded) admin so migrations can
// backfill existing task rows to it.
func firstAdminID(ctx context.Context, db *sql.DB) (domain.UserID, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, isadmin FROM users ORDER BY email`)
	if err != nil {
		return "", fmt.Errorf("seed: find admin: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var isadmin any
		if err := rows.Scan(&id, &isadmin); err != nil {
			return "", fmt.Errorf("seed: scan admin: %w", err)
		}
		if truthy(isadmin) {
			return domain.UserID(id), nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("seed: no admin user present after seeding")
}
