// user.go implements the user half of the TaskRepository port for the
// in-memory backend: storage for users, the seven user methods, owner-
// scoped task filtering, delete-user cascade, and the local bootstrap-admin
// seed (spec docs/auth.md §3.6: memory replicates the sqldb seed policy).
//
// Public API: (none — methods on TaskRepositoryMem)
//
// Private:
//   - users, seedOnce, seed (bootstrap admin)
package memory

import (
	"context"

	"hexarch/internal/domain"
)

// seedOnce guards the bootstrap-admin seeding so it runs exactly once per
// store lifetime (re-seeding is a no-op, per spec FR-U5).
func (m *TaskRepositoryMem) seed(ctx context.Context) {
	if len(m.users) > 0 {
		return
	}
	id := domain.UserID("00000000-0000-4000-8000-000000000001")
	u, err := domain.NewUser(
		id,
		seedAdminEmail,
		seedAdminHash, // bcrypt("admin"); memory backend is for tests/demo
		"0000000000000000000000000000000000000000000000000000000000000001",
		true,
	)
	if err != nil {
		// Cannot happen with fixed valid inputs; ignore to keep constructor
		// signature unchanged.
		return
	}
	_ = m.CreateUser(ctx, u)
}

// seedAdminHash is bcrypt("admin") at cost 4 (MinCost) so test suites that
// construct the memory repo stay fast. The memory backend is the reference
// test double, never a production store.
const seedAdminHash = "$2a$04$X64jKbP6kJbubwOHQ3b4QO6OWQC.zl4FTLcZrdQn1O8DR9PCBGOdO"

const seedAdminEmail = "admin@email.com"
