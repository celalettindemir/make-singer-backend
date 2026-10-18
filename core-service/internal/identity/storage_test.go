package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func newTestStorage(t *testing.T) *Storage {
	t.Helper()
	a, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	return NewStorage(testAuthCfg(), NewFakeUserStore(), NewFakeTokenStore(), nil, a)
}

func TestStorage_SatisfiesInterface(t *testing.T) {
	var _ op.Storage = newTestStorage(t)
}

// A token must not be obtainable with an unconfigured client ID.
func TestStorage_UnknownClientRejected(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	if _, err := d.GetClientByClientID(ctx, "baska-uygulama"); err == nil {
		t.Error("an undefined client was accepted")
	}
	if _, err := d.GetClientByClientID(ctx, "makesinger-mobil"); err != nil {
		t.Errorf("a defined client was rejected: %v", err)
	}
}

// A public client carries no secret; verification with a secret must ALWAYS
// fail. If it succeeded, the client could be impersonated with an empty secret.
func TestStorage_ClientSecretAlwaysRejected(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	for _, secret := range []string{"", "herhangi"} {
		if err := d.AuthorizeClientIDSecret(ctx, "makesinger-mobil", secret); err == nil {
			t.Errorf("secret %q was accepted", secret)
		}
	}
}

// A reused refresh token must be converted to the error the library expects;
// otherwise the client gets a 500 and the cause is unclear.
func TestStorage_ReuseYieldsInvalidRefresh(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	firstRecord, err := d.tokens.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	if _, err := d.tokens.RotateRefresh(ctx, firstRecord, newRefresh("k1")); err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	if _, err := d.TokenRequestByRefreshToken(ctx, firstRecord); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("error = %v, want op.ErrInvalidRefreshToken", err)
	}
}

func TestStorage_JWTProfileUnsupported(t *testing.T) {
	d := newTestStorage(t)
	if _, err := d.ValidateJWTProfileScopes(context.Background(), "k1", []string{"openid"}); err == nil {
		t.Error("JWT profile was accepted")
	}
}

// GetRefreshTokenInfo MUST return op.ErrInvalidRefreshToken for an invalid
// token as well (library contract); if it returns another error the caller
// misinterprets it.
func TestStorage_GetRefreshTokenInfoInvalidReturnsInvalidRefresh(t *testing.T) {
	d := newTestStorage(t)
	if _, _, err := d.GetRefreshTokenInfo(context.Background(), "makesinger-mobil", "uydurma"); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("error = %v, want op.ErrInvalidRefreshToken", err)
	}
}

// GetRefreshTokenInfo must return the userID and tokenID for a valid refresh
// token.
func TestStorage_GetRefreshTokenInfoValid(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	token, err := d.tokens.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	userID, tokenID, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", token)
	if err != nil {
		t.Fatalf("GetRefreshTokenInfo: %v", err)
	}
	if userID != "k1" {
		t.Errorf("userID = %q, want k1", userID)
	}
	if tokenID == "" {
		t.Error("tokenID is empty")
	}
}

// If RevokeToken targets a refresh token the WHOLE FAMILY must die: another
// token of the same family must now be invalid too.
//
// CRITICAL: the library passes RevokeToken the tokenID returned by
// GetRefreshTokenInfo, NOT the RAW TOKEN (see zitadel/oidc pkg/op/
// token_revocation.go:48-55 and server_legacy.go:434-444). This test
// deliberately calls RevokeToken with the tokenID; calling it with the raw
// token would not catch a regression where RevokeToken never finds the ID
// (and never revokes the family).
func TestStorage_RevokeTokenRefreshRevokesFamily(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	firstRecord, err := d.tokens.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	second, err := d.tokens.RotateRefresh(ctx, firstRecord, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	// As the library really does: learn the ID first.
	_, tokenID, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", second)
	if err != nil {
		t.Fatalf("GetRefreshTokenInfo: %v", err)
	}
	// Then call RevokeToken with this ID, NOT WITH THE RAW TOKEN.
	if oidcErr := d.RevokeToken(ctx, tokenID, "", "makesinger-mobil"); oidcErr != nil {
		t.Fatalf("RevokeToken: %v", oidcErr)
	}
	// The targeted token must be dead...
	if _, err := d.tokens.GetRefresh(ctx, second); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("the revoked token is still valid (err=%v)", err)
	}
	// ...and another token of the SAME FAMILY, never named in the revoke call,
	// must be dead too: family revocation closes the WHOLE chain, not a single token.
	third, err := d.tokens.RotateRefresh(ctx, second, newRefresh("k1"))
	// Note: since the second one was already revoked by RevokeToken, RotateRefresh
	// must fail too; this is a second proof for the question "is the family
	// really dead".
	if err == nil {
		t.Fatalf("rotation succeeded in a revoked family, new token = %q", third)
	}
	if !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("unexpected error type: %v", err)
	}
}

// If RevokeToken targets an access tokenID only that record must be deleted.
func TestStorage_RevokeTokenDeletesAccessRecord(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	if err := d.tokens.SaveAccess(ctx, "at1", "k1", "makesinger-mobil", []string{"openid"}, time.Now().UTC().Add(15*time.Minute)); err != nil {
		t.Fatalf("SaveAccess: %v", err)
	}
	if oidcErr := d.RevokeToken(ctx, "at1", "k1", "makesinger-mobil"); oidcErr != nil {
		t.Fatalf("RevokeToken: %v", oidcErr)
	}
	if _, err := d.tokens.GetAccess(ctx, "at1"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("the revoked access record is still valid (err=%v)", err)
	}
}

// RevokeToken must not return an error for a token that is already invalid
// (neither refresh nor access): RFC 7009 makes revocation idempotent.
func TestStorage_RevokeTokenUnknownSucceedsSilently(t *testing.T) {
	d := newTestStorage(t)
	if oidcErr := d.RevokeToken(context.Background(), "uydurma", "", "makesinger-mobil"); oidcErr != nil {
		t.Errorf("RevokeToken returned an error: %v", oidcErr)
	}
}

// SetUserinfoFromScopes is deprecated: an empty implementation is expected,
// it must not return an error.
func TestStorage_SetUserinfoFromScopesEmpty(t *testing.T) {
	d := newTestStorage(t)
	if err := d.SetUserinfoFromScopes(context.Background(), nil, "k1", "makesinger-mobil", []string{"profile"}); err != nil {
		t.Errorf("SetUserinfoFromScopes returned an error: %v", err)
	}
}

// ---------------------------------------------------------------------
// M-a: the FAIL-CLOSED error path of SetUserinfoFromRequest
// ---------------------------------------------------------------------

// failingByIDUserStore is a UserStore that ALWAYS returns an error from ByID:
// the equivalent of the case where the user row cannot be read (DB down, row
// deleted). The other methods are not called in these tests; if they are
// called they return the SAME error, so they never silently look "successful".
type failingByIDUserStore struct{}

var errUserUnreadable = errors.New("user row could not be read")

func (failingByIDUserStore) Create(context.Context, string, string, string) (*User, error) {
	return nil, errUserUnreadable
}

func (failingByIDUserStore) ByEmail(context.Context, string) (*User, error) {
	return nil, errUserUnreadable
}

func (failingByIDUserStore) ByID(context.Context, string) (*User, error) {
	return nil, errUserUnreadable
}

func (failingByIDUserStore) Delete(context.Context, string) error {
	return errUserUnreadable
}

// fakeIDTokenRequest is the smallest equivalent of op.IDTokenRequest: it is
// needed to be able to REALLY call op.CreateIDToken.
type fakeIDTokenRequest struct {
	sub string
}

func (fakeIDTokenRequest) GetAMR() []string       { return []string{"pwd"} }
func (fakeIDTokenRequest) GetAudience() []string  { return []string{"makesinger-mobil"} }
func (fakeIDTokenRequest) GetAuthTime() time.Time { return time.Now() }
func (fakeIDTokenRequest) GetClientID() string    { return "makesinger-mobil" }
func (fakeIDTokenRequest) GetScopes() []string    { return []string{oidc.ScopeOpenID, oidc.ScopeProfile} }
func (s fakeIDTokenRequest) GetSubject() string   { return s.sub }

// SetUserinfoFromRequest MUST RETURN the ByID error of fillUserinfo to the
// library: in the library chain (pkg/op/token.go:247-251) this method's error
// ABORTS token production.
//
// WHY THIS TEST EXISTS: swallowing the error (_ = d.fillUserinfo(...);
// return nil) left the whole package GREEN. Yet an empty userinfo overwrites
// the id_token's sub through claims.SetUserInfo's UNCONDITIONAL
// "t.Subject = i.Subject" assignment (pkg/oidc/token.go:158-159) and yields
// an id_token without sub -- the SAME as Phase 1's Critical. So a refactor
// made on the grounds that "the worst case is a missing claim" could
// silently reopen that hole.
//
// The measurement goes through op.CreateIDToken, not by calling the method
// directly: so it is also proven that the library REALLY uses the error to
// stop token production.
func TestStorage_IDTokenFailsWhenUserUnreadable(t *testing.T) {
	a, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	cfg := testAuthCfg()
	req := fakeIDTokenRequest{sub: "k1"}
	ctx := context.Background()

	// 1) Vacuous-pass check: with a WORKING store a token IS PRODUCED and it
	// carries sub. Otherwise the assertion below would also hold for
	// "CreateIDToken always blows up".
	healthy := NewStorage(cfg, NewFakeUserStore(), NewFakeTokenStore(), nil, a)
	users, err := healthy.users.Create(ctx, "k@ornek.com", "K", "cok-gizli-sifre")
	if err != nil {
		t.Fatalf("could not create user: %v", err)
	}
	token, err := op.CreateIDToken(ctx, cfg.Issuer, fakeIDTokenRequest{sub: users.ID},
		time.Hour, "", "", healthy, healthy.client)
	if err != nil {
		t.Fatalf("could not produce an id_token with a healthy store: %v", err)
	}
	if token == "" {
		t.Fatal("an empty id_token was produced with a healthy store")
	}

	// 2) With a store whose ByID returns an error, token production must FAIL.
	broken := NewStorage(cfg, failingByIDUserStore{}, NewFakeTokenStore(), nil, a)
	brokenToken, err := op.CreateIDToken(ctx, cfg.Issuer, req, time.Hour, "", "", broken, broken.client)
	if err == nil {
		t.Fatalf("an id_token was PRODUCED while the user could not be read: %q", brokenToken)
	}
	if !errors.Is(err, errUserUnreadable) {
		t.Errorf("error = %v, want the store error wrapped", err)
	}
	if brokenToken != "" {
		t.Errorf("a token was returned together with the error: %q", brokenToken)
	}
}

// Health must be FAIL-CLOSED: if SetHealthDeps was never called (as in the
// tests) it must explicitly return an error, so that /healthz is prevented
// from saying "healthy" even when Postgres/Redis are dead.
func TestStorage_HealthWithoutDepsFails(t *testing.T) {
	d := newTestStorage(t)
	if err := d.Health(context.Background()); err == nil {
		t.Error("Health returned success without a connection (it must be fail-closed)")
	}
}

// GetRefreshTokenInfo must return op.ErrInvalidRefreshToken if the token
// belongs to ANOTHER client as well; if the clientID parameter is ignored,
// one client can learn the userID/tokenID of another's refresh token.
func TestStorage_GetRefreshTokenInfoOtherClientRejected(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	otherClient := newRefresh("k1")
	otherClient.ClientID = "makesinger-web"
	token, err := d.tokens.CreateRefresh(ctx, otherClient)
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	if _, _, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", token); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("error = %v, want op.ErrInvalidRefreshToken", err)
	}
}

// When currentRefreshToken is empty (authorization code flow)
// CreateAccessAndRefreshTokens must start a new family: both the returned
// access and refresh tokens must be valid.
func TestStorage_CreateAccessAndRefreshTokensStartsFamily(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	req := &AuthRequest{
		ID:       "istek1",
		ClientID: "makesinger-mobil",
		Subject:  "k1",
		Scopes:   []string{"openid", "offline_access"},
		AuthTime: time.Now().UTC(),
	}
	accessID, refreshToken, exp, err := d.CreateAccessAndRefreshTokens(ctx, req, "")
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}
	if accessID == "" || refreshToken == "" {
		t.Fatalf("empty value returned: accessID=%q refreshToken=%q", accessID, refreshToken)
	}
	if !exp.After(time.Now().UTC()) {
		t.Errorf("access expiry is in the past: %v", exp)
	}
	if _, err := d.tokens.GetAccess(ctx, accessID); err != nil {
		t.Errorf("could not read the access record: %v", err)
	}
	if record, err := d.tokens.GetRefresh(ctx, refreshToken); err != nil {
		t.Errorf("could not read the refresh token: %v", err)
	} else if record.UserID != "k1" || record.ClientID != "makesinger-mobil" {
		t.Errorf("refresh record was filled wrong: %+v", record)
	}
}

// When currentRefreshToken is set (refresh_token flow)
// CreateAccessAndRefreshTokens must rotate: the old token must die, the new
// one must be valid, and if the old token is presented AGAIN it must return
// op.ErrInvalidRefreshToken (invalid_grant) -- reuse detection must work at
// the Storage level too.
func TestStorage_CreateAccessAndRefreshTokensRotates(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	old, err := d.tokens.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	req := &refreshRequest{userID: "k1", clientID: "makesinger-mobil", scopes: []string{"openid"}}
	accessID, newToken, _, err := d.CreateAccessAndRefreshTokens(ctx, req, old)
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}
	if accessID == "" || newToken == "" || newToken == old {
		t.Fatalf("rotation did not work as expected: accessID=%q newToken=%q", accessID, newToken)
	}
	if _, err := d.tokens.GetRefresh(ctx, newToken); err != nil {
		t.Errorf("the new token is invalid: %v", err)
	}
	// If the old token is presented AGAIN: stolen-token detection must fire and
	// be converted to the right OAuth error (invalid_grant).
	if _, _, _, err := d.CreateAccessAndRefreshTokens(ctx, req, old); !errors.Is(err, oidc.ErrInvalidGrant()) {
		t.Errorf("error = %v, want oidc.ErrInvalidGrant()", err)
	}
}

// simulateRefreshFlow imitates the CALL ORDER of the library's refresh_token
// flow EXACTLY (see zitadel/oidc pkg/op/token_refresh.go:36-44):
// ValidateRefreshTokenRequest -> ... -> TokenRequestByRefreshToken is called
// first and if it RETURNS AN ERROR the flow ABORTS; CreateAccessAndRefreshTokens
// (and therefore RotateRefresh) is NEVER called. Tests have to imitate
// this order: if reuse detection lived only in RotateRefresh it would never
// run in production, but a test that calls RotateRefresh directly can
// never see that.
func simulateRefreshFlow(t *testing.T, d *Storage, refreshToken string) error {
	t.Helper()
	req, err := d.TokenRequestByRefreshToken(context.Background(), refreshToken)
	if err != nil {
		return err
	}
	_, _, _, err = d.CreateAccessAndRefreshTokens(context.Background(), req, refreshToken)
	return err
}

// Regression test for the finding (Critical): when a used refresh token is
// presented in the library's real call order the FAMILY MUST BE REVOKED. If
// the revocation is moved back to RotateRefresh alone this test FAILS,
// because the flow aborts on the error of TokenRequestByRefreshToken and
// never reaches RotateRefresh.
func TestStorage_TokenRequestByRefreshTokenReuseRevokesFamily(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	old, err := d.tokens.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	// The legitimate client rotates normally: the old token becomes "used", the
	// rotated token is valid.
	rotated, err := d.tokens.RotateRefresh(ctx, old, newRefresh("k1"))
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	if _, err := d.tokens.GetRefresh(ctx, rotated); err != nil {
		t.Fatalf("the new token is invalid after rotation: %v", err)
	}

	// The attacker presents the token they stole (now used).
	if err := simulateRefreshFlow(t, d, old); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("error = %v, want op.ErrInvalidRefreshToken", err)
	}

	// What is really measured: the REST of the family must be dead too. The
	// CURRENT token in the legitimate client's hands must no longer work.
	if _, err := d.tokens.GetRefresh(ctx, rotated); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("family was not revoked, the current token is still valid (err=%v)", err)
	}
	if err := simulateRefreshFlow(t, d, rotated); err == nil {
		t.Error("the refresh flow succeeded with a revoked family")
	}
	// Every record in the store must be revoked: the family dies as a whole.
	for _, record := range d.tokens.(*FakeTokenStore).AllRecords() {
		if record.RevokedAt == nil {
			t.Errorf("a family member was not revoked: id=%s", record.ID)
		}
	}
}

// Error INDISTINGUISHABILITY: by looking at the response the attacker must
// not be able to tell whether the token is "unknown" or "used". If the two
// differ, they can measure whether a value they hold is a real token.
func TestStorage_TokenRequestByRefreshTokenErrorIndistinguishable(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	old, err := d.tokens.CreateRefresh(ctx, newRefresh("k1"))
	if err != nil {
		t.Fatalf("CreateRefresh: %v", err)
	}
	if _, err := d.tokens.RotateRefresh(ctx, old, newRefresh("k1")); err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}
	_, usedErr := d.TokenRequestByRefreshToken(ctx, old)
	_, unknownErr := d.TokenRequestByRefreshToken(ctx, "uydurma-jeton")
	if usedErr == nil || unknownErr == nil {
		t.Fatalf("an error is expected in both cases: used=%v unknown=%v", usedErr, unknownErr)
	}
	if usedErr.Error() != unknownErr.Error() {
		t.Errorf("the errors are distinguishable: used=%q unknown=%q", usedErr, unknownErr)
	}
}

const testAdminSecret = "0123456789abcdef0123456789abcdef-admin-secret"

func newAdminStorage(t *testing.T, secret string) *Storage {
	t.Helper()
	a, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}
	cfg := testAuthCfg()
	cfg.AdminClientID = "admin-web"
	cfg.AdminClientSecret = secret
	cfg.AdminRedirectURIs = []string{"https://panel.ornek.dev/admin/callback"}
	return NewStorage(cfg, NewFakeUserStore(), NewFakeTokenStore(), nil, a)
}

func TestStorage_ResolvesBothClients(t *testing.T) {
	s := newAdminStorage(t, testAdminSecret)
	ctx := context.Background()
	m, err := s.GetClientByClientID(ctx, "makesinger-mobil")
	if err != nil || m.AuthMethod() != oidc.AuthMethodNone || m.GetID() != "makesinger-mobil" {
		t.Fatalf("mobile client: %v %v", m, err)
	}
	a, err := s.GetClientByClientID(ctx, "admin-web")
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}
	if a.GetID() != "admin-web" || a.AuthMethod() != oidc.AuthMethodBasic ||
		a.AccessTokenType() != op.AccessTokenTypeJWT || a.ApplicationType() != op.ApplicationTypeWeb {
		t.Errorf("admin client misconfigured: id=%s auth=%s", a.GetID(), a.AuthMethod())
	}
	if got := a.RedirectURIs(); len(got) != 1 || got[0] != "https://panel.ornek.dev/admin/callback" {
		t.Errorf("admin RedirectURIs = %v", got)
	}
	if got := a.PostLogoutRedirectURIs(); len(got) != 1 {
		t.Errorf("admin PostLogoutRedirectURIs = %v", got)
	}
	if !a.IsScopeAllowed("offline_access") || a.IsScopeAllowed("admin") {
		t.Error("admin scopes wrong")
	}
	if _, err := s.GetClientByClientID(ctx, "baska"); err == nil {
		t.Error("unknown client accepted")
	}
	if _, err := s.GetClientByClientID(ctx, ""); err == nil {
		t.Error("empty client id accepted")
	}
}

func TestStorage_AdminSecretCheckedConstantTime(t *testing.T) {
	s := newAdminStorage(t, testAdminSecret)
	ctx := context.Background()
	if err := s.AuthorizeClientIDSecret(ctx, "admin-web", testAdminSecret); err != nil {
		t.Errorf("right secret rejected: %v", err)
	}
	for _, bad := range []string{"", "yanlis", testAdminSecret + "x", testAdminSecret[:len(testAdminSecret)-1]} {
		if err := s.AuthorizeClientIDSecret(ctx, "admin-web", bad); err == nil {
			t.Errorf("secret %q accepted", bad)
		}
	}
	// The mobile client stays secretless: not even the admin secret works.
	if err := s.AuthorizeClientIDSecret(ctx, "makesinger-mobil", testAdminSecret); err == nil {
		t.Error("mobile client authorized with a secret")
	}
	if err := s.AuthorizeClientIDSecret(ctx, "makesinger-mobil", ""); err == nil {
		t.Error("mobile client authorized with an empty secret")
	}
}

func TestServer_AdminClientMissingSecretDisablesAdmin(t *testing.T) {
	s := newAdminStorage(t, "")
	ctx := context.Background()
	if _, err := s.GetClientByClientID(ctx, "admin-web"); err == nil {
		t.Error("admin client registered without a secret")
	}
	if err := s.AuthorizeClientIDSecret(ctx, "admin-web", ""); err == nil {
		t.Error("empty secret authorized the admin client")
	}
	cfg := testAuthCfg()
	cfg.AdminClientID = "admin-web"
	if err := validateConfig(cfg); err != nil {
		t.Errorf("config without admin secret should be valid: %v", err)
	}
}

func TestValidateConfig_AdminSecret(t *testing.T) {
	cases := map[string]func(*config.AuthConfig){
		"short secret":      func(c *config.AuthConfig) { c.AdminClientSecret = "kisa" },
		"31 bytes":          func(c *config.AuthConfig) { c.AdminClientSecret = strings.Repeat("a", 31) },
		"same id as mobile": func(c *config.AuthConfig) { c.AdminClientID = c.ClientID },
		"empty id":          func(c *config.AuthConfig) { c.AdminClientID = " " },
		"no redirects":      func(c *config.AuthConfig) { c.AdminRedirectURIs = nil },
		"blank redirect":    func(c *config.AuthConfig) { c.AdminRedirectURIs = []string{" "} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testAuthCfg()
			cfg.AdminClientID = "admin-web"
			cfg.AdminClientSecret = strings.Repeat("s", 32)
			cfg.AdminRedirectURIs = []string{"https://panel.ornek.dev/admin/callback"}
			if err := validateConfig(cfg); err != nil {
				t.Fatalf("baseline invalid: %v", err)
			}
			mutate(cfg)
			if err := validateConfig(cfg); err == nil {
				t.Error("invalid admin configuration accepted")
			}
		})
	}
}

func TestServer_AdminViewReadsIdentityStore(t *testing.T) {
	users := NewFakeUserStore()
	u, err := users.Create(context.Background(), "Yonetici@Ornek.dev", "Y", "uzun-bir-parola-123")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{users: users}
	email, verified, err := srv.AdminView(context.Background(), u.ID)
	if err != nil || email != u.Email {
		t.Fatalf("AdminView = %q %v %v", email, verified, err)
	}
	if _, v, err := srv.AdminView(context.Background(), "yok"); err != nil || v {
		t.Errorf("unknown user: verified=%v err=%v, want false/nil", v, err)
	}
}
