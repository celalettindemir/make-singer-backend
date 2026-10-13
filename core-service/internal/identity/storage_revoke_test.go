package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// trackingTokenStore tracks whether RevokeUser was called.
type trackingTokenStore struct {
	*FakeTokenStore
	revokeUserCalls int
}

func (s *trackingTokenStore) RevokeUser(ctx context.Context, userID, clientID string) error {
	s.revokeUserCalls++
	return s.FakeTokenStore.RevokeUser(ctx, userID, clientID)
}

// newTestStorageWithTokens is the variant of newTestStorage in
// storage_test.go that is built with a given TokenStore (there the fixed
// NewFakeTokenStore is used).
func newTestStorageWithTokens(t *testing.T, tokens TokenStore) *Storage {
	t.Helper()
	key, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	return NewStorage(testAuthCfg(), NewFakeUserStore(), tokens, nil, key)
}

// M2: a parameterless GET /end_session fell into TerminateSession with
// an empty userID/clientID and sent the empty string to Postgres as a
// uuid: 500 + "invalid input syntax for type uuid" (SQLSTATE 22P02) +
// an ERROR log line on EVERY request (log pollution + small
// amplification). There is no token to revoke with an empty ID anyway,
// so returning early is correct.
func TestStorage_TerminateSessionEmptyIDsSkipStore(t *testing.T) {
	tracking := &trackingTokenStore{FakeTokenStore: NewFakeTokenStore()}
	d := newTestStorageWithTokens(t, tracking)
	ctx := context.Background()

	statuses := []struct{ userID, clientID string }{
		{"", ""},
		{"", "makesinger-mobil"},
		{"k1", ""},
	}
	for _, c := range statuses {
		if err := d.TerminateSession(ctx, c.userID, c.clientID); err != nil {
			t.Errorf("TerminateSession(%q, %q) = %v, beklenen nil", c.userID, c.clientID, err)
		}
	}
	if tracking.revokeUserCalls != 0 {
		t.Errorf("bos kimlikle depo %d kez cagrildi, beklenen 0", tracking.revokeUserCalls)
	}

	// NEGATIVE CONTROL: the guard must not cut off a VALID call too.
	if err := d.TerminateSession(ctx, "k1", "makesinger-mobil"); err != nil {
		t.Fatalf("gecerli TerminateSession = %v", err)
	}
	if tracking.revokeUserCalls != 1 {
		t.Errorf("gecerli cagri sonrasi sayac = %d, beklenen 1", tracking.revokeUserCalls)
	}
}

// M7: GetRefreshTokenInfo did NOT REVOKE the family when it saw
// ErrTokenReused. It was not exploitable (the attacker does not gain a
// token in the /revoke flow), but while the whole architecture rests on
// the principle "reuse => kill the family", a detection channel was silently missing.
func TestStorage_GetRefreshTokenInfoReuseRevokesFamily(t *testing.T) {
	store := NewFakeTokenStore()
	d := newTestStorageWithTokens(t, store)
	ctx := context.Background()

	old, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	rotated, err := store.RotateRefresh(ctx, old, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	// After rotation the new token must be valid (the family is still alive).
	if _, err := store.GetRefresh(ctx, rotated); err != nil {
		t.Fatalf("rotasyon sonrasi yeni jeton gecersiz: %v", err)
	}

	// Present the USED token: by the library contract it must return
	// op.ErrInvalidRefreshToken (the behavior must NOT CHANGE; /revoke
	// should keep returning 200 per RFC 7009 §2.2).
	if _, _, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", old); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}

	// ... and the FAMILY must be revoked: the new token is now invalid too.
	if _, err := store.GetRefresh(ctx, rotated); err == nil {
		t.Error("yeniden kullanimdan sonra aile iptal edilmedi: yeni jeton hala gecerli")
	}
}

// NEGATIVE CONTROL: the family revocation must happen ONLY on reuse.
// No family must die for an unknown token, otherwise the test above
// would also pass in the "every call kills the family" case.
func TestStorage_GetRefreshTokenInfoUnknownTokenNoRevoke(t *testing.T) {
	store := NewFakeTokenStore()
	d := newTestStorageWithTokens(t, store)
	ctx := context.Background()

	valid, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, _, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", "boyle-bir-jeton-yok"); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
	if _, err := store.GetRefresh(ctx, valid); err != nil {
		t.Errorf("bilinmeyen jeton yuzunden gecerli jeton iptal edildi: %v", err)
	}
}

// For a VALID refresh token GetRefreshTokenInfo must still return the
// right info: the M7 fix must not break the happy path.
func TestStorage_GetRefreshTokenInfoReturnsValidToken(t *testing.T) {
	store := NewFakeTokenStore()
	d := newTestStorageWithTokens(t, store)
	ctx := context.Background()

	token, err := store.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	userID, tokenID, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", token)
	if err != nil {
		t.Fatalf("GetRefreshTokenInfo: %v", err)
	}
	if userID != "k1" {
		t.Errorf("userID = %q, beklenen k1", userID)
	}
	if tokenID == "" {
		t.Error("tokenID bos dondu")
	}
}

// M3: the discovery document must advertise ONLY the flows we really
// support. The library (v3.51.8) does not read these fields from the
// configuration, it produces them as CONSTANTS; op.Config has NO field
// to restrict them, so we fix the document before publishing it.
func TestTrimDiscovery_RemovesUnsupportedFlows(t *testing.T) {
	// Imitate the document the library produces.
	doc := &oidc.DiscoveryConfiguration{
		ResponseTypesSupported: []string{"code", "id_token", "id_token token"},
		GrantTypesSupported: []oidc.GrantType{
			oidc.GrantTypeCode,
			oidc.GrantTypeImplicit,
			oidc.GrantTypeRefreshToken,
			oidc.GrantTypeBearer,
		},
		DeviceAuthorizationEndpoint: "https://ornek.dev/device_authorization",
		// A field the fix must NOT touch.
		CodeChallengeMethodsSupported: []oidc.CodeChallengeMethod{oidc.CodeChallengeMethodS256},
	}
	trimDiscovery(doc)

	if len(doc.ResponseTypesSupported) != 1 || doc.ResponseTypesSupported[0] != "code" {
		t.Errorf("response_types_supported = %v, beklenen [code]", doc.ResponseTypesSupported)
	}
	for _, forbidden := range []oidc.GrantType{oidc.GrantTypeImplicit, oidc.GrantTypeBearer, oidc.GrantTypeDeviceCode} {
		for _, g := range doc.GrantTypesSupported {
			if g == forbidden {
				t.Errorf("grant_types_supported desteklenmeyen %q iceriyor: %v", forbidden, doc.GrantTypesSupported)
			}
		}
	}
	// The two we support must REMAIN (the trimming must not cut too much).
	for _, required := range []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken} {
		wasFound := false
		for _, g := range doc.GrantTypesSupported {
			if g == required {
				wasFound = true
			}
		}
		if !wasFound {
			t.Errorf("grant_types_supported %q icermiyor: %v", required, doc.GrantTypesSupported)
		}
	}
	if doc.DeviceAuthorizationEndpoint != "" {
		t.Errorf("device_authorization_endpoint = %q, beklenen bos", doc.DeviceAuthorizationEndpoint)
	}
	if len(doc.CodeChallengeMethodsSupported) != 1 {
		t.Errorf("code_challenge_methods_supported bozuldu: %v", doc.CodeChallengeMethodsSupported)
	}
}
