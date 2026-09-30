package kimlik

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
		// Depo testleri (depo_test.go) CreateAccessAndRefreshTokens gibi
		// TTL'e bagli yollari da olcer; bunlar 0 birakilirsa access kaydi
		// TTL<=0 ile sessizce yazilmaz ve refresh aninda "gecmis" sayilir,
		// o yollar fiilen hic test edilmemis olur.
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 1440 * time.Hour,
	}
}

func TestMobilIstemciArayuzuKarsilar(t *testing.T) {
	var _ op.Client = NewMobilIstemci(testAuthCfg())
}

// Public client: secret tasimaz, bu yuzden kimlik dogrulama yontemi
// "none" olmali. "basic" secilirse kutuphane secret bekler ve token
// istegi reddedilir.
func TestMobilIstemciPublicVeNative(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
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

// Yalnizca authorization code akisi. Implicit veya token response type
// public client'ta jetonu adres cubuguna dusurur.
func TestMobilIstemciYalnizcaKodAkisi(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
	tipler := c.ResponseTypes()
	if len(tipler) != 1 || tipler[0] != oidc.ResponseTypeCode {
		t.Errorf("ResponseTypes = %v, beklenen [code]", tipler)
	}
	var refreshVar bool
	for _, g := range c.GrantTypes() {
		if g == oidc.GrantTypeImplicit {
			t.Error("implicit grant acik")
		}
		if g == oidc.GrantTypeRefreshToken {
			refreshVar = true
		}
	}
	if !refreshVar {
		t.Error("refresh_token grant kapali; oturum yenilenemez")
	}
}

func TestMobilIstemciRedirectleri(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
	if len(c.RedirectURIs()) != 2 {
		t.Errorf("RedirectURIs = %v", c.RedirectURIs())
	}
}

// DevMode acik kalirsa kutuphane redirect URI dogrulamasini gevsetir;
// uretimde bu, jetonlari saldirganin adresine yollamak demektir.
func TestMobilIstemciDevModeKapali(t *testing.T) {
	if NewMobilIstemci(testAuthCfg()).DevMode() {
		t.Error("DevMode acik")
	}
}

func TestMobilIstemciIzinliScopelar(t *testing.T) {
	c := NewMobilIstemci(testAuthCfg())
	for _, s := range []string{oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess} {
		if !c.IsScopeAllowed(s) {
			t.Errorf("%q scope'u reddedildi", s)
		}
	}
	if c.IsScopeAllowed("admin") {
		t.Error("tanimsiz scope kabul edildi")
	}
}
