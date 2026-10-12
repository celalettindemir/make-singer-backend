package identity

import (
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// MobileClient defines our only first-party client: a native public
// client, authorization code + PKCE only.
type MobileClient struct {
	cfg *config.AuthConfig
}

func NewMobileClient(cfg *config.AuthConfig) *MobileClient {
	return &MobileClient{cfg: cfg}
}

var _ op.Client = (*MobileClient)(nil)

func (c *MobileClient) GetID() string          { return c.cfg.ClientID }
func (c *MobileClient) RedirectURIs() []string { return c.cfg.RedirectURIs }

// The return after logout is restricted to the same addresses.
func (c *MobileClient) PostLogoutRedirectURIs() []string { return c.cfg.RedirectURIs }

func (c *MobileClient) ApplicationType() op.ApplicationType { return op.ApplicationTypeNative }
func (c *MobileClient) AuthMethod() oidc.AuthMethod         { return oidc.AuthMethodNone }

func (c *MobileClient) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}

func (c *MobileClient) GrantTypes() []oidc.GrantType {
	return []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken}
}

// LoginURL sends the user to our own login page. It must carry
// authRequestID; once the page completes the login it resumes the flow
// with this ID.
func (c *MobileClient) LoginURL(authRequestID string) string {
	return yolGiris + "?authRequestID=" + authRequestID
}

// JWT access token: /api/* verification never touches the data store.
func (c *MobileClient) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeJWT }

func (c *MobileClient) IDTokenLifetime() time.Duration { return time.Hour }
func (c *MobileClient) DevMode() bool                  { return false }

func (c *MobileClient) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}

func (c *MobileClient) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(scopes []string) []string { return scopes }
}

// Allowed scopes are listed explicitly; an undefined scope is never
// accepted silently.
func (c *MobileClient) IsScopeAllowed(scope string) bool {
	switch scope {
	case oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess:
		return true
	}
	return false
}

// The id_token also carries the userinfo claims: the mobile app does not
// have to call /userinfo separately.
func (c *MobileClient) IDTokenUserinfoClaimsAssertion() bool { return true }

func (c *MobileClient) ClockSkew() time.Duration { return 0 }
