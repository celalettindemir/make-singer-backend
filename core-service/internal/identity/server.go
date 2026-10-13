// Package identity is the custom OpenID Provider: it carries the second
// HTTP listener of our own OpenID Provider and the resources attached to
// it (signing key, storage, token and user stores, the login, signup and
// federation pages, rate limiting and the database). The API process
// builds it with a single Start call and cleans it up on shutdown with
// Server.Shutdown.
package identity

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"

	"github.com/makeasinger/api/internal/auth"
	"github.com/makeasinger/api/internal/config"
)

// migrationLockKey is the fixed key of the Postgres advisory lock that
// wraps Migrate. It is not random: during a rolling update in the cluster
// every pod with the same name/purpose must ask for the same lock. The
// value was derived from the CRC32 of the text "make-singer kimlik
// migration"; its meaning does not matter, only that it is specific to
// this application and fixed.
const migrationLockKey int64 = 0x6B696D6C696B31 // the byte sequence "kimlik1", a readable signature

// Server carries the second HTTP listener of our own OpenID Provider and
// the resources attached to it. main.go builds it with a single Start call
// and cleans it up on shutdown with Shutdown.
type Server struct {
	srv        *http.Server
	pool       *pgxpool.Pool
	signingKey *SigningKey

	// users is kept for the account deletion endpoint on the API side
	// (see DeleteAccount).
	users UserStore

	// ready tracks whether the listener is REALLY serving. /health used to
	// look only at "identityServer != nil", so it never caught the listener
	// crashing AFTER Start returned (e.g. the accept loop stopping with an
	// error) and still returned "kimlik": true: a full outage + a false
	// health signal.
	ready atomic.Bool

	// fedCancel cancels the JWKS background refresh ctx of the upstream
	// verifiers. It is tied to the lifetime of the SERVER (K7): Shutdown
	// calls it, so the refresh lives as long as the process does.
	fedCancel context.CancelFunc

	// verifiers is kept to be Closed on shutdown. It is EMPTY if
	// federation is not configured.
	verifiers map[string]*auth.UpstreamVerifier
}

// Ready reports that the OP listener is serving right now. /health uses
// it; it is also safe to call on a nil receiver (the OP was never set
// up).
func (s *Server) Ready() bool {
	return s != nil && s.ready.Load()
}

// Start brings up our own OpenID Provider and starts publishing the
// discovery document, the JWKS and the OIDC endpoints on a second HTTP
// listener (cfg.Port). It does not start at all if the issuer is empty: a
// discovery document produced with an empty issuer is unusable (main
// already refuses to start with an empty issuer). env is cfg.Server.Env
// (SERVER_ENV); if the issuer is http:// it is used so that insecure mode
// can only be enabled in local development (see validateInsecureIssuer).
func Start(ctx context.Context, cfg *config.AuthConfig, rdb *redis.Client, env string) (*Server, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("issuer is empty: the identity provider cannot start")
	}

	if err := validateInsecureIssuer(cfg.Issuer, env); err != nil {
		return nil, err
	}

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	signingKey, err := LoadSigningKey(cfg.SigningKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("could not load the signing key: %w", err)
	}

	cryptoKey, err := CryptoKey(cfg.CryptoKey)
	if err != nil {
		return nil, fmt.Errorf("crypto key is invalid: %w", err)
	}

	pool, err := Connect(ctx, cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("could not connect to the database: %w", err)
	}

	if err := migrateLocked(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	userStore := NewPostgresUserStore(pool)
	tokenStore := NewPostgresTokenStore(pool, rdb)
	requestStore := NewRequestStore(rdb)

	storage := NewStorage(cfg, userStore, tokenStore, requestStore, signingKey)
	storage.SetHealthDeps(pool, rdb)

	opCfg := &op.Config{
		CryptoKey: cryptoKey,
		// NOTE: CodeMethodS256 ONLY feeds the discovery document
		// (pkg/op/op.go CodeMethodS256Supported -> pkg/op/discovery.go);
		// no validation uses it, so on its own it ENFORCES NOTHING. WE
		// enforce S256 ourselves, in two layers:
		//   1. /authorize: pkceEnforcer (pkce.go), through the
		//      op.AuthorizeValidator extension point — the request is
		//      rejected if code_challenge is missing or the method is not
		//      S256 (including empty).
		//   2. Token endpoint: AuthRequest.GetCodeChallenge (request.go) is
		//      fail-closed; it returns nil for a method other than S256 and
		//      the library rejects a public client with "PKCE required".
		CodeMethodS256:        true,
		AuthMethodPost:        false, // public client, no secret
		GrantTypeRefreshToken: true,
		SupportedUILocales:    []language.Tag{language.Turkish, language.English},
		SupportedScopes: []string{
			oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess,
		},
	}

	// validateInsecureIssuer passed above; so if we got here with an http://
	// issuer, insecure mode was deliberately allowed (local development /
	// manual verification with Docker). By default the library accepts no
	// issuer other than https.
	var opOpts []op.Option
	if strings.HasPrefix(cfg.Issuer, "http://") {
		opOpts = append(opOpts, op.WithAllowInsecure())
	}

	provider, err := op.NewProvider(opCfg, storage, op.StaticIssuer(cfg.Issuer), opOpts...)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("could not set up the OpenID Provider: %w", err)
	}

	// The Secure flag of the cookie depends on the issuer: ON for https, OFF
	// for http:// local development (otherwise the browser never stores the
	// cookie and the local flow breaks).
	secureCookie := !strings.HasPrefix(cfg.Issuer, "http://")

	// Upstream verifiers (Phase 2). The ctx cancellation chain is
	// DELIBERATELY CUT (context.WithoutCancel): the ctx given to Start may be
	// a startup timeout, and when it expired the hourly JWKS background
	// refresh would stop too. Cancellation is tied to Shutdown, so the
	// refresh lives as long as the server (K7; the rationale is in the
	// auth.NewUpstreamVerifier comment).
	fedCtx, cancelFed := context.WithCancel(context.WithoutCancel(ctx))
	upstreams, verifiers, err := setupFederation(fedCtx, cfg)
	if err != nil {
		cancelFed()
		pool.Close()
		return nil, err
	}

	federatedStore := NewPostgresFederatedStore(pool)

	// Rate limit: for POST /giris and POST /kayit. If it cannot be set up
	// Start RETURNS AN ERROR (fail-closed): coming up with a login endpoint
	// that has no limit means both unlimited brute force and a CPU DoS
	// surface that also takes down the main API. See ratelimit.go.
	rateLimiter, err := NewRateLimiter(rdb, RateLimitConfig{
		LoginIPPerMin:     cfg.LoginIPPerMin,
		LoginEmailPerHour: cfg.LoginEmailPerHour,
		SignupIPPerHour:   cfg.SignupIPPerHour,
		TrustedProxies:    cfg.TrustedProxies,
		Cloudflare:        cfg.Cloudflare,
	}, cryptoKey[:])
	if err != nil {
		cancelFed()
		pool.Close()
		return nil, fmt.Errorf("could not set up the rate limiter: %w", err)
	}

	mux := buildMux(
		provider, userStore, requestStore, federatedStore,
		upstreams, verifiers, cfg.Issuer, secureCookie, rateLimiter,
	)

	// The listener is opened INSIDE Start, SYNCHRONOUSLY. ListenAndServe used
	// to be called inside a goroutine; an error such as a port conflict
	// happened silently AFTER Start returned and /health did not catch it.
	// Now such an error makes Start fail and main.go stops with log.Fatalf:
	// CrashLoopBackOff is the correct and visible signal.
	listener, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		cancelFed()
		pool.Close()
		return nil, fmt.Errorf("could not open the identity listener (port %s): %w", cfg.Port, err)
	}

	srv := &http.Server{
		Handler: mux,
		// M8: there was only ReadHeaderTimeout; there was NO time limit for
		// the body and the response, so a client sending a slow body could
		// hold the connection (and the goroutine) indefinitely (Slowloris
		// style). The values follow the real cost of the endpoint: the most
		// expensive request is bcrypt at ~258 ms, the forms are a few hundred
		// bytes.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	server := &Server{
		srv: srv, pool: pool, signingKey: signingKey, users: userStore,
		fedCancel: cancelFed, verifiers: verifiers,
	}
	server.ready.Store(true)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			// We do not call log.Fatal here: this goroutine is separate from
			// the main flow and stops silently instead of panicking. But it is
			// NO LONGER silent: ready=false is written, /health shows it as
			// "kimlik": false and the cluster does not count the pod as
			// healthy.
			server.ready.Store(false)
			log.Printf("identity listener stopped: %v", err)
		}
	}()

	return server, nil
}

// validateConfig collects the configuration errors for which the OP must
// NOT START AT ALL. All of them were verified live: in all three cases
// Start used to return WITHOUT AN ERROR.
//
//   - ClientID empty: middleware.NewOPAuthMiddleware falls into setupErr and
//     rejects EVERY /api/* request with 401, but /health would still return
//     "kimlik": true / "auth": true — a full outage + a false health
//     signal. config.yaml said this was forbidden but the code did not
//     enforce it.
//   - RedirectURIs empty: every /authorize is rejected, no user can sign
//     in.
//   - AccessTTL <= 0: SaveAccess never writes the record (token_store.go)
//     and the access token is produced with exp=now => every /api/* 401,
//     every /userinfo 403.
//   - RefreshTTL <= 0: every refresh record is dead the moment it is born
//     => all users are logged out instantly.
//
// Duration values of 0 are NOT a made-up scenario: for a duration value
// that cannot be parsed (e.g. "15min", "60d") viper does NOT fall back to
// the default, it silently gives 0s (see the note in config.go).
func validateConfig(cfg *config.AuthConfig) error {
	if cfg.ClientID == "" {
		return fmt.Errorf("client_id is empty: AUTH_CLIENT_ID is required (while it is empty every /api/* request is rejected with 401)")
	}
	if len(cfg.RedirectURIs) == 0 {
		return fmt.Errorf("redirect_uris is empty: AUTH_REDIRECT_URIS is required (while it is empty every /authorize is rejected)")
	}
	for _, uri := range cfg.RedirectURIs {
		if strings.TrimSpace(uri) == "" {
			return fmt.Errorf("redirect_uris contains an empty entry: check the AUTH_REDIRECT_URIS value")
		}
	}
	if cfg.AccessTTL <= 0 {
		return fmt.Errorf("access_ttl must be positive, resolved value: %s (AUTH_ACCESS_TTL must be a valid duration, e.g. 15m)", cfg.AccessTTL)
	}
	if cfg.RefreshTTL <= 0 {
		return fmt.Errorf("refresh_ttl must be positive, resolved value: %s (AUTH_REFRESH_TTL must be a valid duration, e.g. 1440h)", cfg.RefreshTTL)
	}
	// Apple cannot work unless ALL THREE fields are set: the client_secret
	// is produced from these three with ES256 on every token exchange (see
	// K5, Upstream.AppleTeamID/AppleKeyID/AppleKeyP8). This must be an
	// error that stops the start FROM THE BEGINNING, not a silent breakage
	// discovered the moment the user tries to sign in with Apple (Phase 1's
	// "silent zero" class of problem is not repeated here).
	for _, f := range cfg.Federation {
		if f.Name == providerApple {
			if f.AppleTeamID == "" || f.AppleKeyID == "" || f.AppleKeyP8 == "" {
				return fmt.Errorf("apple federation configuration is incomplete: all three of AUTH_FED_APPLE_TEAM_ID, AUTH_FED_APPLE_KEY_ID and AUTH_FED_APPLE_KEY_P8 are required")
			}
			// "Not empty" is NOT ENOUGH: the silent breakage that the comment
			// above forbids was opening up exactly here. The most common real
			// deployment mistake with PEM secrets is mounting the key on a
			// single line with a literal \n instead of real newlines; such a
			// value is NOT EMPTY, so it PASSES the old check, but pem.Decode
			// returns nil. The result: the pod is Ready, the button shows, and
			// the error only appears AFTER the user has finished Face ID +
			// two-step approval at Apple, as a meaningless session page.
			//
			// The key is REALLY parsed and appleClientSecret does that: the .p8
			// parsing logic stands in ONE single place there, and copying it
			// here would mean two places drifting apart separately. The
			// produced secret is DISCARDED; all we are after is the error
			// itself.
			if _, err := appleClientSecret(f.AppleTeamID, f.AppleKeyID, f.ClientID, f.AppleKeyP8, time.Now()); err != nil {
				// The wrapped errors do not carry the key CONTENT (see
				// appleClientSecret): only the error class and the key type.
				// The message tells the operator WHAT to do.
				return fmt.Errorf("apple .p8 key is unusable: AUTH_FED_APPLE_KEY_P8 must be an ECDSA (P-256) private key in a PKCS#8 PEM block; mount the value with REAL newlines (a literal \\n sequence on a single line cannot be parsed as PEM): %w", err)
			}
		}
		// Google's only mandatory secret is client_secret and it was checked
		// nowhere: ExchangeCode sends it EMPTY with form.Set("client_secret",
		// secret), the Google token endpoint returns 401 invalid_client and
		// the user sees 403. The pod is Ready, the metrics are clean, there
		// is no alarm -- having a fail-closed start check for Apple but not
		// for Google was an obvious asymmetry.
		//
		// The rule is DELIBERATELY built on "client_id set but secret empty":
		// if client_id is empty too, the provider NEVER enters the list in
		// buildFederationClients (see the silent-zero rule there) and that is
		// NOT an error, it means "federation off".
		if f.Name == providerGoogle && f.ClientSecret == "" {
			return fmt.Errorf("google federation configuration is incomplete: if AUTH_FED_GOOGLE_CLIENT_ID is given, AUTH_FED_GOOGLE_CLIENT_SECRET is required too (while it is empty the token exchange fails with 401 invalid_client)")
		}
	}
	return nil
}

// upstreamDefaults carries the addresses and scopes of Apple and Google.
//
// The upstream addresses are FIXED and come from the code; ONLY the
// credentials are read from the environment. Fetching discovery on every
// login is a needless network round trip and an extra source of errors;
// leaving the addresses to an environment variable would mean being able
// to come up with a misconfigured issuer/JWKS pair (that is, trusting some
// other party).
var upstreamDefaults = map[string]Upstream{
	providerGoogle: {
		Name:         providerGoogle,
		Issuer:       "https://accounts.google.com",
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		JWKSURL:      "https://www.googleapis.com/oauth2/v3/certs",
		Scopes:       []string{"openid", "email", "profile"},
		SupportsPKCE: true,
		FormPost:     false,
	},
	providerApple: {
		Name:     providerApple,
		Issuer:   "https://appleid.apple.com",
		AuthURL:  "https://appleid.apple.com/auth/authorize",
		TokenURL: "https://appleid.apple.com/auth/token",
		JWKSURL:  "https://appleid.apple.com/auth/keys",
		Scopes:   []string{"openid", "email", "name"},
		// K4: Apple's PKCE support is not documented.
		SupportsPKCE: false,
		// When name/email is requested Apple returns with form_post (see K2).
		FormPost: true,
	},
}

// setupFederation merges the providers in the configuration with the fixed
// addresses in the code and opens an upstream verifier for each of them.
//
// An unknown provider name returns an ERROR: skipping it silently would
// mean nobody notices the absence of a button that the user did not press
// (Phase 1's "silent zero" class).
//
// ctx is the lifetime of the verifiers' JWKS background refresh and must be
// the CALLER's ctx tied to the server lifetime (see Start).
func setupFederation(
	ctx context.Context, cfg *config.AuthConfig,
) (map[string]*Upstream, map[string]*auth.UpstreamVerifier, error) {
	if len(cfg.Federation) == 0 {
		return nil, nil, nil
	}
	upstreams := make(map[string]*Upstream, len(cfg.Federation))
	verifiers := make(map[string]*auth.UpstreamVerifier, len(cfg.Federation))
	// A half-built set is not left behind: on the first error the verifiers
	// opened so far are closed.
	cleanup := func() {
		for _, v := range verifiers {
			_ = v.Close()
		}
	}
	for _, f := range cfg.Federation {
		defaults, ok := upstreamDefaults[f.Name]
		if !ok {
			cleanup()
			return nil, nil, fmt.Errorf("unknown federation provider: %q (supported: %s, %s)", f.Name, providerApple, providerGoogle)
		}
		upstream := defaults // copy: the fixed table is NOT MODIFIED
		upstream.ClientID = f.ClientID
		upstream.ClientSecret = f.ClientSecret
		upstream.AppleTeamID = f.AppleTeamID
		upstream.AppleKeyID = f.AppleKeyID
		upstream.AppleKeyP8 = f.AppleKeyP8

		// audience = OUR client ID: an id_token that the provider issued for
		// another application must not be accepted by us.
		verifier, err := auth.NewUpstreamVerifier(ctx, upstream.JWKSURL, upstream.Issuer, upstream.ClientID)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("could not set up the %s verifier: %w", f.Name, err)
		}
		upstreams[f.Name] = &upstream
		verifiers[f.Name] = verifier
	}
	return upstreams, verifiers, nil
}

// buildMux builds the HTTP surface of the OP. It is a separate function
// from Start so that tests can set up the same wiring (with fake stores)
// and measure the flow end to end over a real listener.
//
// Path PRIORITIES (the Go 1.22+ ServeMux specificity rule): "/authorize"
// and "/authorize/callback" are EXACT path patterns and beat the "/"
// subtree pattern; so both go through our wrapper instead of going to the
// library. The library code is NOT MODIFIED or COPIED: both wrappers
// delegate to the library's own handler after validation.
// FEDERATION (Phase 2): if upstreams is EMPTY the federation endpoints are
// NOT MOUNTED AT ALL and no button shows on the login page. Leaving a half
// surface -- no button but a callable endpoint, or a button but no
// endpoint -- leads the user into error in both directions. When they are
// mounted, the path priorities follow the rule above:
// "/federasyon/{saglayici}/baslat", "/federasyon/{saglayici}/geri" and
// "/baglama" are exact path patterns and beat the "/" subtree pattern.
func buildMux(
	provider *op.Provider,
	userStore UserStore,
	requestStore *RequestStore,
	federatedStore FederatedStore,
	upstreams map[string]*Upstream,
	verifiers map[string]*auth.UpstreamVerifier,
	issuer string,
	secureCookie bool,
	rateLimiter *RateLimiter,
) *http.ServeMux {
	mux := http.NewServeMux()

	// Mounts the /giris and /kayit pages. The POST handlers are wrapped with
	// op.NewIssuerInterceptor: the interceptor puts the issuer into the
	// request context and authCallback (op.AuthCallbackURL) reads it from
	// there. Mounting without wrapping breaks the flow at the redirect
	// stage.
	interceptor := op.NewIssuerInterceptor(provider.IssuerFromRequest)
	pages := NewPages(
		userStore,
		requestStore,
		requestStore, // RequestStore also satisfies SessionStore
		op.AuthCallbackURL(provider),
		secureCookie,
	)
	pages.SetCallbackDelegate(http.HandlerFunc(op.AuthorizeCallbackHandler(provider)))

	if len(upstreams) > 0 {
		names := make([]string, 0, len(upstreams))
		for name := range upstreams {
			names = append(names, name)
		}
		// The button order must be DETERMINISTIC: map iteration in Go is
		// deliberately random, and if we do not sort the buttons swap places
		// on every page load.
		sort.Strings(names)
		pages.SetFederationProviders(names)

		// requestStore satisfies THREE interfaces at once (FlowStore =
		// StateStore + PendingLinkStore, FederationRequestStore and
		// SessionStore); all of them are the short-lived Redis state of the
		// same flow, and wiring them separately would be a setup that can
		// break separately in two places.
		fedPages := NewFederationPages(
			upstreams,
			verifiers,
			requestStore,
			federatedStore,
			userStore,
			requestStore,
			requestStore,
			op.AuthCallbackURL(provider),
			issuer,
			secureCookie,
		)
		// Both wrap their POST handlers with the interceptor (see Mount and
		// MountLink); rateLimiter is NEVER passed as nil, in production Start
		// does not start at all if the limit cannot be set up.
		fedPages.Mount(mux, interceptor, rateLimiter)
		fedPages.MountLink(mux, interceptor, rateLimiter)
	}

	pages.Mount(mux, interceptor, rateLimiter)

	authorizePath := provider.AuthorizationEndpoint().Relative()

	// /authorize: S256 PKCE is made mandatory through the library's
	// AuthorizeValidator extension point (see pkce.go). We call op.Authorize
	// ourselves because the library's own router uses the provider itself,
	// not the validator.
	//
	// WrapAuthorize: the cookie binding is set up ONLY here, for the browser
	// that STARTED the request (see Pages.WrapAuthorize). GET /giris and
	// GET /kayit no longer MINT a binding.
	enforcer := &pkceEnforcer{Provider: provider}
	mux.Handle(authorizePath, interceptor.Handler(pages.WrapAuthorize(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			op.Authorize(w, r, enforcer)
		}))))

	// /authorize/callback: it is NOT PASSED to the library before the cookie
	// binding is verified (see Pages.Callback).
	mux.HandleFunc(authorizePath+"/callback", interceptor.HandlerFunc(pages.Callback))

	// Discovery document: we do not publish what the library produces AS IS,
	// we filter out the unsupported flows (see trimDiscovery).
	mux.Handle(oidc.DiscoveryEndpoint, interceptor.Handler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			doc := op.CreateDiscoveryConfig(r.Context(), provider, provider.Storage())
			trimDiscovery(doc)
			op.Discover(w, doc)
		})))

	// The root path is mounted last: the library's other endpoints
	// (/oauth/token, /userinfo, /keys, /end_session) go through here.
	mux.Handle("/", provider)

	return mux
}

// trimDiscovery cuts the discovery document down to the flows we REALLY
// support.
//
// WHY: zitadel/oidc v3.51.8 does NOT READ these fields from configuration,
// it produces them as CONSTANTS (pkg/op/discovery.go: ResponseTypes always
// returns code + id_token + "id_token token"; GrantTypes always adds
// implicit; GrantTypeJWTAuthorizationSupported always returns true) and
// writes device_authorization_endpoint unconditionally. There is NO field
// in op.Config that restricts these, so we fix the document ourselves
// before publishing it. The library code is NOT MODIFIED or COPIED: the
// document is produced by the library's own op.CreateDiscoveryConfig and we
// only filter out the wrong fields.
//
// This is NOT a SECURITY fix: the reviewer tried the implicit, jwt-bearer
// and device flows, and all of them are correctly rejected. The fix is for
// COMPATIBILITY: a compliant RP that trusted the metadata and tried
// implicit would get an error for no reason.
func trimDiscovery(doc *oidc.DiscoveryConfiguration) {
	// Only the authorization code flow (PKCE S256 mandatory, see
	// pkce.go). id_token / "id_token token" (implicit) is not supported.
	doc.ResponseTypesSupported = []string{string(oidc.ResponseTypeCode)}

	// Supported grants: code + refresh_token. The implicit and
	// urn:ietf:params:oauth:grant-type:jwt-bearer that the library adds
	// unconditionally are DROPPED here.
	doc.GrantTypesSupported = []oidc.GrantType{
		oidc.GrantTypeCode,
		oidc.GrantTypeRefreshToken,
	}

	// The device authorization grant is not implemented (Storage does not
	// satisfy op.DeviceAuthorizationStorage), so advertising the endpoint
	// would be wrong.
	doc.DeviceAuthorizationEndpoint = ""
}

// validateInsecureIssuer decides whether insecure mode
// (op.WithAllowInsecure) may be allowed when the issuer starts with
// http://. The library (zitadel/oidc) decides only by the scheme without
// looking at the host; that is why WE grant the insecure permission only
// when two conditions hold TOGETHER:
//  1. env is not production (if SERVER_ENV=production there is NO
//     permission).
//  2. the host part of the issuer is localhost, 127.0.0.1 or ::1
//     (including the forms with a port, resolved with url.Hostname()).
//
// Otherwise the https-required error is returned and Start stops without
// opening any connection. The issuer is not a secret, it may appear openly
// in the error message and in the warning logs.
func validateInsecureIssuer(issuer, env string) error {
	if !strings.HasPrefix(issuer, "http://") {
		return nil
	}
	if env == "production" {
		return fmt.Errorf("issuer http: https is required in the production environment, insecure mode is not allowed: %s", issuer)
	}
	u, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("could not parse the issuer: %w", err)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("issuer http: insecure mode is only allowed for localhost/127.0.0.1/::1, https is required: %s", issuer)
	}
	log.Printf("WARNING: issuer is http and local, INSECURE mode is on: %s", issuer)
	return nil
}

// migrateLocked wraps Migrate in a Postgres advisory lock. If two pods run
// Migrate at the same time during a rolling update in the cluster, both
// can pass the "not applied" check and hit a schema_migrations primary
// key conflict; the lock prevents that. To take and release the lock on
// the same connection we borrow a single connection from the pool.
func migrateLocked(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("could not get a connection for the migration: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("could not acquire the migration lock: %w", err)
	}
	defer func() {
		// Release the lock on the same connection; a release error does not
		// change the migration result but must not be swallowed silently, it
		// has to leave a trace.
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey); err != nil {
			log.Printf("could not release the migration lock: %v", err)
		}
	}()

	return Migrate(ctx, pool)
}

// Shutdown gracefully stops the HTTP listener and closes the database
// pool. The pool is closed on EVERY PATH even if the listener shutdown
// returns an error; otherwise the pool leaks when Shutdown fails.
func (s *Server) Shutdown(ctx context.Context) error {
	// As soon as shutdown begins /health must say "kimlik": false.
	s.ready.Store(false)
	shutdownErr := s.srv.Shutdown(ctx)
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("could not close the listener: %w", shutdownErr)
	}
	s.pool.Close()
	// The JWKS refresh goroutines live as long as the process does; they are
	// stopped EXPLICITLY on shutdown, otherwise hourly network calls would
	// continue behind a server that has been shut down.
	if s.fedCancel != nil {
		s.fedCancel()
	}
	for name, v := range s.verifiers {
		if err := v.Close(); err != nil {
			log.Printf("could not close the %s verifier: %v", name, err)
		}
	}
	return shutdownErr
}

// APIPublicKey returns the raw RSA public key that the API side will use
// for access token verification. It is deliberately different from
// SigningKey.PublicKey(): that returns the op.Key wrapper for publishing
// the JWKS, while this returns a *rsa.PublicKey directly.
func (s *Server) APIPublicKey() *rsa.PublicKey {
	return &s.signingKey.private.PublicKey
}

// DeleteAccount deletes the user's identity record. Federated links and
// refresh families go away with ON DELETE CASCADE; no new access token can
// be produced. For the access tokens that remain, the deleted-user block
// on the API side comes into play.
func (s *Server) DeleteAccount(ctx context.Context, userID string) error {
	return s.users.Delete(ctx, userID)
}
