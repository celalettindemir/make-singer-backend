package identity

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// identityKey turns a (provider, sub) pair into a map key. "\x00" was chosen
// as the separator because it is not expected to appear in provider or sub
// values, so the separator never mixes with the data.
func identityKey(provider, sub string) string {
	return provider + "\x00" + sub
}

// FakeFederatedStore is an in-memory FederatedStore for tests.
//
// CONTRACT: it MUST behave EXACTLY like PostgresFederatedStore (see
// TestFederatedStoreContract). It also SHARES the FakeUserStore:
// CreateFederatedUser creates the user there, and UserByIdentity reads from
// the same store -- just as the Postgres implementation shares a single pool.
type FakeFederatedStore struct {
	mu    sync.Mutex
	users *FakeUserStore
	links map[string]string // identityKey(provider, sub) -> userID
	hooks signupHooks
}

func NewFakeFederatedStore(users *FakeUserStore) *FakeFederatedStore {
	return &FakeFederatedStore{
		users: users,
		links: map[string]string{},
	}
}

func (d *FakeFederatedStore) UserByIdentity(ctx context.Context, provider, sub string) (*User, error) {
	d.mu.Lock()
	userID, ok := d.links[identityKey(provider, sub)]
	d.mu.Unlock()
	if !ok {
		return nil, ErrIdentityNotFound
	}
	// FakeUserStore.ByID already returns a deep copy.
	return d.users.ByID(ctx, userID)
}

func (d *FakeFederatedStore) Link(ctx context.Context, provider, sub, userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.canLinkLocked(provider, sub, userID); err != nil {
		return err
	}
	d.links[identityKey(provider, sub)] = userID
	return nil
}

// canLinkLocked imitates Postgres's TWO unique constraints: the (provider,
// sub) primary key and the (user_id, provider) uniqueness. The caller must
// hold the lock.
func (d *FakeFederatedStore) canLinkLocked(provider, sub, userID string) error {
	if _, ok := d.links[identityKey(provider, sub)]; ok {
		return ErrIdentityInUse
	}
	prefix := provider + "\x00"
	for key, uid := range d.links {
		if uid == userID && strings.HasPrefix(key, prefix) {
			return ErrIdentityInUse
		}
	}
	return nil
}

func (d *FakeFederatedStore) CreateFederatedUser(
	ctx context.Context, provider, sub, email, name string, emailVerified bool,
) (*User, error) {
	k, err := d.createLocked(provider, sub, email, name, emailVerified)
	if err != nil {
		return nil, err
	}
	d.hooks.run(ctx, k) // after the "commit", outside the lock
	return k, nil
}

func (d *FakeFederatedStore) createLocked(provider, sub, email, name string, emailVerified bool) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.canLinkLocked(provider, sub, ""); err != nil {
		return nil, err
	}
	k, err := d.users.addFederated(email, name, emailVerified)
	if err != nil {
		return nil, err
	}
	d.links[identityKey(provider, sub)] = k.ID
	return k, nil
}

// OnSignup registers a hook that runs after a federated user is created.
func (d *FakeFederatedStore) OnSignup(h SignupHook) { d.hooks.add(h) }

func (d *FakeFederatedStore) ProvidersByUserID(ctx context.Context, userID string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var providers []string
	for key, uid := range d.links {
		if uid != userID {
			continue
		}
		idx := strings.IndexByte(key, 0)
		providers = append(providers, key[:idx])
	}
	sort.Strings(providers)
	return providers, nil
}

// addFederated adds a passwordless (federated-only) user to the FakeUserStore.
// Like PostgresUserStore's federated path, it leaves password_hash EMPTY:
// PasswordMatches therefore returns false for every password. It applies the
// SAME email uniqueness rule as Create (case-insensitive).
func (s *FakeUserStore) addFederated(email, name string, emailVerified bool) (*User, error) {
	key := fakeEmailKey(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byEmail[key]; ok {
		return nil, ErrEmailInUse
	}
	k := &User{
		ID:            uuid.NewString(),
		Email:         normalizeEmail(email),
		EmailVerified: emailVerified,
		Name:          strings.TrimSpace(name),
		CreatedAt:     time.Now().UTC(),
	}
	s.byEmail[key] = k
	s.byID[k.ID] = k
	return copyUser(k), nil
}

var _ FederatedStore = (*FakeFederatedStore)(nil)
