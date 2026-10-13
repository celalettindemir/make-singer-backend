package identity

import (
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func testAuthCfg() *config.AuthConfig {
	return &config.AuthConfig{
		Issuer:   "https://makesinger-auth.ornek.dev",
		ClientID: "makesinger-mobil",
		RedirectURIs: []string{
			"com.makesinger.app:/oauth2redirect",
			"https://makesinger.ornek.dev/oauth2redirect",
		},
		// The store tests (token_store_test.go) also exercise TTL-dependent paths
		// such as CreateAccessAndRefreshTokens. If these are left at 0, the access
		// record is silently not written because TTL<=0, and refresh counts as
		// "expired" at once, so those paths would never actually be tested.
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 1440 * time.Hour,
	}
}

func TestMobileClient_SatisfiesInterface(t *testing.T) {
	var _ op.Client = NewMobileClient(testAuthCfg())
}

// Public client: it carries no secret, so the authentication method must
// be "none". With "basic" the library expects a secret and the token
// request is rejected.
func TestMobileClient_PublicAndNative(t *testing.T) {
	c := NewMobileClient(testAuthCfg())
	if c.AuthMethod() != oidc.AuthMethodNone {
		t.Errorf("AuthMethod = %v, beklenen none", c.AuthMethod())
	}
	if c.ApplicationType() != op.ApplicationTypeNative {
		t.Errorf("ApplicationType = %v, beklenen native", c.ApplicationType())
	}
	if c.GetID() != "makesinger-mobil" {
		t.Errorf("GetID = %q", c.GetID())
	}
}

// Authorization code flow only. The implicit or token response type
// would put the token in the address bar for a public client.
func TestMobileClient_CodeFlowOnly(t *testing.T) {
	c := NewMobileClient(testAuthCfg())
	types := c.ResponseTypes()
	if len(types) != 1 || types[0] != oidc.ResponseTypeCode {
		t.Errorf("ResponseTypes = %v, beklenen [code]", types)
	}
	var hasRefresh bool
	for _, g := range c.GrantTypes() {
		if g == oidc.GrantTypeImplicit {
			t.Error("implicit grant acik")
		}
		if g == oidc.GrantTypeRefreshToken {
			hasRefresh = true
		}
	}
	if !hasRefresh {
		t.Error("refresh_token grant kapali; oturum yenilenemez")
	}
}

func TestMobileClient_RedirectURIs(t *testing.T) {
	c := NewMobileClient(testAuthCfg())
	if len(c.RedirectURIs()) != 2 {
		t.Errorf("RedirectURIs = %v", c.RedirectURIs())
	}
}

// If DevMode stays on, the library relaxes redirect URI validation; in
// production that would mean sending tokens to an attacker's address.
func TestMobileClient_DevModeOff(t *testing.T) {
	if NewMobileClient(testAuthCfg()).DevMode() {
		t.Error("DevMode acik")
	}
}

func TestMobileClient_AllowedScopes(t *testing.T) {
	c := NewMobileClient(testAuthCfg())
	for _, s := range []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess} {
		if !c.IsScopeAllowed(s) {
			t.Errorf("%q scope'u reddedildi", s)
		}
	}
	if c.IsScopeAllowed("admin") {
		t.Error("tanimsiz scope kabul edildi")
	}
}
