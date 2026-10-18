package identity

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// FakeUserStore is an in-memory UserStore for tests.
//
// CONTRACT: it must behave EXACTLY like PostgresUserStore (see
// TestUserStore_Contract). A fake store that does not match lets
// production drift while the tests pass; on this branch exactly this
// divergence produced three separate defects:
//
//   - Create/ByEmail did not normalize the email (production trims), so
//     the Email/Name fields were stored DIFFERENTLY from production.
//   - Case-insensitive uniqueness was missing: for " e@t " and "e@t"
//     Postgres returned ErrEmailInUse while the fake store created a
//     SECOND account.
//   - ByEmail/ByID returned an INTERNAL pointer; if the caller mutated
//     the returned *User the store was corrupted. PostgresUserStore
//     naturally returns a fresh struct because it rescans the row on every
//     read, so the fake store has to provide the same isolation BY HAND
//     (same reason as copyRefresh/copyAccess in the token stores).
type FakeUserStore struct {
	mu      sync.Mutex
	byEmail map[string]*User
	byID    map[string]*User
	hooks   signupHooks
}

func NewFakeUserStore() *FakeUserStore {
	return &FakeUserStore{
		byEmail: map[string]*User{},
		byID:    map[string]*User{},
	}
}

// fakeEmailKey imitates the "UNIQUE (lower(email))" constraint in
// Postgres: first the normalization that production applies (trim), then
// case insensitivity.
func fakeEmailKey(email string) string {
	return strings.ToLower(normalizeEmail(email))
}

func (s *FakeUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	created, err := s.createLocked(email, name, hash)
	if err != nil {
		return nil, err
	}
	// Hooks run after the "commit", outside the lock (a hook may call back).
	s.hooks.run(ctx, created)
	return created, nil
}

func (s *FakeUserStore) createLocked(email, name, hash string) (*User, error) {
	key := fakeEmailKey(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byEmail[key]; ok {
		return nil, ErrEmailInUse
	}
	u := &User{
		ID: uuid.NewString(),
		// The SAME normalization as PostgresUserStore.Create in production.
		Email:        normalizeEmail(email),
		Name:         strings.TrimSpace(name),
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	s.byEmail[key] = u
	s.byID[u.ID] = u
	return copyUser(u), nil
}

// OnSignup registers a hook that runs after a user is created.
func (s *FakeUserStore) OnSignup(h SignupHook) { s.hooks.add(h) }

func (s *FakeUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.byEmail[fakeEmailKey(email)]; ok {
		return copyUser(u), nil
	}
	return nil, ErrUserNotFound
}

func (s *FakeUserStore) ByID(ctx context.Context, id string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.byID[id]; ok {
		return copyUser(u), nil
	}
	return nil, ErrUserNotFound
}

func (s *FakeUserStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, found := s.byID[id]
	if !found {
		return ErrUserNotFound
	}
	delete(s.byID, id)
	for key, candidate := range s.byEmail {
		if candidate.ID == u.ID {
			delete(s.byEmail, key)
		}
	}
	return nil
}

// copyUser returns a shallow copy of the record so that no INTERNAL
// pointer is handed to the caller. User consists only of value fields
// (string, bool, time.Time), so a shallow copy is enough; if a slice or
// pointer field is added it has to be cloned here too.
func copyUser(u *User) *User {
	if u == nil {
		return nil
	}
	clone := *u
	return &clone
}
