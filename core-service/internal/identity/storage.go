package identity

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/makeasinger/api/internal/config"
)

// Storage satisfies the whole op.Storage interface. It carries no storage
// logic of its own; it delegates to UserStore, TokenStore, RequestStore and
// SigningKey. The job of this file is mechanical: wiring the signatures the
// library expects to our own stores.
type Storage struct {
	cfg        *config.AuthConfig
	users      UserStore
	tokens     TokenStore
	requests   *IstekDepo
	signingKey *SigningKey
	client     *MobileClient

	pool *pgxpool.Pool // for Health; may be nil
	rdb  *redis.Client // for Health; may be nil
}

// NewStorage is the common setup point for tests and production. If
// Health needs the Postgres/Redis connections they are added later with
// SetHealthDeps; this way tests can build a Storage without live
// connections.
func NewStorage(cfg *config.AuthConfig, users UserStore, tokens TokenStore, requests *IstekDepo, signingKey *SigningKey) *Storage {
	return &Storage{
		cfg:        cfg,
		users:      users,
		tokens:     tokens,
		requests:   requests,
		signingKey: signingKey,
		client:     NewMobileClient(cfg),
	}
}

// SetHealthDeps wires in the Postgres pool and the Redis client that
// Health will use. If it is not called Health behaves fail-closed and
// always returns an error (see Health): with no connection, failing
// explicitly is preferred over being counted as healthy.
func (s *Storage) SetHealthDeps(pool *pgxpool.Pool, rdb *redis.Client) {
	s.pool = pool
	s.rdb = rdb
}

// Verify at compile time that Storage satisfies ALL of op.Storage. If a
// method is missing or has the wrong signature the build breaks here.
var _ op.Storage = (*Storage)(nil)

// op.CanSetUserinfoFromRequest is an OPTIONAL interface: the library looks
// for it with a type assertion (pkg/op/token.go:247) and SILENTLY skips it
// if it is not satisfied. Misspelling the method name therefore produces
// not a compile error but a HARD-TO-UNDERSTAND loss of behavior (sub goes
// missing in the id_token). This line makes the compiler the guard.
var _ op.CanSetUserinfoFromRequest = (*Storage)(nil)

// ---- AuthStorage ----

// CreateAuthRequest hands the incoming authorization request over to
// RequestStore. The third parameter (userID) is usually empty: the user
// has not signed in yet at this stage; MarkDone fills it in when the
// login completes.
func (s *Storage) CreateAuthRequest(ctx context.Context, authReq *oidc.AuthRequest, userID string) (op.AuthRequest, error) {
	req, err := s.requests.Olustur(ctx, authReq, userID)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func (s *Storage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	return s.requests.IDileOku(ctx, id)
}

func (s *Storage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	return s.requests.KodlaOku(ctx, code)
}

func (s *Storage) SaveAuthCode(ctx context.Context, id string, code string) error {
	return s.requests.KodKaydet(ctx, id, code)
}

func (s *Storage) DeleteAuthRequest(ctx context.Context, id string) error {
	return s.requests.Sil(ctx, id)
}

// clientIDGetter is the common denominator of AuthRequest and
// refreshRequest: it is used to extract the client ID without knowing the
// concrete type of the TokenRequest that arrives at
// CreateAccessToken/CreateAccessAndRefreshTokens.
type clientIDGetter interface {
	GetClientID() string
}

// amrGetter / authTimeGetter: AuthRequest and refreshRequest carry this
// information, but the op.TokenRequest interface does not require it (for
// example if another TokenRequest type is added in the future this
// information may be absent).
type amrGetter interface{ GetAMR() []string }
type authTimeGetter interface{ GetAuthTime() time.Time }

// CreateAccessToken is called in the authorization code flow where
// offline_access was not requested (and in flows we do not support, such as
// implicit/JWT profile). The access record is written to TokenStore; the
// JWT access token carries this ID as its jti.
func (s *Storage) CreateAccessToken(ctx context.Context, request op.TokenRequest) (string, time.Time, error) {
	getter, ok := request.(clientIDGetter)
	if !ok {
		return "", time.Time{}, fmt.Errorf("token istegi turu desteklenmiyor: %T", request)
	}
	id := uuid.NewString()
	expiresAt := time.Now().UTC().Add(s.cfg.AccessTTL)
	if err := s.tokens.SaveAccess(ctx, id, request.GetSubject(), getter.GetClientID(), request.GetScopes(), expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return id, expiresAt, nil
}

// CreateAccessAndRefreshTokens is called both in the authorization code
// flow (when offline_access is requested, currentRefreshToken empty) and
// in the refresh_token flow (currentRefreshToken set). Rotation is
// delegated to TokenStore: if currentRefreshToken is empty a new family
// starts, otherwise RotateRefresh continues the existing family.
func (s *Storage) CreateAccessAndRefreshTokens(ctx context.Context, request op.TokenRequest, currentRefreshToken string) (accessTokenID string, newRefreshToken string, expiration time.Time, err error) {
	getter, ok := request.(clientIDGetter)
	if !ok {
		return "", "", time.Time{}, fmt.Errorf("token istegi turu desteklenmiyor: %T", request)
	}
	clientID := getter.GetClientID()
	userID := request.GetSubject()
	scopes := request.GetScopes()
	audience := request.GetAudience()

	var amr []string
	if g, ok := request.(amrGetter); ok {
		amr = g.GetAMR()
	}
	var authTime time.Time
	if g, ok := request.(authTimeGetter); ok {
		authTime = g.GetAuthTime()
	}

	fresh := &RefreshRecord{
		UserID:    userID,
		ClientID:  clientID,
		Scopes:    scopes,
		Audience:  audience,
		AMR:       amr,
		AuthTime:  authTime,
		ExpiresAt: time.Now().UTC().Add(s.cfg.RefreshTTL),
	}

	// Rotation is done FIRST, the access record is written AFTER. If the
	// order were reversed (access first, then rotation) and the rotation
	// failed, an orphaned access record would live in TokenStore for 15
	// minutes (AccessTTL). With this order, if the rotation fails the access
	// record is never written.
	var refreshToken string
	if currentRefreshToken == "" {
		// Authorization code flow: no presented token, a new family starts.
		refreshToken, err = s.tokens.CreateRefresh(ctx, fresh)
	} else {
		refreshToken, err = s.tokens.RotateRefresh(ctx, currentRefreshToken, fresh)
		if errors.Is(err, ErrTokenReused) || errors.Is(err, ErrTokenNotFound) {
			// op.ErrInvalidRefreshToken is a plain errors.New, NOT an
			// *oidc.Error; if returned on this leg the library takes it for
			// a generic error and returns 500 server_error to the client.
			// oidc.ErrInvalidGrant() is converted to the correct OAuth error
			// code (invalid_grant, 400).
			err = oidc.ErrInvalidGrant()
		}
	}
	if err != nil {
		return "", "", time.Time{}, err
	}

	accessID := uuid.NewString()
	accessExpiresAt := time.Now().UTC().Add(s.cfg.AccessTTL)
	if err := s.tokens.SaveAccess(ctx, accessID, userID, clientID, scopes, accessExpiresAt); err != nil {
		return "", "", time.Time{}, err
	}
	return accessID, refreshToken, accessExpiresAt, nil
}

// refreshRequest is a thin wrapper over RefreshRecord that satisfies the
// op.RefreshTokenRequest interface. SetCurrentScopes has to be writable so
// that the library can apply the scope restriction on the refresh request
// (when the requested scopes are a subset of the original).
type refreshRequest struct {
	userID   string
	clientID string
	scopes   []string
	audience []string
	amr      []string
	authTime time.Time
}

var _ op.RefreshTokenRequest = (*refreshRequest)(nil)

func (r *refreshRequest) GetAMR() []string                 { return r.amr }
func (r *refreshRequest) GetAudience() []string            { return r.audience }
func (r *refreshRequest) GetAuthTime() time.Time           { return r.authTime }
func (r *refreshRequest) GetClientID() string              { return r.clientID }
func (r *refreshRequest) GetScopes() []string              { return r.scopes }
func (r *refreshRequest) GetSubject() string               { return r.userID }
func (r *refreshRequest) SetCurrentScopes(scopes []string) { r.scopes = scopes }

// TokenRequestByRefreshToken validates and reads the presented refresh
// token (GetRefresh); rotation is NOT done here, CreateAccessAndRefreshTokens
// does it.
//
// REUSE DETECTION HAPPENS HERE IN PRODUCTION. In the refresh flow the
// library FIRST calls ValidateRefreshTokenRequest -> ... -> this method and
// ABORTS the flow if it returns an error; it NEVER goes on to
// CreateAccessAndRefreshTokens (and therefore RotateRefresh) (see
// op.RefreshTokenExchange, pkg/op/token_refresh.go). So when a used token
// is presented we have to revoke the family HERE: otherwise the thief just
// gets a 400, but the rest of the family (including the current token held
// by the legitimate client) stays valid and the rotation + detection
// architecture becomes meaningless.
func (s *Storage) TokenRequestByRefreshToken(ctx context.Context, refreshToken string) (op.RefreshTokenRequest, error) {
	record, err := s.tokens.GetRefresh(ctx, refreshToken)
	if errors.Is(err, ErrTokenReused) {
		if revokeErr := s.revokeFamilyOf(ctx, refreshToken); revokeErr != nil {
			// An infrastructure error (e.g. a Postgres outage) is NOT masked
			// with op.ErrInvalidRefreshToken: if "I could not revoke" is
			// swallowed silently the theft goes unpunished and the fault
			// becomes invisible. Access is denied either way, because
			// returning an error aborts the flow anyway.
			return nil, revokeErr
		}
		// The error returned to the client is the SAME as for the "unknown
		// token" case: the attacker must not be able to tell from the
		// response whether the token is unknown or used.
		return nil, op.ErrInvalidRefreshToken
	}
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return nil, op.ErrInvalidRefreshToken
		}
		return nil, err
	}
	return &refreshRequest{
		userID:   record.UserID,
		clientID: record.ClientID,
		scopes:   record.Scopes,
		audience: record.Audience,
		amr:      record.AMR,
		authTime: record.AuthTime,
	}, nil
}

// revokeFamilyOf revokes the whole family of the token whose reuse was
// detected. If it returns an error it is an INFRASTRUCTURE error and goes
// to the client as oidc.ErrServerError; since the invalidity of the token
// does not change, the caller denies access either way.
func (s *Storage) revokeFamilyOf(ctx context.Context, refreshToken string) error {
	familyID, err := s.tokens.RefreshFamilyID(ctx, refreshToken)
	if err != nil {
		// Every error here, including ErrTokenNotFound, is an
		// infrastructure/inconsistency error: we SAW the record a moment ago
		// through GetRefresh (records are never deleted), so not finding it
		// now is not a normal situation and cannot be swallowed as "invalid
		// token".
		return oidc.ErrServerError().WithParent(fmt.Errorf("yeniden kullanim: aile kimligi bulunamadi: %w", err))
	}
	if err := s.tokens.RevokeFamily(ctx, familyID); err != nil {
		return oidc.ErrServerError().WithParent(fmt.Errorf("yeniden kullanim: aile iptal edilemedi: %w", err))
	}
	// Neither the token itself nor its hash is written to the log; only the
	// event and the family ID (an ID generated on the server side, not a
	// secret).
	log.Printf("refresh yeniden kullanimi tespit edildi, aile iptal edildi: family_id=%s", familyID)
	return nil
}

// TerminateSession revokes all refresh tokens of the user at that client
// in the logout flow. JWT access tokens end with their natural lifetime.
func (s *Storage) TerminateSession(ctx context.Context, userID string, clientID string) error {
	// M2: there used to be NO guard for an empty userID/clientID. A
	// parameterless GET /end_session sent the empty string to Postgres as a
	// uuid and produced a 500 + "invalid input syntax for type uuid"
	// (SQLSTATE 22P02) and printed an ERROR log line on EVERY request: log
	// pollution and a small amplification surface. There is NO token to
	// revoke with an empty ID anyway, so returning early does not change the
	// behavior — it only removes the pointless query and the error.
	if userID == "" || clientID == "" {
		return nil
	}
	return s.tokens.RevokeUser(ctx, userID, clientID)
}

// revokeRefresh assumes tokenOrTokenID is a refresh token and tries to
// revoke it. If found=false the value was not of this kind (another kind
// must be tried); found=true means the operation is complete (the error
// may also be nil, which then means it was revoked successfully).
//
// IMPORTANT: BEFORE calling RevokeToken the library calls
// GetRefreshTokenInfo and passes the tokenID it returned (NOT THE TOKEN
// ITSELF) to RevokeToken (see zitadel/oidc pkg/op/token_revocation.go and
// server_legacy.go: "token = tokenID; subject = userID"). So we look up BY
// ID FIRST (GetRefreshByID); lookup with the raw token (GetRefresh) is
// only a fallback tried when the ID lookup finds nothing (for example for
// another path that calls this store directly).
func (s *Storage) revokeRefresh(ctx context.Context, tokenOrTokenID, clientID string) (found bool, oidcErr *oidc.Error) {
	record, err := s.tokens.GetRefreshByID(ctx, tokenOrTokenID)
	if err != nil && errors.Is(err, ErrTokenNotFound) {
		record, err = s.tokens.GetRefresh(ctx, tokenOrTokenID)
	}
	if err != nil {
		// ErrTokenReused (used token) is put in the same bucket as
		// ErrTokenNotFound here: in the /revoke flow the library ALREADY
		// arrives first with the ID returned by GetRefreshTokenInfo, and
		// that path finds the record through GetRefreshByID without a
		// validity filter and revokes the family. This raw-token leg is only
		// a fallback; a revoke request for a used token may be answered with
		// 200 per RFC 7009, and this is identical to the behavior before the
		// fix.
		if errors.Is(err, ErrTokenNotFound) || errors.Is(err, ErrTokenReused) {
			return false, nil
		}
		// A real storage error (e.g. a Postgres outage): if it is confused
		// with ErrTokenNotFound, /revoke silently returns 200 while the DB is
		// dead and the problem becomes invisible.
		return true, oidc.ErrServerError().WithParent(err)
	}
	if record.ClientID != clientID {
		return true, oidc.ErrInvalidClient().WithDescription("jeton bu istemci icin verilmemis")
	}
	if err := s.tokens.RevokeFamily(ctx, record.FamilyID); err != nil {
		return true, oidc.ErrServerError().WithParent(err)
	}
	return true, nil
}

// revokeAccess assumes tokenOrTokenID is an access tokenID and deletes the
// TokenStore record. Its semantics are the same as revokeRefresh: if found
// is false another kind must be tried.
func (s *Storage) revokeAccess(ctx context.Context, tokenOrTokenID, clientID string) (found bool, oidcErr *oidc.Error) {
	record, err := s.tokens.GetAccess(ctx, tokenOrTokenID)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return false, nil
		}
		return true, oidc.ErrServerError().WithParent(err)
	}
	if record.ClientID != clientID {
		return true, oidc.ErrInvalidClient().WithDescription("jeton bu istemci icin verilmemis")
	}
	if err := s.tokens.DeleteAccess(ctx, tokenOrTokenID); err != nil {
		return true, oidc.ErrServerError().WithParent(err)
	}
	return true, nil
}

// RevokeToken is called from the RFC 7009 /revoke endpoint. For a refresh
// token the whole family is revoked (not a single token): otherwise
// another token of the same family would remain valid. For an access token
// only the TokenStore record is deleted; the JWT itself keeps its validity
// as long as the signature is valid, but introspection/userinfo can no
// longer find it.
//
// By the library contract the userID parameter is set ONLY when revoking
// an access token (it comes empty for refresh); this hint lets us choose
// which kind to try first and avoid a needless storage query.
func (s *Storage) RevokeToken(ctx context.Context, tokenOrTokenID string, userID string, clientID string) *oidc.Error {
	if userID != "" {
		if found, oidcErr := s.revokeAccess(ctx, tokenOrTokenID, clientID); found {
			return oidcErr
		}
		if found, oidcErr := s.revokeRefresh(ctx, tokenOrTokenID, clientID); found {
			return oidcErr
		}
		// Neither access nor refresh: counts as success per RFC 7009's
		// idempotent revocation.
		return nil
	}
	if found, oidcErr := s.revokeRefresh(ctx, tokenOrTokenID, clientID); found {
		return oidcErr
	}
	if found, oidcErr := s.revokeAccess(ctx, tokenOrTokenID, clientID); found {
		return oidcErr
	}
	return nil
}

// GetRefreshTokenInfo checks whether the presented value really is a
// refresh token; if it is not (or it was consumed/revoked or belongs to
// another client) returning op.ErrInvalidRefreshToken is MANDATORY (library
// contract). A real storage error (e.g. a Postgres outage) is NOT CONFUSED
// WITH IT: the library falls to 500 server_error only when it does not see
// ErrInvalidRefreshToken, and swallowing this distinction would make the
// revoke/refresh flows silently look like "invalid token" while the DB is
// dead (and hide the real fault).
func (s *Storage) GetRefreshTokenInfo(ctx context.Context, clientID string, token string) (userID string, tokenID string, err error) {
	record, err := s.tokens.GetRefresh(ctx, token)
	if err != nil {
		if errors.Is(err, ErrTokenReused) {
			// M7: reuse kills the family HERE too. The whole architecture
			// rests on the "reuse => revoke the family" principle; this path
			// had a detection channel that was silently lost (it was not
			// exploitable, but the loss of the channel was real).
			//
			// The error PATH DELIBERATELY DOES NOT CHANGE: even if the
			// revocation fails we return op.ErrInvalidRefreshToken.
			// Rationale: this function is also called in the /revoke flow and
			// RFC 7009 §2.2 wants 200 for an invalid token. Propagating the
			// revocation error would turn /revoke into a 500 and make the
			// "used token" and "unknown token" cases distinguishable from the
			// responses (information leak). The fault instead goes to
			// revokeFamilyOf's own log and to the line below; neither the
			// token nor its hash is written to the log.
			if revokeErr := s.revokeFamilyOf(ctx, token); revokeErr != nil {
				log.Printf("refresh yeniden kullanimi: aile iptal edilemedi (erisim yine reddedildi): %v", revokeErr)
			}
			return "", "", op.ErrInvalidRefreshToken
		}
		if errors.Is(err, ErrTokenNotFound) {
			return "", "", op.ErrInvalidRefreshToken
		}
		return "", "", fmt.Errorf("refresh jetonu okunamadi: %w", err)
	}
	if record.ClientID != clientID {
		return "", "", op.ErrInvalidRefreshToken
	}
	return record.UserID, record.ID, nil
}

func (s *Storage) SigningKey(ctx context.Context) (op.SigningKey, error) {
	return s.signingKey, nil
}

func (s *Storage) SignatureAlgorithms(ctx context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{s.signingKey.SignatureAlgorithm()}, nil
}

// KeySet returns the public key for publishing the JWKS. s.signingKey
// cannot be returned directly: *SigningKey DELIBERATELY does not satisfy
// op.Key (it has no Algorithm/Use methods), so the private key leaking
// into the JWKS by mistake is prevented at compile time.
func (s *Storage) KeySet(ctx context.Context) ([]op.Key, error) {
	return []op.Key{s.signingKey.PublicKey()}, nil
}

// ---- OPStorage ----

// GetClientByClientID rejects EVERYTHING except our single configured
// client: makesinger has no second client, and if it had one it must not
// be accepted without being defined here.
func (s *Storage) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	if clientID != s.cfg.ClientID {
		return nil, fmt.Errorf("istemci bulunamadi: %s", clientID)
	}
	return s.client, nil
}

// AuthorizeClientIDSecret ALWAYS returns an error: our single client is
// public (native, AuthMethodNone) so it carries no secret. If it returned
// success the client could be impersonated with an empty secret.
func (s *Storage) AuthorizeClientIDSecret(ctx context.Context, clientID, clientSecret string) error {
	return fmt.Errorf("istemci %q secret tasimaz (public client)", clientID)
}

// SetUserinfoFromScopes was declared deprecated by the library but has to
// stay because it is part of the minimum interface that op.Storage
// requires. Its body MAY STAY EMPTY, but only because SetUserinfoFromRequest
// below fills in the same userinfo object: if both are empty the sub in the
// id_token is ERASED (see the explanation there).
func (s *Storage) SetUserinfoFromScopes(ctx context.Context, userinfo *oidc.UserInfo, userID, clientID string, scopes []string) error {
	return nil
}

// SetUserinfoFromRequest fills the userinfo claims of the id_token (sub,
// name, email, email_verified). This is the substitution point that the
// library documents (pkg/op/storage.go:147-151).
//
// WHY IT IS MANDATORY -- the library chain (zitadel/oidc/v3@v3.51.8):
//
//   - pkg/op/token.go:241: the "else if len(scopes) > 0" branch. Since
//     openid is always in the scope, it is entered on EVERY id_token
//     generation.
//   - :242-246: an EMPTY oidc.UserInfo is created and
//     SetUserinfoFromScopes is called (ours returns empty).
//   - :247-251: if the storage implements op.CanSetUserinfoFromRequest this
//     method is called -- this is the only chance to fill it.
//   - :253: claims.SetUserInfo(userInfo).
//   - pkg/oidc/token.go:158-159: the body of SetUserInfo is
//     "t.Subject = i.Subject" -- an UNCONDITIONAL assignment.
//
// So without this method the empty userinfo OVERWRITES the Subject that
// NewIDTokenClaims wrote earlier and the id_token comes out without a sub.
// sub is REQUIRED for the ID Token in OIDC Core 1.0 §2: a standard RP
// (AppAuth, oidc-client-ts, zitadel's own rp package) rejects such a token
// or reads the user identity as empty.
//
// BEHAVIOR CHANGE: the ByID error of fillUserinfo is now RETURNED to the
// library, so if the user record cannot be read the token generation
// FAILS. Before, SetUserinfoFromScopes returned nil unconditionally and an
// id_token without sub was produced. This is the fail-closed direction:
// not issuing an identity token for a user who cannot be verified is
// better than issuing an incomplete token.
func (s *Storage) SetUserinfoFromRequest(ctx context.Context, userinfo *oidc.UserInfo, request op.IDTokenRequest, scopes []string) error {
	return s.fillUserinfo(ctx, userinfo, request.GetSubject(), scopes)
}

// SetUserinfoFromToken is called by the /userinfo endpoint: it finds the
// access token record and fills the claims according to the scope.
func (s *Storage) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, subject, origin string) error {
	record, err := s.tokens.GetAccess(ctx, tokenID)
	if err != nil {
		return err
	}
	// subject is the user the library knows independently in the
	// id_token/access context; if it does not match record.UserID the token
	// belongs to ANOTHER user (e.g. a tokenID collision or a bug in the
	// calling layer) -- we must not silently return someone else's
	// information.
	if record.UserID != subject {
		return fmt.Errorf("jeton bu kullanici icin verilmemis")
	}
	return s.fillUserinfo(ctx, userinfo, record.UserID, record.Scopes)
}

// SetIntrospectionFromToken is called by the /introspect endpoint.
func (s *Storage) SetIntrospectionFromToken(ctx context.Context, introspection *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	record, err := s.tokens.GetAccess(ctx, tokenID)
	if err != nil {
		return err
	}
	if record.ClientID != clientID {
		return fmt.Errorf("jeton bu istemci icin verilmemis")
	}
	introspection.Scope = record.Scopes
	introspection.ClientID = record.ClientID

	userinfo := new(oidc.UserInfo)
	if err := s.fillUserinfo(ctx, userinfo, record.UserID, record.Scopes); err != nil {
		return err
	}
	introspection.SetUserInfo(userinfo)
	return nil
}

// fillUserinfo writes the user information into the claims according to
// the scope. The profile scope opens the name, the email scope opens the
// email and its verification status; no other scope is defined
// (makesinger's only authentication method is email+password, there is no
// extra profile data).
func (s *Storage) fillUserinfo(ctx context.Context, userinfo *oidc.UserInfo, userID string, scopes []string) error {
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		return err
	}
	userinfo.Subject = user.ID
	for _, scope := range scopes {
		switch scope {
		case oidc.ScopeProfile:
			userinfo.Name = user.Name
		case oidc.ScopeEmail:
			userinfo.Email = user.Email
			userinfo.EmailVerified = oidc.Bool(user.EmailVerified)
		}
	}
	return nil
}

// GetPrivateClaimsFromScopes is for adding private claims to the JWT
// access token. Makesinger has no private scope; the standard scopes are
// already carried through the id_token and userinfo.
func (s *Storage) GetPrivateClaimsFromScopes(ctx context.Context, userID, clientID string, scopes []string) (map[string]any, error) {
	return nil, nil
}

// GetKeyByIDAndClientID is for JWT profile / private_key_jwt client
// authentication. Not supported: our single client is public and has no
// secret.
func (s *Storage) GetKeyByIDAndClientID(ctx context.Context, keyID, clientID string) (*jose.JSONWebKey, error) {
	return nil, fmt.Errorf("private_key_jwt destegi yok")
}

// ValidateJWTProfileScopes is for the JWT Profile Authorization Grant
// (RFC 7523). Not supported: makesinger only uses authorization code +
// PKCE and refresh_token.
func (s *Storage) ValidateJWTProfileScopes(ctx context.Context, userID string, scopes []string) ([]string, error) {
	return nil, fmt.Errorf("JWT profile grant desteklenmiyor")
}

// Health is called by the /healthz of the secondary listener.
//
// FAIL-CLOSED: if SetHealthDeps was never called (pool or rdb is nil),
// Health EXPLICITLY counts that as an error. In an earlier version
// "success" was returned in this case; if SetHealthDeps were FORGOTTEN in
// production (and it is not called from anywhere in this file -- the
// bootstrap code is the job of a separate task) that would have made
// /healthz say "healthy" even while Postgres and Redis were dead. So that
// Storage can still be tested without wiring the dependencies, the tests
// either call SetHealthDeps with real connections or expect this error.
func (s *Storage) Health(ctx context.Context) error {
	if s.pool == nil || s.rdb == nil {
		return fmt.Errorf("saglik bagimliliklari baglanmadi: SaglikBagla cagrilmadi")
	}
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres saglik kontrolu basarisiz: %w", err)
	}
	if err := s.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis saglik kontrolu basarisiz: %w", err)
	}
	return nil
}
