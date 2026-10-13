package identity

import (
	"context"
	"sync"
	"time"
)

// FakeSessionStore is the in-memory counterpart of SessionStore. Its purpose
// is for tests to measure the cookie binding OFFLINE (without Redis); the
// same pattern as the fakes of the user/token stores.
type FakeSessionStore struct {
	mu      sync.Mutex
	entries map[string]fakeSessionEntry
}

type fakeSessionEntry struct {
	binding   SessionBinding
	expiresAt time.Time
}

func NewFakeSessionStore() *FakeSessionStore {
	return &FakeSessionStore{entries: map[string]fakeSessionEntry{}}
}

var _ SessionStore = (*FakeSessionStore)(nil)

func (d *FakeSessionStore) WriteSession(_ context.Context, session string, binding SessionBinding, ttl time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries[session] = fakeSessionEntry{binding: binding, expiresAt: time.Now().Add(ttl)}
	return nil
}

func (d *FakeSessionStore) ReadSession(_ context.Context, session string) (SessionBinding, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, found := d.entries[session]
	if !found || time.Now().After(entry.expiresAt) {
		return SessionBinding{}, ErrSessionNotFound
	}
	return entry.binding, nil
}
