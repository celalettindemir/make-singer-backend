package identity

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// If the issuer is empty the OP must not start at all: a discovery document
// produced with an empty issuer is unusable and the error would only show up
// at the first login attempt.
func TestStart_EmptyIssuerFails(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = ""
	if _, err := Start(context.Background(), cfg, nil, "development"); err == nil {
		t.Error("bos issuer ile baslatildi")
	}
}

// It must not start if the secrets are missing; silently falling back to a
// weak default would go unnoticed in production.
func TestStart_MissingSecretsFails(t *testing.T) {
	base := func() *config.AuthConfig {
		c := testAuthCfg()
		c.DBURL = "postgres://yok/yok"
		c.SigningKeyPEM = "gecersiz"
		c.CryptoKey = "kisa"
		return c
	}
	if _, err := Start(context.Background(), base(), nil, "development"); err == nil {
		t.Error("gecersiz sirlarla baslatildi")
	}
}

// http:// issuer + local host (localhost) + a non-production environment:
// insecure mode is allowed, so Start PASSES the http/https check. Since the
// secrets (SigningKeyPEM etc.) are missing in testAuthCfg, Start still
// returns an error, but that error must NOT BE ABOUT https -- otherwise it
// means the http check itself is blocking.
func TestValidateInsecureIssuer_LocalHTTPAllowed(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = "http://localhost:8001"
	_, err := Start(context.Background(), cfg, nil, "development")
	if err == nil {
		t.Fatal("sirlar eksikken baslatildi (beklenmiyordu)")
	}
	if strings.Contains(strings.ToLower(err.Error()), "https") {
		t.Errorf("http+yerel+development icin https hatasi donmemeli, ama dondu: %v", err)
	}
}

// http:// issuer but the host is not local (a production-like domain):
// insecure mode is not allowed, the https-required error must be returned
// and Start must stop without opening any connection.
func TestValidateInsecureIssuer_RemoteHostRejected(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = "http://auth.example.com"
	_, err := Start(context.Background(), cfg, nil, "development")
	if err == nil {
		t.Fatal("uzak host'lu http issuer ile baslatildi")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "https") {
		t.Errorf("https zorunlu hatasi beklenirdi, gelen: %v", err)
	}
}

// http:// issuer + production environment: not allowed even if the host is
// localhost. If AUTH_ISSUER is mistakenly written as http:// in production,
// OIDC without TLS must not silently come up.
func TestValidateInsecureIssuer_ProductionRejected(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = "http://localhost:8001"
	_, err := Start(context.Background(), cfg, nil, "production")
	if err == nil {
		t.Fatal("production ortaminda http issuer ile baslatildi")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "https") {
		t.Errorf("https zorunlu hatasi beklenirdi, gelen: %v", err)
	}
}

// ---- I2: invalid/0 duration values ----

// viper does NOT fall back to the default on a duration it CANNOT PARSE; it
// silently gives 0s (e.g. AUTH_ACCESS_TTL="15min", AUTH_REFRESH_TTL="60d" --
// the natural spellings of the "15 minutes"/"60 days" in the docs).
// A 0 TTL is destructive: with AccessTTL=0 the access record is never
// written and the token is produced with exp=now (every /api/* gets 401),
// with RefreshTTL=0 every refresh record dies the moment it is born (all
// users are logged out instantly). Start did NOT VALIDATE these AT ALL and
// returned without an error.
func TestStart_InvalidTTLFails(t *testing.T) {
	statuses := []struct {
		name   string
		mutate func(*config.AuthConfig)
		key    string
	}{
		{"access_ttl 0", func(c *config.AuthConfig) { c.AccessTTL = 0 }, "access_ttl"},
		{"access_ttl negatif", func(c *config.AuthConfig) { c.AccessTTL = -time.Minute }, "access_ttl"},
		{"refresh_ttl 0", func(c *config.AuthConfig) { c.RefreshTTL = 0 }, "refresh_ttl"},
		{"refresh_ttl negatif", func(c *config.AuthConfig) { c.RefreshTTL = -time.Hour }, "refresh_ttl"},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			cfg := testAuthCfg()
			d.mutate(cfg)
			_, err := Start(context.Background(), cfg, nil, "development")
			if err == nil {
				t.Fatal("gecersiz TTL ile baslatildi")
			}
			// Verify it is rejected FOR THE REASON THE TEST NAME SAYS:
			// the secrets are missing in testAuthCfg too, so the "an error was
			// returned" check would be vacuous.
			if !strings.Contains(err.Error(), d.key) {
				t.Errorf("hata %q icermeli, gelen: %v", d.key, err)
			}
		})
	}
}

// ---- I3: empty ClientID / RedirectURIs ----

// When ClientID is empty middleware.NewOPAuthMiddleware falls into setupErr
// and rejects EVERY /api/* request with 401; meanwhile /health still
// returned "identity": true (a total outage + a false health signal).
// When RedirectURIs is empty every /authorize is rejected and no user can
// log in. config.yaml SAID this was forbidden but the code did not enforce it.
func TestStart_EmptyClientConfigFails(t *testing.T) {
	statuses := []struct {
		name   string
		mutate func(*config.AuthConfig)
		key    string
	}{
		{"client_id bos", func(c *config.AuthConfig) { c.ClientID = "" }, "client_id"},
		{"redirect_uris nil", func(c *config.AuthConfig) { c.RedirectURIs = nil }, "redirect_uris"},
		{"redirect_uris bos dilim", func(c *config.AuthConfig) { c.RedirectURIs = []string{} }, "redirect_uris"},
		{"redirect_uris bos girdi", func(c *config.AuthConfig) { c.RedirectURIs = []string{"  "} }, "redirect_uris"},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			cfg := testAuthCfg()
			d.mutate(cfg)
			_, err := Start(context.Background(), cfg, nil, "development")
			if err == nil {
				t.Fatal("bos istemci yapilandirmasi ile baslatildi")
			}
			if !strings.Contains(err.Error(), d.key) {
				t.Errorf("hata %q icermeli, gelen: %v", d.key, err)
			}
		})
	}
}

// A valid configuration must not trip ANY of these validations: otherwise the
// tests above would also hold for "everything is rejected".
func TestValidateConfig_ValidPasses(t *testing.T) {
	if err := validateConfig(testAuthCfg()); err != nil {
		t.Errorf("gecerli yapilandirma reddedildi: %v", err)
	}
}

// Apple federation cannot work without all three fields
// (TeamID/KeyID/KeyP8): the client_secret is produced from these on every
// exchange (see K5). What is missing must fail at STARTUP, not be a silent
// breakage discovered when the user tries to log in with Apple.
func TestValidateConfig_AppleMissingFieldsFails(t *testing.T) {
	statuses := []struct {
		name      string
		federated config.FederationClient
	}{
		{"ekip id eksik", config.FederationClient{Name: "apple", ClientID: "tr.makesinger.giris", AppleKeyID: "AB12", AppleKeyP8: "pem-verisi"}},
		{"anahtar id eksik", config.FederationClient{Name: "apple", ClientID: "tr.makesinger.giris", AppleTeamID: "TEAM1", AppleKeyP8: "pem-verisi"}},
		{"anahtar p8 eksik", config.FederationClient{Name: "apple", ClientID: "tr.makesinger.giris", AppleTeamID: "TEAM1", AppleKeyID: "AB12"}},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			cfg := testAuthCfg()
			cfg.Federation = []config.FederationClient{d.federated}
			if err := validateConfig(cfg); err == nil {
				t.Fatal("eksik apple alanlariyla dogrulama gecti")
			}
		})
	}
}

// If Apple's three fields are all set AND the .p8 can REALLY be parsed the
// validation must pass (vacuous rejection check).
//
// Previously a placeholder such as "pem-verisi" was enough for AppleKeyP8;
// this test PINNED the door I1 opened by accepting that placeholder. Now a
// real key is generated.
func TestValidateConfig_AppleCompletePasses(t *testing.T) {
	p8, _ := generateTestP8(t)
	cfg := testAuthCfg()
	cfg.Federation = []config.FederationClient{{
		Name: "apple", ClientID: "tr.makesinger.giris",
		AppleTeamID: "TEAM1", AppleKeyID: "AB12", AppleKeyP8: p8,
	}}
	if err := validateConfig(cfg); err != nil {
		t.Errorf("tam apple yapilandirmasi reddedildi: %v", err)
	}
}

// I1: the fields being "non-empty" is NOT ENOUGH. The most common real
// mistake with PEM secrets in K8s is mounting the key on a single line with
// literal \n instead of real newlines. Such a value is not empty, so it
// passes the old check; the pod becomes Ready, the "Sign in with Apple"
// button shows, and the error only appears as a meaningless 403 session page
// AFTER the user FINISHES Face ID + two-step approval at Apple.
// It MUST be caught at startup.
func TestValidateConfig_AppleP8ParsedAtStartup(t *testing.T) {
	real, _ := generateTestP8(t)
	// The exact input of the real deployment mistake: the same key with the
	// newlines converted to the literal \n sequence.
	escapedLiteral := strings.ReplaceAll(real, "\n", `\n`)

	statuses := []struct {
		name string
		p8   string
	}{
		{"literal \\n ile tek satira dusmus p8", escapedLiteral},
		{"PEM olmayan yer tutucu", "pem-verisi"},
		{"PEM govdesi bozuk", "-----BEGIN PRIVATE KEY-----\nbu-base64-degil\n-----END PRIVATE KEY-----\n"},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			cfg := testAuthCfg()
			cfg.Federation = []config.FederationClient{{
				Name: "apple", ClientID: "tr.makesinger.giris",
				AppleTeamID: "TEAM1", AppleKeyID: "AB12", AppleKeyP8: d.p8,
			}}
			err := validateConfig(cfg)
			if err == nil {
				t.Fatal("ayristirilamayan .p8 ile dogrulama GECTI; baslangicta reddedilmeliydi")
			}
			if !strings.Contains(err.Error(), "AUTH_FED_APPLE_KEY_P8") {
				t.Errorf("hata operatore ne yapacagini soylemiyor: %v", err)
			}
			// The error message must not leak the key CONTENT: neither the raw value
			// nor its base64 body should end up in logs/stderr.
			if strings.Contains(err.Error(), d.p8) {
				t.Errorf("hata mesaji anahtar icerigini sizdiriyor: %v", err)
			}
		})
	}
}

// I2: Google's only mandatory secret is client_secret. When it is empty the
// token exchange fails with 401 invalid_client and the user sees 403; the pod
// is Ready, metrics are clean, there is no alarm. It was an obvious
// asymmetry that Apple had a fail-closed startup check and Google did not.
func TestValidateConfig_GoogleWithoutSecretFails(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Federation = []config.FederationClient{{
		Name: "google", ClientID: "123.apps.googleusercontent.com",
	}}
	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("client_secret bos iken dogrulama GECTI")
	}
	if !strings.Contains(err.Error(), "AUTH_FED_GOOGLE_CLIENT_SECRET") {
		t.Errorf("hata operatore ne yapacagini soylemiyor: %v", err)
	}
}

// Vacuous rejection check: if the secret is set, Google must pass. It also
// pins the RIGHT side of the rule: the "client_id is empty too" case is NOT
// an error -- that provider never enters the list in
// setupFederation, so it never reaches validateConfig, and an empty
// federation list is valid.
func TestValidateConfig_GoogleWithSecretPasses(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Federation = []config.FederationClient{{
		Name: "google", ClientID: "123.apps.googleusercontent.com", ClientSecret: "gizli",
	}}
	if err := validateConfig(cfg); err != nil {
		t.Errorf("tam google yapilandirmasi reddedildi: %v", err)
	}

	cfg.Federation = nil
	if err := validateConfig(cfg); err != nil {
		t.Errorf("federasyon KAPALI yapilandirma reddedildi: %v", err)
	}
}

// ---- I3: /health must reflect REAL readiness ----

// Ready() answers "is the listener serving right now", not "did Start
// return successfully". It must also be safe for a nil receiver:
// /health calls it even when the OP was never set up.
func TestServer_ReadyReflectsListener(t *testing.T) {
	var missing *Server
	if missing.Ready() {
		t.Error("nil Sunucu hazir gorundu")
	}

	s := &Server{}
	if s.Ready() {
		t.Error("baslatilmamis Sunucu hazir gorundu")
	}
	s.ready.Store(true)
	if !s.Ready() {
		t.Error("hazir=true iken Hazir() false dondu")
	}
	// If the listener crashes (or closes) /health must see it.
	s.ready.Store(false)
	if s.Ready() {
		t.Error("dinleyici durduktan sonra hala hazir gorunuyor")
	}
}

// Phase 1's fail-closed rule: if the rate limiter CANNOT BE SET UP, Start
// must never succeed. Coming up with a login endpoint that has no limit
// means both unlimited brute force and a CPU DoS surface that takes down
// the main API (see ratelimit.go).
//
// WHY SO MUCH SETUP: the rate limiter is set up inside Start AFTER the
// database and migrations. If the rest of the configuration is left
// incomplete Start already fails earlier and the test NEVER measures the
// rate limiter path. So a real Postgres and Redis are used here; if they
// are absent the test is skipped (see testRedis, testPostgres).
func TestStart_FailsWhenRateLimiterInvalid(t *testing.T) {
	rdb := testRedis(t)
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}

	base := func() *config.AuthConfig {
		c := testAuthCfg()
		c.DBURL = dsn
		c.SigningKeyPEM = testPEM(t)
		c.CryptoKey = testCryptoKey
		c.Port = "0"
		c.LoginIPPerMin = 10
		c.LoginEmailPerHour = 10
		c.SignupIPPerHour = 10
		return c
	}

	// First a HEALTH check: the same configuration must be able to take Start
	// all the way to the rate limiter while the limit values are VALID.
	// Otherwise the assertion below would have verified an error unrelated to
	// the limit.
	server, err := Start(context.Background(), base(), rdb, "development")
	if err != nil {
		if strings.Contains(err.Error(), "veritabanina baglanilamadi") {
			t.Skip("yerel Postgres yok, atlaniyor")
		}
		t.Fatalf("gecerli yapilandirma hiz limitine gelemeden dustu: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if closeErr := server.Shutdown(ctx); closeErr != nil {
		t.Fatalf("Kapat: %v", closeErr)
	}

	// Now we break ONLY the limit value.
	broken := base()
	broken.LoginIPPerMin = 0
	s, err := Start(context.Background(), broken, rdb, "development")
	if err == nil {
		if s != nil {
			_ = s.Shutdown(context.Background())
		}
		t.Fatal("hiz limiti kurulamadigi halde Start basarili oldu")
	}
	if s != nil {
		t.Error("Start hata dondu ama Sunucu da dondurdu")
	}
	if !strings.Contains(err.Error(), "hiz limiti kurulamadi") {
		t.Errorf("hata hiz limitiyle ilgili degil: %v", err)
	}
}
