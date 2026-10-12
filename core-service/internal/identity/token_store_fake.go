package identity

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
)

// FakeTokenStore is an in-memory TokenStore for tests. The rotation, reuse
// detection and family revocation logic works in EXACTLY the same order
// and under the same conditions as PostgresTokenStore; the tests measure
// both implementations against the same contract.
type FakeTokenStore struct {
	mu      sync.Mutex
	access  map[string]*AccessRecord
	refresh map[string]*RefreshRecord // key: the string form of token_hash
}

func NewFakeTokenStore() *FakeTokenStore {
	return &FakeTokenStore{
		access:  map[string]*AccessRecord{},
		refresh: map[string]*RefreshRecord{},
	}
}

func (s *FakeTokenStore) SaveAccess(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error {
	// On the Redis side no record is written when TTL <= 0; same here.
	if time.Until(expiresAt) <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access[id] = &AccessRecord{
		ID:        id,
		UserID:    userID,
		ClientID:  clientID,
		Scopes:    slices.Clone(scopes),
		ExpiresAt: expiresAt,
	}
	return nil
}

func (s *FakeTokenStore) GetAccess(ctx context.Context, id string) (*AccessRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.access[id]
	if !ok {
		return nil, ErrTokenNotFound
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		return nil, ErrTokenNotFound
	}
	return copyAccess(record), nil
}

// DeleteAccess removes the record the same way the Postgres implementation
// does; if there is no record it silently counts as success.
func (s *FakeTokenStore) DeleteAccess(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.access, id)
	return nil
}

// CreateRefresh starts a new FAMILY: family_id is freshly generated.
func (s *FakeTokenStore) CreateRefresh(ctx context.Context, r *RefreshRecord) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.insertLocked(r, uuid.NewString(), token)
	return token, nil
}

// insertLocked is called while s.mu is held. It fills the same fields as
// the INSERT in Postgres; the hash of the token is stored, not the token
// itself.
func (s *FakeTokenStore) insertLocked(r *RefreshRecord, familyID, token string) {
	hash := hashToken(token)
	s.refresh[string(hash)] = &RefreshRecord{
		ID:        uuid.NewString(),
		FamilyID:  familyID,
		UserID:    r.UserID,
		ClientID:  r.ClientID,
		TokenHash: hash,
		Scopes:    slices.Clone(r.Scopes),
		Audience:  slices.Clone(r.Audience),
		AMR:       slices.Clone(r.AMR),
		AuthTime:  r.AuthTime,
		ExpiresAt: r.ExpiresAt,
	}
}

// RotateRefresh decides in the same order as the Postgres implementation:
// no record -> revoked -> used (family revocation) -> expired -> rotation.
// Since it is in-memory, the single lock stands in for the transaction +
// FOR UPDATE row lock in Postgres.
func (s *FakeTokenStore) RotateRefresh(ctx context.Context, presented string, fresh *RefreshRecord) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.refresh[string(hashToken(presented))]
	if !ok {
		return "", ErrTokenNotFound
	}
	// Order matters: a revoked token is already dead (once the family has
	// been revoked), so the reuse alarm must not fire again; the reuse
	// check comes after it.
	if record.RevokedAt != nil {
		return "", ErrTokenNotFound
	}
	if record.UsedAt != nil {
		// Reuse: kill the whole family and return a distinct error.
		s.revokeFamilyLocked(record.FamilyID)
		return "", ErrTokenReused
	}
	// Ownership: if the calling layer has a bug, another user's (or
	// another client's) record must not be added to the same family. A
	// cheap defense: on a mismatch the token is treated as if it did not
	// exist.
	if record.UserID != fresh.UserID || record.ClientID != fresh.ClientID {
		return "", ErrTokenNotFound
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		return "", ErrTokenNotFound
	}
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	record.UsedAt = &now
	// The new record is added with the SAME family_id: the trace of the chain
	// is preserved so that if a reuse is seen later the whole chain can be
	// revoked.
	s.insertLocked(fresh, record.FamilyID, token)
	return token, nil
}

// GetRefresh decides in EXACTLY the same order as the Postgres
// implementation: no record -> ErrTokenNotFound, revoked ->
// ErrTokenNotFound, used -> ErrTokenReused (reuse), expired ->
// ErrTokenNotFound. A divergence between the two implementations here has
// caused a defect before; the order and the errors must be kept as they
// are.
func (s *FakeTokenStore) GetRefresh(ctx context.Context, presented string) (*RefreshRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.refresh[string(hashToken(presented))]
	if !ok {
		return nil, ErrTokenNotFound
	}
	if record.RevokedAt != nil {
		return nil, ErrTokenNotFound
	}
	if record.UsedAt != nil {
		return nil, ErrTokenReused
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		return nil, ErrTokenNotFound
	}
	return copyRefresh(record), nil
}

// RefreshFamilyID has the same contract as the Postgres implementation: the
// family ID from the raw token, without validity filtering.
func (s *FakeTokenStore) RefreshFamilyID(ctx context.Context, presented string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.refresh[string(hashToken(presented))]
	if !ok {
		return "", ErrTokenNotFound
	}
	return record.FamilyID, nil
}

// GetRefreshByID satisfies the same contract as the Postgres
// implementation: it looks up by ID (not by the token itself) and does not
// filter for validity. Since the fake store keeps the token (its hash) as
// the key, looking up by ID requires a linear scan; that is enough for
// tests.
func (s *FakeTokenStore) GetRefreshByID(ctx context.Context, id string) (*RefreshRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.refresh {
		if record.ID == id {
			return copyRefresh(record), nil
		}
	}
	return nil, ErrTokenNotFound
}

func (s *FakeTokenStore) RevokeFamily(ctx context.Context, familyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokeFamilyLocked(familyID)
	return nil
}

// revokeFamilyLocked is called while s.mu is held. Postgres equivalent:
// UPDATE ... WHERE family_id = $1 AND revoked_at IS NULL
func (s *FakeTokenStore) revokeFamilyLocked(familyID string) {
	now := time.Now().UTC()
	for _, record := range s.refresh {
		if record.FamilyID == familyID && record.RevokedAt == nil {
			revokedAt := now
			record.RevokedAt = &revokedAt
		}
	}
}

// RevokeUser: Postgres equivalent:
// UPDATE ... WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL
func (s *FakeTokenStore) RevokeUser(ctx context.Context, userID, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, record := range s.refresh {
		if record.UserID == userID && record.ClientID == clientID && record.RevokedAt == nil {
			revokedAt := now
			record.RevokedAt = &revokedAt
		}
	}
	return nil
}

// AllRecords is for tests to inspect the stored form.
func (s *FakeTokenStore) AllRecords() []*RefreshRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := make([]*RefreshRecord, 0, len(s.refresh))
	for _, record := range s.refresh {
		all = append(all, copyRefresh(record))
	}
	return all
}

// copyRefresh / copyAccess / copyTime: the fake store never hands an
// INTERNAL pointer to the caller. PostgresTokenStore naturally returns a
// fresh struct because it rescans the row on every read; if the caller
// mutates the returned record (e.g. RevokedAt = nil) the database is not
// corrupted. The fake store has to provide the same isolation by hand,
// otherwise production drifts while the tests pass.
func copyRefresh(r *RefreshRecord) *RefreshRecord {
	clone := *r
	clone.TokenHash = slices.Clone(r.TokenHash)
	clone.Scopes = slices.Clone(r.Scopes)
	clone.Audience = slices.Clone(r.Audience)
	clone.AMR = slices.Clone(r.AMR)
	clone.UsedAt = copyTime(r.UsedAt)
	clone.RevokedAt = copyTime(r.RevokedAt)
	return &clone
}

func copyAccess(r *AccessRecord) *AccessRecord {
	clone := *r
	clone.Scopes = slices.Clone(r.Scopes)
	return &clone
}

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	r := *t
	return &r
}
