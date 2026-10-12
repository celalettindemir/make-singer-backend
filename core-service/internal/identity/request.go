package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

const (
	requestTTL = 10 * time.Minute
	codeTTL    = 60 * time.Second
)

// amr claim values: they say HOW the user was authenticated.
// amrPassword is used on the hosted email+password login/signup path;
// amrFederation is used on the Apple/Google sign-in and link-to-existing-
// account paths. These two values are ALL the amr values passed to MarkDone
// (see K6).
const (
	amrPassword   = "pwd"
	amrFederation = "fed"
)

// ErrRequestNotFound is returned when the requested auth request (or code)
// cannot be found in Redis. It may have expired, never been created, or (for
// a code) already been consumed.
var ErrRequestNotFound = errors.New("auth istegi bulunamadi")

// ErrRequestAlreadyCompleted is returned when an already-completed auth
// request is asked to be bound to ANOTHER user again (see MarkDone).
var ErrRequestAlreadyCompleted = errors.New("auth istegi zaten tamamlandi")

// AuthRequest is the authorization request record that satisfies the
// op.AuthRequest interface and is stored in Redis as JSON. All fields MUST
// be exported (capitalized); otherwise encoding/json silently skips them
// and data is lost when the record is written to Redis.
type AuthRequest struct {
	ID                  string
	ClientID            string
	RedirectURI         string
	Scopes              []string
	ResponseType        oidc.ResponseType
	ResponseMode        oidc.ResponseMode
	State               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod oidc.CodeChallengeMethod

	// HintSubject is the "sub" value that comes with id_token_hint (the library
	// supplies it in CreateAuthRequest's userID parameter). It is kept ONLY as a
	// hint: it is neither used as the request ID nor does it affect Done().
	// Using it as the request ID would let two concurrent flows overwrite the
	// same Redis key.
	HintSubject string `json:"IpucuSubject"`

	// Filled in when login completes.
	Subject  string
	AuthTime time.Time

	// AMR is filled in when login completes (see MarkDone) and says HOW the
	// user was authenticated (amrPassword or amrFederation). It MUST be
	// exported: encoding/json silently skips lowercase fields, which would make
	// amr disappear when read back from Redis.
	AMR []string
}

var _ op.AuthRequest = (*AuthRequest)(nil)

func (a *AuthRequest) GetID() string { return a.ID }

// GetACR: makesinger uses a single authentication class, so we return a
// constant/empty value. If multiple ACRs are needed it is extended here.
func (a *AuthRequest) GetACR() string { return "" }

// GetAMR says HOW the user was authenticated and is computed per request.
// It used to return a constant []string{"pwd"}; "pwd" appearing in the
// token of a user who signed in with Google is A FALSE STATEMENT.
//
// If the field is empty "pwd" is NOT assumed: silently turning a caller that
// forgot to supply amr into a false statement is the most expensive way of
// hiding the bug. So an empty slice is returned, not "pwd" -- the next
// reader may feel the urge to "fix" this with a "more sensible" default;
// they must not.
func (a *AuthRequest) GetAMR() []string {
	if len(a.AMR) == 0 {
		return []string{}
	}
	return a.AMR
}

// GetAudience: it must include at least the client ID (the op library uses
// it in the token's "aud" field).
func (a *AuthRequest) GetAudience() []string { return []string{a.ClientID} }

func (a *AuthRequest) GetAuthTime() time.Time { return a.AuthTime }

func (a *AuthRequest) GetClientID() string { return a.ClientID }

// GetCodeChallenge: if Challenge is empty it means PKCE is not used, and
// returning nil is mandatory (the op library interprets nil as "no PKCE").
//
// FAIL-CLOSED: if the method is NOT S256 (plain or empty) we DO NOT PASS the
// challenge on. With an empty/plain method the library does a plain string
// comparison (pkg/oidc/code_challenge.go VerifyCodeChallenge) and this
// effectively disables PKCE. Returning nil makes the token endpoint reject
// the request with "PKCE required" for a public client (pkg/op/token_code.go,
// the AuthMethodNone branch). The first layer is rejecting at /authorize
// (see pkceEnforcer, server.go); this second layer closes the code exchange
// even if that check is bypassed.
func (a *AuthRequest) GetCodeChallenge() *oidc.CodeChallenge {
	if a.CodeChallenge == "" || a.CodeChallengeMethod != oidc.CodeChallengeMethodS256 {
		return nil
	}
	return &oidc.CodeChallenge{
		Challenge: a.CodeChallenge,
		Method:    a.CodeChallengeMethod,
	}
}

func (a *AuthRequest) GetNonce() string { return a.Nonce }

func (a *AuthRequest) GetRedirectURI() string { return a.RedirectURI }

func (a *AuthRequest) GetResponseType() oidc.ResponseType { return a.ResponseType }

func (a *AuthRequest) GetResponseMode() oidc.ResponseMode { return a.ResponseMode }

func (a *AuthRequest) GetScopes() []string { return a.Scopes }

func (a *AuthRequest) GetState() string { return a.State }

func (a *AuthRequest) GetSubject() string { return a.Subject }

// Done: returns true only if Subject is set AND AuthTime is non-zero.
// Critical for security: if Done() returned true before login completed, the
// authorization code would be issued without the user ever being verified.
func (a *AuthRequest) Done() bool {
	return a.Subject != "" && !a.AuthTime.IsZero()
}

// RequestStore keeps authorization requests and single-use authorization
// codes in Redis.
//
// Key scheme:
//
//	kimlik:istek:<id>   -> JSON, TTL 10 minutes  (until login completes)
//	kimlik:kod:<code>   -> request id, TTL 60 seconds
type RequestStore struct {
	rdb *redis.Client
}

func NewRequestStore(rdb *redis.Client) *RequestStore { return &RequestStore{rdb: rdb} }

func requestKey(id string) string { return "kimlik:istek:" + id }
func codeKey(code string) string  { return "kimlik:kod:" + code }

// Create turns the incoming authorization request into an AuthRequest and
// writes it to Redis. The request ID is ALWAYS generated here: the userID
// parameter the library gives to CreateAuthRequest is the id_token_hint's
// "sub" value, NOT a request ID (pkg/op/auth_request.go,
// ValidateAuthReqIDTokenHint). Using it as the ID would let two concurrent
// flows overwrite the same Redis key; so the hint is stored only in the
// HintSubject field.
func (d *RequestStore) Create(ctx context.Context, req *oidc.AuthRequest, hintSubject string) (*AuthRequest, error) {
	request := &AuthRequest{
		ID:                  uuid.NewString(),
		HintSubject:         hintSubject,
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		Scopes:              []string(req.Scopes),
		ResponseType:        req.ResponseType,
		ResponseMode:        req.ResponseMode,
		State:               req.State,
		Nonce:               req.Nonce,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
	}
	if err := d.write(ctx, request, requestTTL); err != nil {
		return nil, err
	}
	return request, nil
}

func (d *RequestStore) write(ctx context.Context, request *AuthRequest, ttl time.Duration) error {
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("istek serilenemedi: %w", err)
	}
	if err := d.rdb.Set(ctx, requestKey(request.ID), data, ttl).Err(); err != nil {
		return fmt.Errorf("istek yazilamadi: %w", err)
	}
	return nil
}

// GetByID reads the authorization request with the given id.
func (d *RequestStore) GetByID(ctx context.Context, id string) (*AuthRequest, error) {
	data, err := d.rdb.Get(ctx, requestKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("istek okunamadi: %w", err)
	}
	var request AuthRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("istek cozumlenemedi: %w", err)
	}
	return &request, nil
}

// SaveCode maps the code belonging to a completed authorization request to
// the request id. The code becomes invalid on its own after 60 seconds.
func (d *RequestStore) SaveCode(ctx context.Context, id string, code string) error {
	if err := d.rdb.Set(ctx, codeKey(code), id, codeTTL).Err(); err != nil {
		return fmt.Errorf("kod kaydedilemedi: %w", err)
	}
	return nil
}

// GetByCode resolves the code to a request id and deletes the code
// ATOMICALLY (GETDEL): the code is single-use, a second read returns
// ErrRequestNotFound. So someone who grabs the code from the address bar or
// logs cannot obtain a token if the code was already used.
func (d *RequestStore) GetByCode(ctx context.Context, code string) (*AuthRequest, error) {
	id, err := d.rdb.GetDel(ctx, codeKey(code)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("kod okunamadi: %w", err)
	}
	return d.GetByID(ctx, id)
}

// Delete removes the authorization request from Redis (for example, used
// when the code exchange completes).
func (d *RequestStore) Delete(ctx context.Context, id string) error {
	if err := d.rdb.Del(ctx, requestKey(id)).Err(); err != nil {
		return fmt.Errorf("istek silinemedi: %w", err)
	}
	return nil
}

// MarkDone reads the request, fills in Subject and AuthTime and writes it
// back. The remaining TTL is read and re-applied for the same duration;
// otherwise every rewrite resets the TTL and the request lives longer than
// necessary (or an expired record stays forever).
//
// SECOND CALL: the Subject of an already-completed request is NOT OVERWRITTEN.
//   - For the same user: NO-OP (returns nil, nothing is rewritten).
//     Rationale: an honest double submit (sending the form twice, the back
//     button) should not show the user an error; the result is already the
//     desired state. Not rewriting also ensures AuthTime keeps the real
//     moment of login.
//   - For ANOTHER user: REJECTED (ErrRequestAlreadyCompleted).
//     Rationale: this was itself the way to redirect a pending auth request
//     to as many different identities as desired until the code was
//     generated; it must fail explicitly instead of overwriting silently.
//
// amr is also NOT OVERWRITTEN in the NO-OP case: the first real login
// method is the correct one. (If the second call is rejected anyway, what
// amr is does not matter; only, since the NO-OP branch does no rewrite at
// all, amr is preserved on its own.)
func (d *RequestStore) MarkDone(ctx context.Context, id string, userID string, amr []string) error {
	request, err := d.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if request.Done() {
		if request.Subject == userID {
			return nil
		}
		return ErrRequestAlreadyCompleted
	}
	ttl, err := d.rdb.TTL(ctx, requestKey(id)).Result()
	if err != nil {
		return fmt.Errorf("TTL okunamadi: %w", err)
	}
	if ttl <= 0 {
		ttl = requestTTL
	}
	request.Subject = userID
	request.AuthTime = time.Now()
	request.AMR = amr
	return d.write(ctx, request, ttl)
}

// ---- SessionStore ----
//
// The cookie binding is kept in the SAME store as the auth request with the
// SAME TTL: a binding outliving the request is meaningless, and one with a
// shorter life breaks the flow midway.

func sessionKey(session string) string { return "kimlik:oturum:" + session }

var _ SessionStore = (*RequestStore)(nil)

// WriteSession maps the cookie value (session) to the auth request and the
// CSRF token.
func (d *RequestStore) WriteSession(ctx context.Context, session string, binding SessionBinding, ttl time.Duration) error {
	data, err := json.Marshal(binding)
	if err != nil {
		return fmt.Errorf("oturum serilenemedi: %w", err)
	}
	if err := d.rdb.Set(ctx, sessionKey(session), data, ttl).Err(); err != nil {
		return fmt.Errorf("oturum yazilamadi: %w", err)
	}
	return nil
}

// ReadSession returns the auth request and CSRF token bound to the cookie
// value. If there is no record it returns ErrSessionNotFound.
func (d *RequestStore) ReadSession(ctx context.Context, session string) (SessionBinding, error) {
	data, err := d.rdb.Get(ctx, sessionKey(session)).Bytes()
	if errors.Is(err, redis.Nil) {
		return SessionBinding{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionBinding{}, fmt.Errorf("oturum okunamadi: %w", err)
	}
	var binding SessionBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		return SessionBinding{}, fmt.Errorf("oturum cozumlenemedi: %w", err)
	}
	return binding, nil
}
