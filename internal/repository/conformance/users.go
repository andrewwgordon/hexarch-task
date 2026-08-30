// users.go holds the user half of the backend-agnostic contract suite: the
// seven user methods, ordered-by-email contract, unique-constraint mapping,
// delete-user cascade, and TaskFilter.UserID scoping. Run by Repository()
// as the "Users" subtest against every backend.
//
// Public API: (none — invoked by Repository)
package conformance

import (
	"testing"
	"time"

	"hexarch/internal/domain"
	"hexarch/internal/repository"
)

// fixtureHash is a stand-in bcrypt hash (the repo contract stores it
// verbatim; hashing is an application concern).
func fixtureHash() string { return "$2a$04$abcdefghijklmnopqrstuv0123456789012345678901234567890" }

// seedOwnerFixture creates the task fixture owner ("u-owner") if it does not
// already exist, so SQL backends' tasks.user_id FK accepts fixture tasks.
func seedOwnerFixture(t *testing.T, repo repository.TaskRepository) domain.UserID {
	t.Helper()
	if u, err := repo.UserByID(ctx, owner); err == nil {
		return u.ID()
	}
	u, err := domain.NewUser(owner, "owner@fixture.test", fixtureHash(), "owner-apikey-fixture", false)
	if err != nil {
		t.Fatalf("NewUser(owner fixture): %v", err)
	}
	if err := repo.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser(owner fixture): %v", err)
	}
	return u.ID()
}

// newRepoWithOwner returns a fresh repo from newRepo with the fixture owner
// user created (used by every task subtest).
func newRepoWithOwner(t *testing.T, newRepo func(t *testing.T) repository.TaskRepository) repository.TaskRepository {
	t.Helper()
	repo := newRepo(t)
	seedOwnerFixture(t, repo)
	return repo
}

// mustUser builds a user with the given id/email/apikey or fails the test.
func mustUser(t *testing.T, id domain.UserID, email, apikey string, admin bool) domain.User {
	t.Helper()
	u, err := domain.NewUser(id, email, fixtureHash(), apikey, admin)
	if err != nil {
		t.Fatalf("NewUser(%s): %v", id, err)
	}
	return u
}

// Users runs the complete user contract against fresh repos from newRepo.
func Users(t *testing.T, newRepo func(t *testing.T) repository.TaskRepository) {
	t.Helper()

	t.Run("CreateUserAndUserByID", func(t *testing.T) {
		repo := newRepo(t)
		want := mustUser(t, "u1", "u1@example.com", "key-u1", false)
		if err := repo.CreateUser(ctx, want); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		got, err := repo.UserByID(ctx, "u1")
		if err != nil {
			t.Fatalf("UserByID: %v", err)
		}
		requireUser(t, got, want)
	})

	t.Run("CreateUserDuplicateEmailConflict", func(t *testing.T) {
		repo := newRepo(t)
		a := mustUser(t, "u1", "dup@example.com", "key-u1", false)
		b := mustUser(t, "u2", "dup@example.com", "key-u2", false)
		if err := repo.CreateUser(ctx, a); err != nil {
			t.Fatalf("CreateUser(a): %v", err)
		}
		err := repo.CreateUser(ctx, b)
		assertKind(t, err, domain.KindConflict)
	})

	t.Run("CreateUserDuplicateAPIKeyConflict", func(t *testing.T) {
		repo := newRepo(t)
		a := mustUser(t, "u1", "a@example.com", "shared-key", false)
		b := mustUser(t, "u2", "b@example.com", "shared-key", false)
		if err := repo.CreateUser(ctx, a); err != nil {
			t.Fatalf("CreateUser(a): %v", err)
		}
		err := repo.CreateUser(ctx, b)
		assertKind(t, err, domain.KindConflict)
	})

	t.Run("UpdateUserPersistsAndConflictsOnDuplicateEmail", func(t *testing.T) {
		repo := newRepo(t)
		seedOwnerFixture(t, repo)
		a := mustUser(t, "u1", "u1@example.com", "key-u1", false)
		b := mustUser(t, "u2", "u2@example.com", "key-u2", false)
		if err := repo.CreateUser(ctx, a); err != nil {
			t.Fatalf("CreateUser(a): %v", err)
		}
		if err := repo.CreateUser(ctx, b); err != nil {
			t.Fatalf("CreateUser(b): %v", err)
		}
		// Change u1's email to a fresh value and persist.
		changed, err := domain.NewUser("u1", "renamed@example.com", fixtureHash(), "key-u1", true)
		if err != nil {
			t.Fatalf("NewUser(changed): %v", err)
		}
		if err := repo.UpdateUser(ctx, changed); err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}
		got, err := repo.UserByID(ctx, "u1")
		if err != nil {
			t.Fatalf("UserByID: %v", err)
		}
		if got.Email() != "renamed@example.com" || !got.IsAdmin() {
			t.Errorf("UpdateUser did not persist: %+v", got)
		}
		// Change u2's email to u1's (now "renamed") — fine; then to a
		// duplicate of u1's current email → Conflict.
		dup, err := domain.NewUser("u2", "renamed@example.com", fixtureHash(), "key-u2", false)
		if err != nil {
			t.Fatalf("NewUser(dup): %v", err)
		}
		err = repo.UpdateUser(ctx, dup)
		assertKind(t, err, domain.KindConflict)
	})

	t.Run("UpdateUserNotFound", func(t *testing.T) {
		repo := newRepo(t)
		ghost := mustUser(t, "ghost", "ghost@example.com", "key-ghost", false)
		assertKind(t, repo.UpdateUser(ctx, ghost), domain.KindNotFound)
	})

	t.Run("DeleteUserCascadesTasks", func(t *testing.T) {
		repo := newRepoWithOwner(t, newRepo)
		// Owner creates two tasks; owner is deleted; tasks disappear.
		t1 := mustTask(t, "cascade-1", "owned one", 1, time.Now())
		t2 := mustTask(t, "cascade-2", "owned two", 1, time.Now())
		if err := repo.Create(ctx, t1); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.Create(ctx, t2); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.DeleteUser(ctx, owner); err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}
		if _, err := repo.ByID(ctx, "cascade-1"); kind(t, err) != domain.KindNotFound {
			t.Errorf("task cascade-1 should be gone, got %v", err)
		}
		if _, err := repo.ByID(ctx, "cascade-2"); kind(t, err) != domain.KindNotFound {
			t.Errorf("task cascade-2 should be gone, got %v", err)
		}
		// Re-create the owner for other assertions; second delete → NotFound.
		assertKind(t, repo.DeleteUser(ctx, owner), domain.KindNotFound)
	})

	t.Run("DeleteUserNotFound", func(t *testing.T) {
		repo := newRepo(t)
		assertKind(t, repo.DeleteUser(ctx, "missing-user"), domain.KindNotFound)
	})

	t.Run("ListUsersOrderedByEmail", func(t *testing.T) {
		repo := newRepo(t)
		seedOwnerFixture(t, repo)
		if err := repo.CreateUser(ctx, mustUser(t, "u3", "zeta@example.com", "key-u3", false)); err != nil {
			t.Fatalf("CreateUser(zeta): %v", err)
		}
		if err := repo.CreateUser(ctx, mustUser(t, "u4", "alpha@example.com", "key-u4", true)); err != nil {
			t.Fatalf("CreateUser(alpha): %v", err)
		}
		users, err := repo.ListUsers(ctx)
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(users) < 2 {
			t.Fatalf("ListUsers = %d users, want >= 2", len(users))
		}
		for i := 1; i < len(users); i++ {
			if users[i-1].Email() > users[i].Email() {
				t.Errorf("ListUsers not ordered by email: %q > %q", users[i-1].Email(), users[i].Email())
			}
		}
	})

	t.Run("AuthUserLookupByEmail", func(t *testing.T) {
		repo := newRepo(t)
		if err := repo.CreateUser(ctx, mustUser(t, "u1", "Mixed@Case.test", "key-u1", false)); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		got, err := repo.AuthUser(ctx, "mixed@case.test")
		if err != nil {
			t.Fatalf("AuthUser(lower): %v", err)
		}
		if got.ID() != "u1" {
			t.Errorf("AuthUser returned %s, want u1", got.ID())
		}
		assertKind(t, funcErr(repo.AuthUser(ctx, "unknown@example.com")), domain.KindNotFound)
	})

	t.Run("UserByAPIKey", func(t *testing.T) {
		repo := newRepo(t)
		if err := repo.CreateUser(ctx, mustUser(t, "u1", "u1@example.com", "the-key", false)); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		got, err := repo.UserByAPIKey(ctx, "the-key")
		if err != nil {
			t.Fatalf("UserByAPIKey: %v", err)
		}
		if got.ID() != "u1" {
			t.Errorf("UserByAPIKey returned %s, want u1", got.ID())
		}
		assertKind(t, funcErr(repo.UserByAPIKey(ctx, "no-such-key")), domain.KindNotFound)
	})

	t.Run("TaskFilterByOwner", func(t *testing.T) {
		repo := newRepo(t)
		seedOwnerFixture(t, repo)
		other := mustUser(t, "u9", "other@example.com", "key-u9", false)
		if err := repo.CreateUser(ctx, other); err != nil {
			t.Fatalf("CreateUser(other): %v", err)
		}
		mine := mustTask(t, "mine-1", "mine", 1, time.Now())
		theirs := mustTaskFor(t, "theirs-1", "theirs", 1, time.Now(), other.ID())
		if err := repo.Create(ctx, mine); err != nil {
			t.Fatalf("Create(mine): %v", err)
		}
		if err := repo.Create(ctx, theirs); err != nil {
			t.Fatalf("Create(theirs): %v", err)
		}
		mid := other.ID()
		got, err := repo.List(ctx, repository.TaskFilter{UserID: &mid})
		if err != nil {
			t.Fatalf("List(owner): %v", err)
		}
		if len(got) != 1 || got[0].ID() != "theirs-1" {
			t.Errorf("List(owner) = %v, want [theirs-1]", idsOf(got))
		}
		all, err := repo.List(ctx, repository.TaskFilter{})
		if err != nil {
			t.Fatalf("List(all): %v", err)
		}
		if len(all) != 2 {
			t.Errorf("List(all) = %d tasks, want 2", len(all))
		}
		// Counts honor the same owner scope: `other` sees exactly 1, the
		// unscoped view sees both.
		scopedTotal, err := repo.Count(ctx, repository.TaskFilter{UserID: &mid})
		if err != nil {
			t.Fatalf("Count(owner): %v", err)
		}
		if scopedTotal != 1 {
			t.Errorf("Count(owner) = %d, want 1", scopedTotal)
		}
		scopedTodo, err := repo.CountByStatus(ctx, domain.StatusTodo, repository.TaskFilter{UserID: &mid})
		if err != nil {
			t.Fatalf("CountByStatus(owner): %v", err)
		}
		if scopedTodo != 1 {
			t.Errorf("CountByStatus(owner, todo) = %d, want 1", scopedTodo)
		}
		allN, err := repo.Count(ctx, repository.TaskFilter{})
		if err != nil {
			t.Fatalf("Count(all): %v", err)
		}
		if allN != 2 {
			t.Errorf("Count(all) = %d, want 2", allN)
		}
	})
}

// ---- small helpers ----

func requireUser(t *testing.T, got, want domain.User) {
	t.Helper()
	if got.ID() != want.ID() || got.Email() != want.Email() ||
		got.Password() != want.Password() || got.APIKey() != want.APIKey() ||
		got.IsAdmin() != want.IsAdmin() {
		t.Errorf("user mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func mustTaskFor(t *testing.T, id domain.TaskID, title string, priority int, at time.Time, uid domain.UserID) domain.Task {
	t.Helper()
	task, err := domain.NewTask(id, uid, title, "", priority, nil, now(at))
	if err != nil {
		t.Fatalf("NewTask(%s): %v", id, err)
	}
	return task
}

// funcErr wraps a (user, error) call into an error for assertKind.
func funcErr(_ domain.User, err error) error { return err }

func idsOf(tasks []domain.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID().String())
	}
	return out
}
