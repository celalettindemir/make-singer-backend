package identity

import (
	"context"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// pkceEnforcer implements the library's op.AuthorizeValidator extension
// point and makes S256 PKCE MANDATORY at /authorize.
//
// WHY WE ENFORCE IT OURSELVES: op.Config.CodeMethodS256 ONLY feeds the
// discovery document (pkg/op/op.go CodeMethodS256Supported ->
// pkg/op/discovery.go); no validation uses it.
// ValidateAuthRequestClient never looks at code_challenge,
// AuthorizeCodeChallenge does not check the method, and
// oidc.VerifyCodeChallenge does a plain string comparison for any method
// other than S256 (plain or EMPTY) — so challenge == verifier holds and
// PKCE is effectively disabled.
type pkceEnforcer struct {
	*op.Provider
}

var _ op.AuthorizeValidator = (*pkceEnforcer)(nil)

// ValidateAuthRequest runs the library's default validation and adds the
// S256 PKCE requirement on top of it.
//
// ORDER MATTERS: op.ValidateAuthRequestClient first, PKCE AFTER. The
// library is what validates redirect_uri, and a PKCE error returns to the
// client's redirect_uri with a 302 (op.AuthRequestError). Returning an
// error to an unvalidated redirect_uri would be an OPEN REDIRECT.
func (e *pkceEnforcer) ValidateAuthRequest(
	ctx context.Context,
	authReq *oidc.AuthRequest,
	storage op.Storage,
	verifier *op.IDTokenHintVerifier,
) (string, error) {
	client, err := storage.GetClientByClientID(ctx, authReq.ClientID)
	if err != nil {
		return "", oidc.ErrInvalidRequestRedirectURI().
			WithDescription("unable to retrieve client by id").WithParent(err)
	}
	sub, err := op.ValidateAuthRequestClient(ctx, authReq, client, verifier)
	if err != nil {
		return "", err
	}
	if err := requirePKCES256(authReq); err != nil {
		return "", err
	}
	return sub, nil
}

// requirePKCES256 verifies that code_challenge is present and that the
// method is S256. An empty method is REJECTED too: the library treats an
// empty method like plain (plain comparison).
//
// Rejecting at /authorize matters: otherwise the user is redirected to the
// login page, enters the password, a code is issued, and the error only
// surfaces at the token endpoint ("PKCE required") — so the credentials
// would have been entered into a dead flow from the start.
func requirePKCES256(authReq *oidc.AuthRequest) error {
	if authReq.CodeChallenge == "" {
		return oidc.ErrInvalidRequest().WithDescription(
			"code_challenge is required: this provider only accepts PKCE with code_challenge_method=S256")
	}
	if authReq.CodeChallengeMethod != oidc.CodeChallengeMethodS256 {
		return oidc.ErrInvalidRequest().WithDescription(
			"code_challenge_method must be S256")
	}
	return nil
}
