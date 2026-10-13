package identity

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testPostgresUserStore wants a local Postgres. If there is none the test
// is skipped: unit tests have to pass without a network (see testRedis /
// testPostgres, the SAME pattern). The DSN comes from the environment
// variable; the default value is for a throwaway, TEST-LOCAL container (not a production secret).
func testPostgresUserStore(t *testing.T) *PostgresUserStore {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Skip("no local Postgres, skipping")
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewPostgresUserStore(pool)
}

// contractResult is the OBSERVABLE result of a UserStore call: the fake
// and Postgres have to be EXACTLY the same in these three values. The ID
// is not compared (uuid, different in every store).
type contractResult struct {
	found bool
	email string
	name  string
	err   string // the KIND of error ("", "user-missing", "email-in-use", "password-short", "other")
}

func contractErr(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUserNotFound):
		return "user-missing"
	case errors.Is(err, ErrEmailInUse):
		return "email-in-use"
	case errors.Is(err, ErrPasswordTooShort):
		return "password-short"
	default:
		return "other: " + err.Error()
	}
}

func result(k *User, err error) contractResult {
	s := contractResult{err: contractErr(err)}
	if k != nil {
		s.found = true
		s.email = k.Email
		s.name = k.Name
	}
	return s
}

// CONTRACT TEST: FakeUserStore and PostgresUserStore must do the SAME
// thing on the SAME inputs. On this branch the two had diverged, and the
// divergence hid a real production bug (a user who signed up with a
// padded email could not log in). If there is no Postgres the test is
// SKIPPED; the fake side runs anyway and is compared to the expected values.
func TestUserStore_Contract(t *testing.T) {
	const password = "gecerliSifre12"
	// A fresh email on every run: Postgres is persistent, the same email
	// would come out "in use" on the second run and the test would break itself.
	baseEmail := "sozlesme-" + uuid.NewString() + "@ornek.com"
	padded := "  " + baseEmail + "  "
	upper := strings.ToUpper(baseEmail)
	missingID := uuid.NewString()

	steps := []struct {
		name string
		call func(context.Context, UserStore) contractResult
		want contractResult
	}{
		{
			// Leading/trailing whitespace is trimmed before storing.
			name: "Create email and name with spaces",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.Create(ctx, padded, "  Ad Soyad  ", password))
			},
			want: contractResult{found: true, email: baseEmail, name: "Ad Soyad"},
		},
		{
			// A REAL PRODUCTION BUG: the user must be able to log in with the SAME
			// INPUT (padded) they signed up with.
			name: "ByEmail finds with padded input",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.ByEmail(ctx, padded))
			},
			want: contractResult{found: true, email: baseEmail, name: "Ad Soyad"},
		},
		{
			name: "ByEmail finds with unpadded input",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.ByEmail(ctx, baseEmail))
			},
			want: contractResult{found: true, email: baseEmail, name: "Ad Soyad"},
		},
		{
			// "Email is unique, case-insensitive" is a binding
			// constraint: UNIQUE (lower(email)) in Postgres.
			name: "ByEmail finds with uppercase input",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.ByEmail(ctx, upper))
			},
			want: contractResult{found: true, email: baseEmail, name: "Ad Soyad"},
		},
		{
			// The fake store used to CREATE the SECOND account.
			name: "Create rejects the same email with space/case differences",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.Create(ctx, " "+upper+" ", "Baska", password))
			},
			want: contractResult{err: "email-in-use"},
		},
		{
			name: "ByEmail nonexistent email",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.ByEmail(ctx, "yok-"+uuid.NewString()+"@ornek.com"))
			},
			want: contractResult{err: "user-missing"},
		},
		{
			name: "ByID nonexistent id",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.ByID(ctx, missingID))
			},
			want: contractResult{err: "user-missing"},
		},
		{
			name: "Create short password",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.Create(ctx, "kisa-"+uuid.NewString()+"@ornek.com", "Ad", "kisa"))
			},
			want: contractResult{err: "password-short"},
		},
		{
			name: "Delete removes an existing user",
			call: func(ctx context.Context, d UserStore) contractResult {
				k, err := d.ByEmail(ctx, baseEmail)
				if err != nil {
					return result(nil, err)
				}
				return contractResult{err: contractErr(d.Delete(ctx, k.ID))}
			},
			want: contractResult{},
		},
		{
			name: "ByEmail cannot find a deleted user",
			call: func(ctx context.Context, d UserStore) contractResult {
				return result(d.ByEmail(ctx, baseEmail))
			},
			want: contractResult{err: "user-missing"},
		},
		{
			name: "Delete for a nonexistent user returns ErrUserNotFound",
			call: func(ctx context.Context, d UserStore) contractResult {
				return contractResult{err: contractErr(d.Delete(ctx, missingID))}
			},
			want: contractResult{err: "user-missing"},
		},
	}

	run := func(t *testing.T, store UserStore) {
		ctx := context.Background()
		for _, a := range steps {
			got := a.call(ctx, store)
			if got != a.want {
				t.Errorf("%s: result = %+v, want %+v", a.name, got, a.want)
			}
		}
	}

	t.Run("fake", func(t *testing.T) { run(t, NewFakeUserStore()) })
	t.Run("postgres", func(t *testing.T) { run(t, testPostgresUserStore(t)) })

	// ByID must really find the ID that Create returned. We measure this
	// separately from the steps: IDs cannot be compared across stores.
	idRun := func(t *testing.T, store UserStore) {
		ctx := context.Background()
		k, err := store.Create(ctx, "id-"+uuid.NewString()+"@ornek.com", "Ad", password)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		matched, err := store.ByID(ctx, k.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if matched.ID != k.ID || matched.Email != k.Email {
			t.Errorf("ByID = %+v, want %+v", matched, k)
		}
	}
	t.Run("fake-byid", func(t *testing.T) { idRun(t, NewFakeUserStore()) })
	t.Run("postgres-byid", func(t *testing.T) { idRun(t, testPostgresUserStore(t)) })
}

// FakeUserStore must NOT hand out INTERNAL pointers to the caller. This
// is the SAME flaw that commit 50e323e fixed for FakeTokenStore:
// PostgresUserStore naturally returns a fresh struct because it rescans
// the row on every read, while the fake store has to provide the same
// isolation BY HAND. Otherwise, when the caller mutates the returned
// *User the store gets corrupted, and tests pass while production fails.
func TestFakeUserStore_NoInternalPointers(t *testing.T) {
	store := NewFakeUserStore()
	ctx := context.Background()
	k, err := store.Create(ctx, "kopya@ornek.com", "Ad Soyad", "gecerliSifre12")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 1. Corrupting the record that Create returned must not corrupt the store.
	k.Email = "bozuk@ornek.com"
	k.PasswordHash = ""

	first, err := store.ByEmail(ctx, "kopya@ornek.com")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if first.Email != "kopya@ornek.com" || first.PasswordHash == "" {
		t.Fatalf("mutating the record returned by Create corrupted the store: %+v", first)
	}

	// 2. Corrupting the record that ByEmail returned must not corrupt the store either.
	first.PasswordHash = ""
	first.Name = "degisti"
	second, err := store.ByEmail(ctx, "kopya@ornek.com")
	if err != nil {
		t.Fatalf("ByEmail (second): %v", err)
	}
	if second.PasswordHash == "" || second.Name != "Ad Soyad" {
		t.Errorf("ByEmail returned an internal pointer: %+v", second)
	}

	// 3. The same isolation for ByID.
	withID, err := store.ByID(ctx, second.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	withID.Name = "yine degisti"
	again, err := store.ByID(ctx, second.ID)
	if err != nil {
		t.Fatalf("ByID (second): %v", err)
	}
	if again.Name != "Ad Soyad" {
		t.Errorf("ByID returned an internal pointer: Name = %q", again.Name)
	}
}

// normalizeEmail is the ONLY helper: Create and ByEmail have to go
// through the same path. Normalization does NOT change case (uniqueness
// goes through lower(email) in Postgres).
func TestNormalizeEmail(t *testing.T) {
	statuses := map[string]string{
		"  x@y.com  ":      "x@y.com",
		"x@y.com":          "x@y.com",
		"\tx@y.com\n":      "x@y.com",
		"X@Y.com":          "X@Y.com",
		"   ":              "",
		"a b@y.com":        "a b@y.com",
		" Ad.Soyad@y.com ": "Ad.Soyad@y.com",
	}
	for input, want := range statuses {
		if got := normalizeEmail(input); got != want {
			t.Errorf("normalizeEmail(%q) = %q, want %q", input, got, want)
		}
	}
}
