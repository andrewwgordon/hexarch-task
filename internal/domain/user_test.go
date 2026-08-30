// user_test.go exercises the User value object: factory validation rules,
// email normalization, and accessor round-trips.
//
// Public API: (none — test file)
package domain_test

import (
	"testing"

	"hexarch/internal/domain"
)

const (
	testUID    = domain.UserID("u-123")
	testEmail  = "user@example.com"
	testAPIKey = "0123456789abcdef"
)

// testHash is a stand-in bcrypt hash: non-empty and hash-shaped enough for
// the domain (which never verifies hashes itself).
func testHash() string { return "$2a$10$abcdefghijklmnopqrstuv0123456789012345678901234567890" }

func TestNewUserRoundTrip(t *testing.T) {
	u, err := domain.NewUser(testUID, testEmail, testHash(), testAPIKey, false)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	if u.ID() != testUID {
		t.Errorf("ID() = %q, want %q", u.ID(), testUID)
	}
	if u.Email() != testEmail {
		t.Errorf("Email() = %q, want %q", u.Email(), testEmail)
	}
	if u.Password() != testHash() {
		t.Errorf("Password() = %q, want the stored hash", u.Password())
	}
	if u.APIKey() != testAPIKey {
		t.Errorf("APIKey() = %q, want %q", u.APIKey(), testAPIKey)
	}
	if u.IsAdmin() {
		t.Error("IsAdmin() = true, want false")
	}
}

func TestNewUserEmailLowercased(t *testing.T) {
	u, err := domain.NewUser(testUID, "Mixed@Case.COM", testHash(), testAPIKey, true)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	if u.Email() != "mixed@case.com" {
		t.Errorf("Email() = %q, want lower-cased %q", u.Email(), "mixed@case.com")
	}
	if !u.IsAdmin() {
		t.Error("IsAdmin() = false, want true")
	}
}

func TestNewUserInvalid(t *testing.T) {
	cases := []struct {
		name                string
		id                  domain.UserID
		email, hash, apiKey string
	}{
		{"empty id", "", testEmail, testHash(), testAPIKey},
		{"empty email", testUID, "", testHash(), testAPIKey},
		{"email without @", testUID, "noatsign", testHash(), testAPIKey},
		{"@ at start", testUID, "@example.com", testHash(), testAPIKey},
		{"@ at end", testUID, "user@", testHash(), testAPIKey},
		{"empty hash", testUID, testEmail, "", testAPIKey},
		{"empty apikey", testUID, testEmail, testHash(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewUser(tc.id, tc.email, tc.hash, tc.apiKey, false)
			if err == nil {
				t.Fatalf("%s: expected error, got nil", tc.name)
			}
			if got := err.Error(); got == "" {
				t.Errorf("%s: error message must not be empty", tc.name)
			}
		})
	}
}

func TestNewUserKindIsInvalid(t *testing.T) {
	_, err := domain.NewUser("", testEmail, testHash(), testAPIKey, false)
	de, ok := err.(*domain.DomainError)
	if !ok {
		t.Fatalf("expected *domain.DomainError, got %T", err)
	}
	if de.Kind() != domain.KindInvalid {
		t.Errorf("kind = %v, want %v", de.Kind(), domain.KindInvalid)
	}
}

func TestHydrateUserEqualsNewUser(t *testing.T) {
	a, errA := domain.NewUser(testUID, testEmail, testHash(), testAPIKey, true)
	b, errB := domain.HydrateUser(testUID, testEmail, testHash(), testAPIKey, true)
	if errA != nil || errB != nil {
		t.Fatalf("factories: %v %v", errA, errB)
	}
	if a.ID() != b.ID() || a.Email() != b.Email() || a.Password() != b.Password() ||
		a.APIKey() != b.APIKey() || a.IsAdmin() != b.IsAdmin() {
		t.Errorf("HydrateUser != NewUser for same inputs")
	}
}

func TestUserIDString(t *testing.T) {
	if string(testUID) != testUID.String() {
		t.Errorf("UserID.String() mismatch")
	}
}
