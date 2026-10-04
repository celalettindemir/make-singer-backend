package kimlik

import (
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// MobilIstemci, tek birinci-taraf istemcimizi tanimlar: native public
// client, yalnizca authorization code + PKCE.
type MobilIstemci struct {
	cfg *config.AuthConfig
}

func NewMobilIstemci(cfg *config.AuthConfig) *MobilIstemci {
	return &MobilIstemci{cfg: cfg}
}

var _ op.Client = (*MobilIstemci)(nil)

func (c *MobilIstemci) GetID() string          { return c.cfg.ClientID }
func (c *MobilIstemci) RedirectURIs() []string { return c.cfg.RedirectURIs }

// Cikis sonrasi geri donus de ayni adreslere sinirlidir.
func (c *MobilIstemci) PostLogoutRedirectURIs() []string { return c.cfg.RedirectURIs }

func (c *MobilIstemci) ApplicationType() op.ApplicationType { return op.ApplicationTypeNative }
func (c *MobilIstemci) AuthMethod() oidc.AuthMethod         { return oidc.AuthMethodNone }

func (c *MobilIstemci) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}

func (c *MobilIstemci) GrantTypes() []oidc.GrantType {
	return []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken}
}

// LoginURL, kullaniciyi kendi giris sayfamiza yollar. authRequestID'yi
// tasimak zorunludur; sayfa girisi tamamladiktan sonra bu id ile
// akisi surdurur.
func (c *MobilIstemci) LoginURL(authRequestID string) string {
	return yolGiris + "?authRequestID=" + authRequestID
}

// JWT access token: /api/* dogrulamasi veri deposuna gitmez.
func (c *MobilIstemci) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeJWT }

func (c *MobilIstemci) IDTokenLifetime() time.Duration { return time.Hour }
func (c *MobilIstemci) DevMode() bool                  { return false }

func (c *MobilIstemci) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}

func (c *MobilIstemci) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}

// Izin verilen scope'lar acikca listelenir; tanimsiz bir scope sessizce
// kabul edilmez.
func (c *MobilIstemci) IsScopeAllowed(scope string) bool {
	switch scope {
	case oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess:
		return true
	}
	return false
}

// id_token, userinfo claim'lerini de tasir: mobil ayrica /userinfo
// cagirmak zorunda kalmaz.
func (c *MobilIstemci) IDTokenUserinfoClaimsAssertion() bool { return true }

func (c *MobilIstemci) ClockSkew() time.Duration { return 0 }
