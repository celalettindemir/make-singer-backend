package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func newRefresh(userID string) *RefreshRecord {
	return &RefreshRecord{
		UserID:    userID,
		ClientID:  "makesinger-mobil",
		Scopes:    []string{"openid", "offline_access"},
		Audience:  []string{"makesinger-mobil"},
		AMR:       []string{"pwd"},
		AuthTime:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
}

func TestTokenStore_CreateAndGetRefresh(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	token, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	if token == "" {
		t.Fatal("an empty token was returned")
	}
	record, err := store.GetRefresh(ctx, token)
	if err != nil {
		t.Fatalf("GetRefresh: %v", err)
	}
	if record.UserID != "k1" {
		t.Errorf("UserID = %q", record.UserID)
	}
	if record.FamilyID == "" {
		t.Error("FamilyID is empty")
	}
}

// Rotation: every use produces a new token and the old one dies.
func TestTokenStore_RotateInvalidatesOld(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	old, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	fresh, err := store.RotateRefresh(ctx, old, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	if fresh == old {
		t.Fatal("the token did not rotate, the same value came back")
	}
	if _, err := store.GetRefresh(ctx, fresh); err != nil {
		t.Errorf("could not read the new token: %v", err)
	}
}

// Stolen-token detection: if a used token is presented again the WHOLE
// family is revoked. Rejecting only the presented token is not enough; the
// attacker may already have taken the new token.
func TestTokenStore_UsedTokenRevokesFamily(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	firstRecord, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	second, err := store.RotateRefresh(ctx, firstRecord, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	// The attacker presents the old token again.
	if _, err := store.RotateRefresh(ctx, firstRecord, newRefresh("k1")); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("error = %v, want ErrTokenReused", err)
	}
	// The valid token in the real user's hands must be dead now too.
	if _, err := store.GetRefresh(ctx, second); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("family was not revoked, the second token is still valid (err=%v)", err)
	}
}

func TestTokenStore_ExpiredRefreshRejected(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	k := newRefresh("k1")
	k.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	token, err := store.CreateRefresh(ctx, k)
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	if _, err := store.GetRefresh(ctx, token); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("error = %v, want ErrTokenNotFound", err)
	}
}

func TestTokenStore_MissingRefresh(t *testing.T) {
	store := NewFakeTokenStore()
	if _, err := store.GetRefresh(context.Background(), "uydurma"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("error = %v, want ErrTokenNotFound", err)
	}
}

// Logout: all of the user's refresh tokens die.
func TestTokenStore_RevokeUserRevokesAll(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	a, _ := store.CreateRefresh(ctx, newRefresh("k1"))
	b, _ := store.CreateRefresh(ctx, newRefresh("k1"))
	c, _ := store.CreateRefresh(ctx, newRefresh("k2"))
	// The same user, ANOTHER client: logout must kill only that client's
	// tokens.
	otherClient := newRefresh("k1")
	otherClient.ClientID = "makesinger-web"
	d, _ := store.CreateRefresh(ctx, otherClient)
	if err := store.RevokeUser(ctx, "k1", "makesinger-mobil"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	for name, token := range map[string]string{"a": a, "b": b} {
		if _, err := store.GetRefresh(ctx, token); !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("%s is still valid", name)
		}
	}
	if _, err := store.GetRefresh(ctx, c); err != nil {
		t.Errorf("another user's token was revoked: %v", err)
	}
	if _, err := store.GetRefresh(ctx, d); err != nil {
		t.Errorf("another client's token was revoked: %v", err)
	}
}

// The digest of the token must be stored, not the token itself.
func TestTokenStore_NoPlaintextToken(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	token, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	for _, record := range store.AllRecords() {
		if string(record.TokenHash) == token {
			t.Fatal("the token was stored in plain text")
		}
	}
	// Looking field by field is not enough (the sha256 raw bytes can never
	// equal a base64 string, so that comparison passes for free). We serialize
	// the WHOLE record and verify that the token does not appear in any field.
	data, err := json.Marshal(store.AllRecords())
	if err != nil {
		t.Fatalf("could not serialize records: %v", err)
	}
	if strings.Contains(string(data), token) {
		t.Fatalf("the token leaked in plain text into a field in the record: %s", data)
	}
	// The map keys must be digests too: if the fake store keeps the token as a
	// key, the plain text still sits in memory.
	for key := range store.refresh {
		if strings.Contains(key, token) {
			t.Fatal("the token was stored in plain text in the map key")
		}
	}
}

// Rotation must not take over another user's token: if there is a bug in
// the calling layer, no foreign record should enter the same family.
func TestTokenStore_RotateChecksOwnership(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	token, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	if _, err := store.RotateRefresh(ctx, token, newRefresh("k2")); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("error for another user = %v, want ErrTokenNotFound", err)
	}
	otherClient := newRefresh("k1")
	otherClient.ClientID = "makesinger-web"
	if _, err := store.RotateRefresh(ctx, token, otherClient); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("error for another client = %v, want ErrTokenNotFound", err)
	}
	// Rejected attempts must not have consumed the token.
	if _, err := store.GetRefresh(ctx, token); err != nil {
		t.Errorf("the valid token was revoked after the rejected attempts: %v", err)
	}
}

// The fake store must not hand out an INTERNAL pointer to the caller:
// Postgres returns a fresh row on every read, so a caller's mutation does not
// corrupt the database, and the fake store must give the same isolation.
func TestFakeTokenStore_ReturnsCopies(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	input := newRefresh("k1")
	token, err := store.CreateRefresh(ctx, input)
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	// Changing the slice in the caller's hands must not affect the store.
	input.Scopes[0] = "BOZULDU"

	record, err := store.GetRefresh(ctx, token)
	if err != nil {
		t.Fatalf("GetRefresh: %v", err)
	}
	if record.Scopes[0] != "openid" {
		t.Errorf("Scopes is shared with the caller: %v", record.Scopes)
	}
	// Corrupting the returned record must not affect the store either.
	record.Scopes[0] = "BOZULDU"
	record.TokenHash[0] ^= 0xff
	record.UserID = "saldirgan"
	cancel := time.Now().UTC()
	record.RevokedAt = &cancel

	again, err := store.GetRefresh(ctx, token)
	if err != nil {
		t.Fatalf("mutating the returned record corrupted the store: %v", err)
	}
	if again.Scopes[0] != "openid" || again.UserID != "k1" || again.RevokedAt != nil {
		t.Errorf("internal record picked up the mutation: %+v", again)
	}

	// The same isolation for the access record.
	scope := []string{"openid"}
	if err := store.SaveAccess(ctx, "at1", "k1", "makesinger-mobil", scope, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	scope[0] = "BOZULDU"
	accessRecord, err := store.GetAccess(ctx, "at1")
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if accessRecord.Scopes[0] != "openid" {
		t.Errorf("access Scopes is shared with the caller: %v", accessRecord.Scopes)
	}
	accessRecord.UserID = "saldirgan"
	if accessRecord2, err := store.GetAccess(ctx, "at1"); err != nil || accessRecord2.UserID != "k1" {
		t.Errorf("internal access record picked up the mutation: %+v (err=%v)", accessRecord2, err)
	}
}

// AllRecords must return a copy too.
func TestFakeTokenStore_AllRecordsReturnsCopies(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	token, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	for _, record := range store.AllRecords() {
		record.UserID = "saldirgan"
		record.Scopes[0] = "BOZULDU"
	}
	record, err := store.GetRefresh(ctx, token)
	if err != nil {
		t.Fatalf("GetRefresh: %v", err)
	}
	if record.UserID != "k1" || record.Scopes[0] != "openid" {
		t.Errorf("AllRecords returned an internal pointer: %+v", record)
	}
}

func TestTokenStore_SaveAndGetAccess(t *testing.T) {
	store := NewFakeTokenStore()
	ctx := context.Background()
	last := time.Now().UTC().Add(15 * time.Minute)
	if err := store.SaveAccess(ctx, "at1", "k1", "makesinger-mobil", []string{"openid"}, last); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	record, err := store.GetAccess(ctx, "at1")
	if err != nil {
		t.Fatalf("GetAccess: %v", err)
	}
	if record.UserID != "k1" {
		t.Errorf("UserID = %q", record.UserID)
	}
	if _, err := store.GetAccess(ctx, "yok"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("error = %v, want ErrTokenNotFound", err)
	}
}
