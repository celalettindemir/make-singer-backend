package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Upstream carries everything needed to build the /authorize address of a
// single upstream provider (Apple or Google). JWKSURL and Issuer are given
// to Task 2's auth.NewUpstreamVerifier; this struct does not verify
// anything itself, it only carries the configuration.
type Upstream struct {
	Name, ClientID, ClientSecret string

	AuthURL, TokenURL, JWKSURL, Issuer string

	Scopes []string

	// SupportsPKCE: Google supports PKCE and we ALWAYS use it (public client,
	// no secret). Apple's PKCE support is NOT DOCUMENTED; sending an unknown
	// parameter carries the risk of breaking the flow, so for Apple this field
	// is DELIBERATELY left false (see TestUpstream_AuthorizeURLAppleOmitsPKCE).
	// This is not an oversight, it is a recorded decision: if someone later
	// tries to add PKCE to Apple too in the name of "consistency", this
	// comment should stop them.
	SupportsPKCE bool

	// FormPost: when the name/email scope is requested, Apple returns its
	// response with POST (response_mode=form_post) instead of GET. The return
	// handler therefore MUST accept POST too (see K2).
	FormPost bool

	// Apple-specific (K5). Left empty for Google. ExchangeCode (Task 4)
	// produces the client_secret from these three fields with ES256 on EVERY
	// token exchange; Apple's secret is not constant, it is a short-lived JWT.
	AppleTeamID, AppleKeyID, AppleKeyP8 string
}

// AuthorizeURL builds the authorize address of the upstream provider.
// redirect_uri is OUR return address and must be registered IDENTICALLY on
// the provider side.
//
// codeVerifier never enters the URL as plain text: if PKCE is supported,
// only the base64url of its SHA-256 digest (code_challenge) is sent. The
// verifier itself is stored only on our side (via WriteState) and is sent
// directly to the provider at the token exchange.
func (ic *Upstream) AuthorizeURL(state, nonce, codeVerifier, callbackURL string) string {
	q := url.Values{}
	q.Set("client_id", ic.ClientID)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(ic.Scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("redirect_uri", callbackURL)
	if ic.FormPost {
		q.Set("response_mode", "form_post")
	}
	if ic.SupportsPKCE {
		sum := sha256.Sum256([]byte(codeVerifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
	}
	return ic.AuthURL + "?" + q.Encode()
}

// codeVerifierBytes: 32 raw bytes become 43 characters when encoded with
// base64url (the lower bound of PKCE RFC 7636). Fewer bytes violate the
// standard, more brings no benefit.
const codeVerifierBytes = 32

// generateCodeVerifier produces a random PKCE "code_verifier" and its S256
// "code_challenge". The error is NOT SWALLOWED: if random generation fails
// (if crypto/rand.Read returns an error), continuing with an empty/weak
// verifier effectively removes the PKCE protection (see session.go,
// the same discipline as randomToken).
func generateCodeVerifier() (verifier, challenge string, err error) {
	b := make([]byte, codeVerifierBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("could not generate the code verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// FederationState carries the whole context moved through the "state"
// parameter between /federasyon/{saglayici}/baslat and
// /federasyon/{saglayici}/geri.
//
// It is DELIBERATELY comparable (==): there is NO slice/map field. This lets
// the test compare directly with read != wanted and at the same time
// guarantees that the record in Redis is minimal and flat.
type FederationState struct {
	// AuthRequestID: the real auth request in our OP (started by the mobile
	// client). When the federation flow finishes, Subject is written to this request.
	AuthRequestID string
	// Nonce: compared with the id_token's nonce claim (against replay, see
	// auth.VerifyWithNonce).
	Nonce string
	// Provider: "google" | "apple". The return handler uses it to verify that
	// the callback belongs to the expected provider.
	Provider string `json:"Saglayici"`
	// Cookie: the cookie value of the browser that started this flow (same
	// rationale as the pattern in session.go: authRequestID alone is not an
	// authorization input, the starting browser and the completing browser must
	// be the same).
	Cookie string `json:"Cerez"`
	// CodeVerifier: the PKCE code_verifier. It is sent to the provider at the
	// token exchange; it MUST NOT BE LOGGED (it is secret in nature).
	CodeVerifier string `json:"KodDogrulayici"`
}

// ErrStateNotFound: there is no FederationState recorded for the state
// (never created, expired, or already consumed).
var ErrStateNotFound = errors.New("federation state not found")

// StateStore defines writing and single-use reading of the federation
// "state" record. Like SessionStore, it is implemented by RequestStore.
type StateStore interface {
	WriteState(ctx context.Context, state string, d FederationState) error
	TakeState(ctx context.Context, state string) (FederationState, error)
}

var _ StateStore = (*RequestStore)(nil)

// federationStateTTL must be long enough for the time the user spends at the
// upstream provider and short enough for a stolen state to lose its value
// quickly. 10 minutes: more than enough, including Apple's two-step
// approval.
const federationStateTTL = 10 * time.Minute

func stateKey(state string) string { return "kimlik:fed:" + state }

// WriteState writes the context at the start of the federation flow to
// Redis under the state key.
func (d *RequestStore) WriteState(ctx context.Context, state string, record FederationState) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("could not serialize the federation state: %w", err)
	}
	if err := d.rdb.Set(ctx, stateKey(state), data, federationStateTTL).Err(); err != nil {
		return fmt.Errorf("could not write the federation state: %w", err)
	}
	return nil
}

// TakeState reads the state record and deletes it ATOMICALLY (GETDEL): the
// record is SINGLE-USE, otherwise an attacker could replay a callback URL
// they captured (address bar, log, proxy) and complete the same federation
// flow a second time. Same pattern as the code exchange (see
// RequestStore.GetByCode).
func (d *RequestStore) TakeState(ctx context.Context, state string) (FederationState, error) {
	data, err := d.rdb.GetDel(ctx, stateKey(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return FederationState{}, ErrStateNotFound
	}
	if err != nil {
		return FederationState{}, fmt.Errorf("could not read the federation state: %w", err)
	}
	var record FederationState
	if err := json.Unmarshal(data, &record); err != nil {
		return FederationState{}, fmt.Errorf("could not decode the federation state: %w", err)
	}
	return record, nil
}
