package config

import (
	"os"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/clientip"
)

func TestLoad_R2TwoBuckets(t *testing.T) {
	os.Setenv("R2_PUBLIC_BUCKET", "makeasinger-public")
	os.Setenv("R2_PRIVATE_BUCKET", "makeasinger-private")
	os.Setenv("R2_PRESIGN_TTL", "1h")
	defer func() {
		os.Unsetenv("R2_PUBLIC_BUCKET")
		os.Unsetenv("R2_PRIVATE_BUCKET")
		os.Unsetenv("R2_PRESIGN_TTL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.R2.PublicBucket != "makeasinger-public" {
		t.Errorf("PublicBucket = %q, want makeasinger-public", cfg.R2.PublicBucket)
	}
	if cfg.R2.PrivateBucket != "makeasinger-private" {
		t.Errorf("PrivateBucket = %q, want makeasinger-private", cfg.R2.PrivateBucket)
	}
	if cfg.R2.PresignTTL != time.Hour {
		t.Errorf("PresignTTL = %v, want 1h", cfg.R2.PresignTTL)
	}
}

func TestLoad_PresignTTLDefault(t *testing.T) {
	os.Unsetenv("R2_PRESIGN_TTL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.R2.PresignTTL != time.Hour {
		t.Errorf("default PresignTTL = %v, want 1h", cfg.R2.PresignTTL)
	}
}

func TestR2Config_PresignTTLOrDefault(t *testing.T) {
	var nilCfg *R2Config
	if got := nilCfg.PresignTTLOrDefault(); got != time.Hour {
		t.Errorf("for nil config, PresignTTLOrDefault = %v, want 1h", got)
	}

	zeroCfg := &R2Config{}
	if got := zeroCfg.PresignTTLOrDefault(); got != time.Hour {
		t.Errorf("for zero PresignTTL, PresignTTLOrDefault = %v, want 1h", got)
	}

	setCfg := &R2Config{PresignTTL: 2 * time.Hour}
	if got := setCfg.PresignTTLOrDefault(); got != 2*time.Hour {
		t.Errorf("PresignTTLOrDefault = %v, want 2h from config (must not fall back to the fixed 1h)", got)
	}
}

func TestLoad_AuthDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Port != "8001" {
		t.Errorf("Auth.Port = %q, want %q", cfg.Auth.Port, "8001")
	}
	if cfg.Auth.AccessTTL != 15*time.Minute {
		t.Errorf("Auth.AccessTTL = %v, want 15m", cfg.Auth.AccessTTL)
	}
	if cfg.Auth.RefreshTTL != 1440*time.Hour {
		t.Errorf("Auth.RefreshTTL = %v, want 1440h", cfg.Auth.RefreshTTL)
	}
}

// This test is a BEHAVIOR DOCUMENT, not a contract: it records that viper
// does NOT fall back to the default for an unparsable duration value and
// silently yields 0s. "15min" and "60d" are natural spellings of the
// "15 minutes"/"60 days" found throughout the docs that
// time.ParseDuration does NOT understand, so this is a realistic
// misconfiguration. Its consequences are destructive (AccessTTL=0 => every
// /api/* returns 401; RefreshTTL=0 => all users are logged out instantly),
// which is why identity.Start validates the TTLs EXPLICITLY and NEVER
// STARTS if one is <= 0 (see TestStart_InvalidTTLFails).
func TestLoad_UnparsableTTLSilentlyBecomesZero(t *testing.T) {
	os.Setenv("AUTH_ACCESS_TTL", "15min")
	os.Setenv("AUTH_REFRESH_TTL", "60d")
	defer func() {
		os.Unsetenv("AUTH_ACCESS_TTL")
		os.Unsetenv("AUTH_REFRESH_TTL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Auth.AccessTTL != 0 {
		t.Errorf("AccessTTL = %v; this test documents that viper returns 0, if the behavior changed, review the identity.Start validation", cfg.Auth.AccessTTL)
	}
	if cfg.Auth.RefreshTTL != 0 {
		t.Errorf("RefreshTTL = %v; same note", cfg.Auth.RefreshTTL)
	}
}

// Whether the rate-limit defaults are really loaded: if they stay 0,
// identity.NewRateLimiter returns an error and the OP never starts.
func TestLoad_RateLimitDefaults(t *testing.T) {
	for _, name := range []string{
		"AUTH_LOGIN_IP_PER_MIN", "AUTH_LOGIN_EMAIL_PER_HOUR",
		"AUTH_SIGNUP_IP_PER_HOUR", "AUTH_TRUSTED_PROXIES",
	} {
		os.Unsetenv(name)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Auth.LoginIPPerMin != 10 {
		t.Errorf("LoginIPPerMin = %d, want 10", cfg.Auth.LoginIPPerMin)
	}
	if cfg.Auth.LoginEmailPerHour != 60 {
		t.Errorf("LoginEmailPerHour = %d, want 60", cfg.Auth.LoginEmailPerHour)
	}
	if cfg.Auth.SignupIPPerHour != 5 {
		t.Errorf("SignupIPPerHour = %d, want 5", cfg.Auth.SignupIPPerHour)
	}
	// FAIL-SAFE: the default is 0, so X-Forwarded-For is NEVER read. A
	// default of 1 would trust a forged single-element XFF in a setup with
	// no proxy in front and the IP limit would be bypassed entirely (see
	// the trusted_proxies note in config.go). Behind a proxy the value is
	// given EXPLICITLY; the next test checks that the override works.
	if cfg.Auth.TrustedProxies != 0 {
		t.Errorf("TrustedProxies = %d, want 0 (the default must be fail-safe)", cfg.Auth.TrustedProxies)
	}
}

// Whether an environment variable can really override (not just the default).
func TestLoad_RateLimitEnvOverrides(t *testing.T) {
	os.Setenv("AUTH_LOGIN_IP_PER_MIN", "3")
	os.Setenv("AUTH_TRUSTED_PROXIES", "2")
	defer func() {
		os.Unsetenv("AUTH_LOGIN_IP_PER_MIN")
		os.Unsetenv("AUTH_TRUSTED_PROXIES")
	}()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Auth.LoginIPPerMin != 3 {
		t.Errorf("LoginIPPerMin = %d, want 3", cfg.Auth.LoginIPPerMin)
	}
	// A value DIFFERENT from the default: otherwise this branch would
	// measure the same result as the default and the "env overrides"
	// claim would be vacuous.
	if cfg.Auth.TrustedProxies != 2 {
		t.Errorf("TrustedProxies = %d, want 2", cfg.Auth.TrustedProxies)
	}
}

// clearFederationEnv makes sure NONE of the AUTH_FED_* environment
// variables is left set. Every federation test calls it both at the start
// and via defer: otherwise a value left behind by one test makes another
// test pass or fail for the wrong reason (not a test from another package
// running in parallel, but the next test in this package).
func clearFederationEnv() {
	for _, name := range []string{
		"AUTH_FED_GOOGLE_CLIENT_ID", "AUTH_FED_GOOGLE_CLIENT_SECRET",
		"AUTH_FED_APPLE_CLIENT_ID", "AUTH_FED_APPLE_TEAM_ID",
		"AUTH_FED_APPLE_KEY_ID", "AUTH_FED_APPLE_KEY_P8",
	} {
		os.Unsetenv(name)
	}
}

// findFederationClient returns the entry with the given Name in the list
// (if any). Tests use it to check "which providers EXIST" separately from
// "what is the list length"; a length-only check would have passed even if
// the function returned the wrong provider.
func findFederationClient(list []FederationClient, name string) (FederationClient, bool) {
	for _, f := range list {
		if f.Name == name {
			return f, true
		}
	}
	return FederationClient{}, false
}

// When both providers are fully configured, both must be in the list and
// their fields (including Apple's three) must be carried over INTACT: the
// token exchange (identity.Upstream.ExchangeCode) builds Apple's client_secret from exactly
// these three fields.
func TestLoad_FederationBothFullyConfigured(t *testing.T) {
	clearFederationEnv()
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_ID", "google-cid")
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_SECRET", "google-sir")
	os.Setenv("AUTH_FED_APPLE_CLIENT_ID", "apple-cid")
	os.Setenv("AUTH_FED_APPLE_TEAM_ID", "team-1")
	os.Setenv("AUTH_FED_APPLE_KEY_ID", "key-1")
	os.Setenv("AUTH_FED_APPLE_KEY_P8", "-----BEGIN PRIVATE KEY-----p8-verisi")
	defer clearFederationEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Auth.Federation) != 2 {
		t.Fatalf("Federation length = %d, want 2: %+v", len(cfg.Auth.Federation), cfg.Auth.Federation)
	}

	google, ok := findFederationClient(cfg.Auth.Federation, "google")
	if !ok {
		t.Fatalf("google provider missing from the list: %+v", cfg.Auth.Federation)
	}
	if google.ClientID != "google-cid" || google.ClientSecret != "google-sir" {
		t.Errorf("google = %+v, want ClientID=google-cid ClientSecret=google-sir", google)
	}

	apple, ok := findFederationClient(cfg.Auth.Federation, "apple")
	if !ok {
		t.Fatalf("apple provider missing from the list: %+v", cfg.Auth.Federation)
	}
	if apple.ClientID != "apple-cid" {
		t.Errorf("apple.ClientID = %q, want apple-cid", apple.ClientID)
	}
	if apple.AppleTeamID != "team-1" || apple.AppleKeyID != "key-1" || apple.AppleKeyP8 != "-----BEGIN PRIVATE KEY-----p8-verisi" {
		t.Errorf("apple's three fields were carried over corrupted: %+v", apple)
	}
}

// If Google's client_id is empty, Google must NEVER be added to the list
// (absence, not an empty entry). Apple must stay on its own.
func TestLoad_FederationOmitsGoogleWhenEmpty(t *testing.T) {
	clearFederationEnv()
	os.Setenv("AUTH_FED_APPLE_CLIENT_ID", "apple-cid")
	os.Setenv("AUTH_FED_APPLE_TEAM_ID", "team-1")
	os.Setenv("AUTH_FED_APPLE_KEY_ID", "key-1")
	os.Setenv("AUTH_FED_APPLE_KEY_P8", "p8-verisi")
	defer clearFederationEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if _, ok := findFederationClient(cfg.Auth.Federation, "google"); ok {
		t.Errorf("google must not be in the list when client_id is empty: %+v", cfg.Auth.Federation)
	}
	apple, ok := findFederationClient(cfg.Auth.Federation, "apple")
	if !ok {
		t.Fatalf("apple missing from the list: %+v", cfg.Auth.Federation)
	}
	if apple.ClientID != "apple-cid" {
		t.Errorf("apple.ClientID = %q, want apple-cid", apple.ClientID)
	}
	if len(cfg.Auth.Federation) != 1 {
		t.Errorf("Federation length = %d, want 1 (apple only)", len(cfg.Auth.Federation))
	}
}

// If Apple's client_id is empty, Apple must NEVER be added to the list,
// even if its three fields (team/key/p8) are filled. Google must stay on
// its own.
func TestLoad_FederationOmitsAppleWhenEmpty(t *testing.T) {
	clearFederationEnv()
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_ID", "google-cid")
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_SECRET", "google-sir")
	// Apple's client_id is DELIBERATELY empty; even with its three fields
	// filled, Apple must not be added (the others are meaningless without
	// a client_id).
	os.Setenv("AUTH_FED_APPLE_TEAM_ID", "team-1")
	os.Setenv("AUTH_FED_APPLE_KEY_ID", "key-1")
	os.Setenv("AUTH_FED_APPLE_KEY_P8", "p8-verisi")
	defer clearFederationEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if _, ok := findFederationClient(cfg.Auth.Federation, "apple"); ok {
		t.Errorf("apple must not be in the list when client_id is empty: %+v", cfg.Auth.Federation)
	}
	google, ok := findFederationClient(cfg.Auth.Federation, "google")
	if !ok {
		t.Fatalf("google missing from the list: %+v", cfg.Auth.Federation)
	}
	if google.ClientID != "google-cid" {
		t.Errorf("google.ClientID = %q, want google-cid", google.ClientID)
	}
	if len(cfg.Auth.Federation) != 1 {
		t.Errorf("Federation length = %d, want 1 (google only)", len(cfg.Auth.Federation))
	}
}

// If both are empty the list must be empty/nil and nothing may panic.
func TestLoad_FederationEmptyWhenNoneConfigured(t *testing.T) {
	clearFederationEnv()
	defer clearFederationEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Auth.Federation) != 0 {
		t.Errorf("Federation length = %d, want 0: %+v", len(cfg.Auth.Federation), cfg.Auth.Federation)
	}
	// Access must not panic (a nil slice is not indexed, but range and len
	// must work safely).
	for _, f := range cfg.Auth.Federation {
		t.Errorf("unexpected entry: %+v", f)
	}
}

func TestLoad_SunoDefaults(t *testing.T) {
	os.Unsetenv("SUNO_MODEL")
	os.Unsetenv("SUNO_CALLBACK_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Suno.Model != "V6" {
		t.Errorf("Suno.Model = %q, want V6", cfg.Suno.Model)
	}
	if cfg.Suno.CallbackURL == "" {
		t.Error("Suno.CallbackURL is empty; the provider requires it")
	}
}

func TestLoad_SunoModelFromEnv(t *testing.T) {
	os.Setenv("SUNO_MODEL", "V6_MINI")
	defer os.Unsetenv("SUNO_MODEL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Suno.Model != "V6_MINI" {
		t.Errorf("Suno.Model = %q, want V6_MINI", cfg.Suno.Model)
	}
}

func TestLoad_SocialPublicationLimit(t *testing.T) {
	os.Unsetenv("RATELIMIT_SOSYAL_YAYIN_PER_HOUR")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.RateLimit.SocialPublicationPerHour != 30 {
		t.Errorf("default SocialPublicationPerHour = %d, want 30", cfg.RateLimit.SocialPublicationPerHour)
	}

	t.Setenv("RATELIMIT_SOSYAL_YAYIN_PER_HOUR", "7")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.RateLimit.SocialPublicationPerHour != 7 {
		t.Errorf("SocialPublicationPerHour = %d, want 7 from the environment", cfg.RateLimit.SocialPublicationPerHour)
	}
}

func TestLoad_SocialLimitsDefault(t *testing.T) {
	for _, name := range []string{
		"RATELIMIT_SOSYAL_YORUM_PER_MIN", "RATELIMIT_SOSYAL_COVER_PER_HOUR",
		"RATELIMIT_PUBLIC_PER_MIN", "RATELIMIT_PUBLIC_WEB_PER_MIN",
		"SOSYAL_WEB_ANAHTARI", "SOSYAL_WEB_ANAHTARI_FILE",
	} {
		os.Unsetenv(name)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	r := cfg.RateLimit
	if r.SocialCommentPerMin != 10 || r.SocialCoverPerHour != 20 || r.PublicPerMin != 30 || r.PublicWebPerMin != 600 {
		t.Errorf("defaults = comment %d, cover %d, public %d, web %d; want 10, 20, 30, 600",
			r.SocialCommentPerMin, r.SocialCoverPerHour, r.PublicPerMin, r.PublicWebPerMin)
	}
	if cfg.Social.WebKey != "" {
		t.Errorf("WebKey default must be empty: %q", cfg.Social.WebKey)
	}
}

func TestLoad_SocialLimitsFromEnv(t *testing.T) {
	t.Setenv("RATELIMIT_SOSYAL_YORUM_PER_MIN", "3")
	t.Setenv("RATELIMIT_SOSYAL_COVER_PER_HOUR", "4")
	t.Setenv("RATELIMIT_PUBLIC_PER_MIN", "5")
	t.Setenv("RATELIMIT_PUBLIC_WEB_PER_MIN", "6")
	t.Setenv("SOSYAL_WEB_ANAHTARI", "dogrudan-deger")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	r := cfg.RateLimit
	if r.SocialCommentPerMin != 3 || r.SocialCoverPerHour != 4 || r.PublicPerMin != 5 || r.PublicWebPerMin != 6 {
		t.Errorf("environment values were not read: %+v", r)
	}
	if cfg.Social.WebKey != "dogrudan-deger" {
		t.Errorf("WebKey = %q", cfg.Social.WebKey)
	}
}

// Docker secret path: SOSYAL_WEB_ANAHTARI is empty, _FILE is set.
func TestLoad_WebKeyFromFile(t *testing.T) {
	secretFile := t.TempDir() + "/web_anahtari"
	if err := os.WriteFile(secretFile, []byte("  dosyadaki-sir\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOSYAL_WEB_ANAHTARI", "")
	t.Setenv("SOSYAL_WEB_ANAHTARI_FILE", secretFile)
	// readSecret writes the value to the environment; t.Setenv's cleanup undoes it.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Social.WebKey != "dosyadaki-sir" {
		t.Errorf("WebKey = %q, want dosyadaki-sir (trimmed)", cfg.Social.WebKey)
	}
}

func TestLoad_CloudflareRanges(t *testing.T) {
	os.Unsetenv("RATELIMIT_CLOUDFLARE_RANGES")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.RateLimit.Cloudflare) != len(clientip.DefaultCloudflareRanges) || len(cfg.Auth.Cloudflare) != len(cfg.RateLimit.Cloudflare) {
		t.Errorf("default ranges: rate limit %d, auth %d; want %d",
			len(cfg.RateLimit.Cloudflare), len(cfg.Auth.Cloudflare), len(clientip.DefaultCloudflareRanges))
	}

	t.Setenv("RATELIMIT_CLOUDFLARE_RANGES", "10.0.0.0/8, 2001:db8::/32")
	cfg, err = Load()
	if err != nil || len(cfg.RateLimit.Cloudflare) != 2 {
		t.Fatalf("custom ranges: %v, %v; want 2 entries", cfg, err)
	}

	t.Setenv("RATELIMIT_CLOUDFLARE_RANGES", "")
	cfg, err = Load()
	if err != nil || len(cfg.RateLimit.Cloudflare) != 0 {
		t.Fatalf("empty must disable: %v, %v", cfg, err)
	}

	t.Setenv("RATELIMIT_CLOUDFLARE_RANGES", "10.0.0.0/8,nonsense")
	if _, err = Load(); err == nil {
		t.Fatal("an invalid entry must be a startup error")
	}
}
