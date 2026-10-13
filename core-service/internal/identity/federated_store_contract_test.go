package identity

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// federatedSetup runs the same contract against both implementations.
type federatedSetup struct {
	name  string
	store FederatedStore
	users UserStore
}

// federatedSetupFactory builds a federatedSetup with the t of ITS OWN
// subtest. The connection/Skip is NEVER attempted until this factory is
// called: otherwise (as it used to be) t.Skip ends the parent test
// function's goroutine with runtime.Goexit, the range loop is never
// entered, and ALL subtests -- the fake included -- are silently
// skipped. Since CI has no Postgres service, that would also wipe out the
// coverage of the fake, which has to run without a network.
type federatedSetupFactory struct {
	name  string
	build func(t *testing.T) federatedSetup
}

// federatedStoreSetups always adds the fake factory; the Postgres factory
// connects with EXACTLY THE SAME pattern as testPostgresUserStore: the
// KIMLIK_TEST_DB_URL environment variable, else the default local DSN,
// Connect + Migrate, t.Skip if it cannot connect, t.Cleanup(pool.Close).
// Postgres is persistent, so the federated identities and users are
// cleaned on every run. The connection attempt is NOT MADE until the
// subtest t inside the calling t.Run is given -- so a missing Postgres
// skips only the "postgres" subtest, and the "fake" subtest runs anyway.
func federatedStoreSetups() []federatedSetupFactory {
	return []federatedSetupFactory{
		{
			name: "fake",
			build: func(t *testing.T) federatedSetup {
				fakeUsers := NewFakeUserStore()
				return federatedSetup{
					name:  "fake",
					store: NewFakeFederatedStore(fakeUsers),
					users: fakeUsers,
				}
			},
		},
		{
			name: "postgres",
			build: func(t *testing.T) federatedSetup {
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
				if _, err := pool.Exec(ctx, `TRUNCATE federe_kimlikler, users CASCADE`); err != nil {
					t.Fatalf("could not clean tables: %v", err)
				}
				return federatedSetup{
					name:  "postgres",
					store: NewPostgresFederatedStore(pool),
					users: NewPostgresUserStore(pool),
				}
			},
		},
	}
}

func TestFederatedStore_Contract(t *testing.T) {
	for _, setup := range federatedStoreSetups() {
		t.Run(setup.name, func(t *testing.T) {
			k := setup.build(t)
			ctx := context.Background()

			// An unknown identity returns ErrIdentityNotFound, not a nil user.
			if _, err := k.store.UserByIdentity(ctx, "google", "yok-123"); !errors.Is(err, ErrIdentityNotFound) {
				t.Fatalf("for an unknown identity err = %v, want ErrIdentityNotFound", err)
			}

			// Creating a federated user: the password hash must be EMPTY, so
			// PasswordMatches must return false for every password.
			user, err := k.store.CreateFederatedUser(ctx, "google", "sub-1", "a@ornek.com", "Ali", true)
			if err != nil {
				t.Fatalf("could not create federated user: %v", err)
			}
			if user.PasswordHash != "" {
				t.Error("a password hash was written for the federated account")
			}
			if user.PasswordMatches("herhangi-bir-sifre-123") {
				t.Error("can sign in with a password to an account that has none")
			}
			if !user.EmailVerified {
				t.Error("emailVerified=true was given but EmailVerified is false")
			}

			// The created identity can now be found and returns the SAME user.
			matched, err := k.store.UserByIdentity(ctx, "google", "sub-1")
			if err != nil {
				t.Fatalf("could not read identity: %v", err)
			}
			if matched.ID != user.ID {
				t.Errorf("user id = %q, want %q", matched.ID, user.ID)
			}

			// Provider separation: the same sub value at another provider is NOT
			// the same person.
			if _, err := k.store.UserByIdentity(ctx, "apple", "sub-1"); !errors.Is(err, ErrIdentityNotFound) {
				t.Errorf("for apple/sub-1 err = %v, want ErrIdentityNotFound", err)
			}

			// Linking to an existing (withPassword) account.
			withPassword, err := k.users.Create(ctx, "b@ornek.com", "Veli", "cok-uzun-bir-sifre")
			if err != nil {
				t.Fatalf("could not create account with password: %v", err)
			}
			if err := k.store.Link(ctx, "apple", "apple-sub-9", withPassword.ID); err != nil {
				t.Fatalf("link: %v", err)
			}
			linked, err := k.store.UserByIdentity(ctx, "apple", "apple-sub-9")
			if err != nil {
				t.Fatalf("could not read linked identity: %v", err)
			}
			if linked.ID != withPassword.ID {
				t.Error("linked identity returns the wrong user")
			}

			// The SAME (provider, sub) cannot be linked to ANOTHER account:
			// otherwise one Google account would be linked to two of our accounts
			// and it would be unclear which one a login lands on.
			if err := k.store.Link(ctx, "apple", "apple-sub-9", user.ID); !errors.Is(err, ErrIdentityInUse) {
				t.Errorf("second link err = %v, want ErrIdentityInUse", err)
			}

			// A user cannot be linked to the same provider twice.
			if err := k.store.Link(ctx, "apple", "apple-sub-FARKLI", withPassword.ID); !errors.Is(err, ErrIdentityInUse) {
				t.Errorf("second link to the same provider err = %v, want ErrIdentityInUse", err)
			}

			// ProvidersByUserID feeds the text of K1's 409 page.
			providers, err := k.store.ProvidersByUserID(ctx, withPassword.ID)
			if err != nil {
				t.Fatalf("could not read providers: %v", err)
			}
			if len(providers) != 1 || providers[0] != "apple" {
				t.Errorf("providers = %v, want [apple]", providers)
			}
		})
	}
}
