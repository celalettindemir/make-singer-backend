package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/clientip"
	"github.com/spf13/viper"
)

// readSecret reads a Docker secret from a file path specified by an env var
// with _FILE suffix. If FOO is already set directly, the file is skipped.
// If FOO_FILE is set, reads the file content and sets FOO.
func readSecret(envKey string) {
	if os.Getenv(envKey) != "" {
		return
	}
	fileKey := envKey + "_FILE"
	filePath := os.Getenv(fileKey)
	if filePath == "" {
		return
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	val := strings.TrimSpace(string(data))
	os.Setenv(envKey, val)
}

type Config struct {
	Server    ServerConfig
	Redis     RedisConfig
	RateLimit RateLimitConfig
	Groq      GroqConfig
	R2        R2Config
	Suno      SunoConfig
	Audio     AudioConfig
	Auth      AuthConfig
	Social    SocialConfig
}

type ServerConfig struct {
	Port      string
	Env       string
	LogLevel  string
	APIDomain string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type RateLimitConfig struct {
	LyricsPerMin   int
	RenderPerHour  int
	MasterPerHour  int
	ExportPerHour  int
	UploadPerHour  int
	WSTicketPerMin int
	SignPerMin     int
	// SocialPublicationPerHour is the hourly per-user limit for
	// POST /api/social/yayinlar.
	SocialPublicationPerHour int
	// SocialCommentPerMin is the per-minute per-user limit for
	// POST /api/social/yayinlar/{id}/yorumlar (429 slow-down when exceeded).
	SocialCommentPerMin int
	// SocialCoverPerHour is the hourly per-user limit for
	// POST /api/social/yayinlar/{id}/cover-baslat (each call copies stems).
	SocialCoverPerHour int
	// PublicPerMin is the per-minute per-client-IP limit for the
	// unauthenticated GET /api/public/s/{slug}.
	PublicPerMin int
	// PublicWebPerMin is the shared per-minute limit across ALL requests
	// to the same endpoint that carry X-Web-Anahtari (the web server).
	PublicWebPerMin int
	// Cloudflare is the parsed ratelimit.cloudflare_ranges list: the edge
	// networks whose CF-Connecting-IP header is trusted for per-IP limits
	// (see internal/clientip). Empty disables the header.
	Cloudflare clientip.CIDRs
}

// SocialConfig is the configuration of the Discover (social) feature.
type SocialConfig struct {
	// WebKey is the shared secret make-singer-frontend sends in the
	// X-Web-Anahtari header. If empty, the web path is disabled and the
	// web server is subject to the per-IP limit as well. The secret comes
	// from SOSYAL_WEB_ANAHTARI or SOSYAL_WEB_ANAHTARI_FILE.
	WebKey string
}

type GroqConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	// MaxTokens also covers reasoning tokens (gpt-oss thinks first).
	MaxTokens           int
	TemperatureGenerate float64
	TemperatureRewrite  float64
}

// R2Config holds the public and private bucket configuration for
// Cloudflare R2. The public bucket is for export files, the private bucket
// for user working files.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	PublicBucket    string
	PrivateBucket   string
	PublicURL       string
	PresignTTL      time.Duration
}

// PresignTTLOrDefault returns the presigned URL lifetime. If the config is
// missing (nil) or PresignTTL is unset (<=0), it falls back to the
// 1-hour default. It exists so this is the ONLY place that reads the TTL;
// the constant "1 hour" must not be repeated elsewhere.
func (c *R2Config) PresignTTLOrDefault() time.Duration {
	if c == nil || c.PresignTTL <= 0 {
		return time.Hour
	}
	return c.PresignTTL
}

type SunoConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	// CallbackURL: the provider requires it but we poll, so no endpoint exists.
	CallbackURL string
}

type AudioConfig struct {
	ServiceURL string
	Timeout    int // seconds
}

// AuthConfig is the configuration of our own OpenID Provider.
// Issuer is required: if empty the service does not start (there is no
// other auth path for /api).
type AuthConfig struct {
	Issuer        string        // https://makesinger-auth.celalettindemir.dev
	Port          string        // port of the second listener
	DBURL         string        // Postgres DSN
	SigningKeyPEM string        // RSA private key, PEM
	CryptoKey     string        // 32-byte secret for op.Config.CryptoKey
	ClientID      string        // mobile public client
	RedirectURIs  []string      // allowed redirect URIs
	AccessTTL     time.Duration // access token lifetime
	RefreshTTL    time.Duration // refresh token lifetime

	// Rate limits for the login/signup endpoints of the OP listener (the
	// second net/http server). These endpoints are SEPARATE from the main
	// API's Fiber chain, so internal/middleware/ratelimit.go never sees
	// them; the limits are defined here and internal/identity/ratelimit.go
	// enforces them.
	LoginIPPerMin     int // POST /giris, per IP per minute
	LoginEmailPerHour int // POST /giris, per email per hour
	SignupIPPerHour   int // POST /kayit, per IP per hour

	// TrustedProxies is the number of TRUSTED reverse proxies a request
	// passes through before reaching the OP. The element that many
	// positions from the END of X-Forwarded-For is taken as the real
	// client address; if 0 the header is NEVER read and RemoteAddr is
	// used. See identity.clientIP.
	//
	// DEFAULT 0 (FAIL-SAFE). With NO proxy in front, a value of 1 would
	// trust a forged single-element XFF: an attacker could write a
	// different IP on every request and bypass the IP limit COMPLETELY.
	// Behind a reverse proxy such as Traefik, this value must be set
	// EXPLICITLY to the number of proxies (AUTH_TRUSTED_PROXIES).
	TrustedProxies int

	// Cloudflare is the same list as RateLimit.Cloudflare, applied to the
	// OP listener's per-IP limits.
	Cloudflare clientip.CIDRs

	// Federation lists the upstream providers. If empty, the federation
	// endpoints are NEVER mounted and no button appears on the login page.
	Federation []FederationClient
}

// FederationClient is the configuration of one upstream provider.
// Name must be "google" or "apple"; the URLs are fixed and come from
// code, only the credentials are read from the environment.
type FederationClient struct {
	Name     string // "google" | "apple"
	ClientID string
	// Plain client_secret for Google. Left EMPTY for Apple: Apple's
	// client_secret is generated with ES256 on every request (see K5).
	ClientSecret string
	// Required for Apple: the .p8 private key (PEM), Team ID and Key ID.
	AppleTeamID string
	AppleKeyID  string
	AppleKeyP8  string
}

func Load() (*Config, error) {
	// Read Docker Swarm secrets from _FILE env vars before Viper binds
	readSecret("REDIS_PASSWORD")
	readSecret("GROQ_API_KEY")
	readSecret("SUNO_API_KEY")
	readSecret("R2_ACCOUNT_ID")
	readSecret("R2_ACCESS_KEY_ID")
	readSecret("R2_SECRET_ACCESS_KEY")
	readSecret("AUTH_DB_URL")
	readSecret("AUTH_SIGNING_KEY")
	readSecret("AUTH_CRYPTO_KEY")
	readSecret("SOSYAL_WEB_ANAHTARI")

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// Environment variables
	viper.AutomaticEnv()

	// Bind environment variables with underscores to nested config keys
	_ = viper.BindEnv("server.port", "SERVER_PORT")
	_ = viper.BindEnv("server.env", "SERVER_ENV")
	_ = viper.BindEnv("server.log_level", "LOG_LEVEL")
	_ = viper.BindEnv("redis.addr", "REDIS_ADDR")
	_ = viper.BindEnv("redis.password", "REDIS_PASSWORD")
	_ = viper.BindEnv("redis.db", "REDIS_DB")
	_ = viper.BindEnv("groq.api_key", "GROQ_API_KEY")
	_ = viper.BindEnv("groq.base_url", "GROQ_BASE_URL")
	_ = viper.BindEnv("groq.model", "GROQ_MODEL")
	_ = viper.BindEnv("groq.max_tokens", "GROQ_MAX_TOKENS")
	_ = viper.BindEnv("groq.temperature_generate", "GROQ_TEMPERATURE_GENERATE")
	_ = viper.BindEnv("groq.temperature_rewrite", "GROQ_TEMPERATURE_REWRITE")
	_ = viper.BindEnv("r2.account_id", "R2_ACCOUNT_ID")
	_ = viper.BindEnv("r2.access_key_id", "R2_ACCESS_KEY_ID")
	_ = viper.BindEnv("r2.secret_access_key", "R2_SECRET_ACCESS_KEY")
	_ = viper.BindEnv("r2.public_bucket", "R2_PUBLIC_BUCKET")
	_ = viper.BindEnv("r2.private_bucket", "R2_PRIVATE_BUCKET")
	_ = viper.BindEnv("r2.public_url", "R2_PUBLIC_URL")
	_ = viper.BindEnv("r2.presign_ttl", "R2_PRESIGN_TTL")
	_ = viper.BindEnv("suno.api_key", "SUNO_API_KEY")
	_ = viper.BindEnv("suno.base_url", "SUNO_BASE_URL")
	_ = viper.BindEnv("suno.model", "SUNO_MODEL")
	_ = viper.BindEnv("suno.callback_url", "SUNO_CALLBACK_URL")
	_ = viper.BindEnv("audio.service_url", "AUDIO_SERVICE_URL")
	_ = viper.BindEnv("audio.timeout", "AUDIO_SERVICE_TIMEOUT")
	_ = viper.BindEnv("server.api_domain", "API_DOMAIN")
	_ = viper.BindEnv("auth.issuer", "AUTH_ISSUER")
	_ = viper.BindEnv("auth.port", "AUTH_PORT")
	_ = viper.BindEnv("auth.db_url", "AUTH_DB_URL")
	_ = viper.BindEnv("auth.signing_key_pem", "AUTH_SIGNING_KEY")
	_ = viper.BindEnv("auth.crypto_key", "AUTH_CRYPTO_KEY")
	_ = viper.BindEnv("auth.client_id", "AUTH_CLIENT_ID")
	_ = viper.BindEnv("auth.redirect_uris", "AUTH_REDIRECT_URIS")
	_ = viper.BindEnv("auth.access_ttl", "AUTH_ACCESS_TTL")
	_ = viper.BindEnv("auth.refresh_ttl", "AUTH_REFRESH_TTL")
	_ = viper.BindEnv("auth.login_ip_per_min", "AUTH_LOGIN_IP_PER_MIN")
	_ = viper.BindEnv("auth.login_email_per_hour", "AUTH_LOGIN_EMAIL_PER_HOUR")
	_ = viper.BindEnv("auth.signup_ip_per_hour", "AUTH_SIGNUP_IP_PER_HOUR")
	_ = viper.BindEnv("auth.trusted_proxies", "AUTH_TRUSTED_PROXIES")
	_ = viper.BindEnv("auth.fed_google_client_id", "AUTH_FED_GOOGLE_CLIENT_ID")
	_ = viper.BindEnv("auth.fed_google_client_secret", "AUTH_FED_GOOGLE_CLIENT_SECRET")
	_ = viper.BindEnv("auth.fed_apple_client_id", "AUTH_FED_APPLE_CLIENT_ID")
	_ = viper.BindEnv("auth.fed_apple_team_id", "AUTH_FED_APPLE_TEAM_ID")
	_ = viper.BindEnv("auth.fed_apple_key_id", "AUTH_FED_APPLE_KEY_ID")
	_ = viper.BindEnv("auth.fed_apple_key_p8", "AUTH_FED_APPLE_KEY_P8")
	_ = viper.BindEnv("ratelimit.sosyal_yayin_per_hour", "RATELIMIT_SOSYAL_YAYIN_PER_HOUR")
	_ = viper.BindEnv("ratelimit.sosyal_yorum_per_min", "RATELIMIT_SOSYAL_YORUM_PER_MIN")
	_ = viper.BindEnv("ratelimit.sosyal_cover_per_hour", "RATELIMIT_SOSYAL_COVER_PER_HOUR")
	_ = viper.BindEnv("ratelimit.public_per_min", "RATELIMIT_PUBLIC_PER_MIN")
	_ = viper.BindEnv("ratelimit.public_web_per_min", "RATELIMIT_PUBLIC_WEB_PER_MIN")
	_ = viper.BindEnv("ratelimit.cloudflare_ranges", "RATELIMIT_CLOUDFLARE_RANGES")
	_ = viper.BindEnv("sosyal.web_anahtari", "SOSYAL_WEB_ANAHTARI")

	// Defaults
	viper.SetDefault("server.port", "8000")
	viper.SetDefault("server.env", "development")
	viper.SetDefault("server.log_level", "info")
	viper.SetDefault("redis.addr", "localhost:6379")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("ratelimit.lyrics_per_min", 30)
	viper.SetDefault("ratelimit.render_per_hour", 5)
	viper.SetDefault("ratelimit.master_per_hour", 10)
	viper.SetDefault("ratelimit.export_per_hour", 20)
	viper.SetDefault("ratelimit.upload_per_hour", 50)
	viper.SetDefault("ratelimit.ws_ticket_per_min", 60)
	viper.SetDefault("ratelimit.sign_per_min", 60)
	viper.SetDefault("ratelimit.sosyal_yayin_per_hour", 30)
	viper.SetDefault("ratelimit.sosyal_yorum_per_min", 10)
	viper.SetDefault("ratelimit.sosyal_cover_per_hour", 20)
	viper.SetDefault("ratelimit.public_per_min", 30)
	viper.SetDefault("ratelimit.public_web_per_min", 600)
	viper.SetDefault("ratelimit.cloudflare_ranges", strings.Join(clientip.DefaultCloudflareRanges, ","))

	// Groq defaults
	viper.SetDefault("groq.base_url", "https://api.groq.com/openai/v1")
	viper.SetDefault("groq.model", "openai/gpt-oss-120b")
	viper.SetDefault("groq.max_tokens", 4000)
	viper.SetDefault("groq.temperature_generate", 0.8)
	viper.SetDefault("groq.temperature_rewrite", 0.6)

	// Suno defaults
	viper.SetDefault("suno.base_url", "https://api.sunoapi.org")
	viper.SetDefault("suno.model", "V6")
	viper.SetDefault("suno.callback_url", "https://makesinger-api.celalettindemir.dev/suno/callback")

	// Audio service defaults
	viper.SetDefault("audio.service_url", "http://localhost:8084")
	viper.SetDefault("audio.timeout", 120)

	// R2 presigned URL default lifetime
	viper.SetDefault("r2.presign_ttl", "1h")

	// Auth (our own OpenID Provider) defaults
	viper.SetDefault("auth.port", "8001")
	viper.SetDefault("auth.access_ttl", "15m")
	viper.SetDefault("auth.refresh_ttl", "1440h") // 60 days

	// WARNING: viper.SetDefault does NOT kick in when a value EXISTS but
	// CANNOT BE PARSED. For example AUTH_ACCESS_TTL="15min" or
	// AUTH_REFRESH_TTL="60d" (natural spellings of the docs' "15 minutes"/
	// "60 days" that time.ParseDuration does NOT understand) silently
	// become 0s and do NOT fall back to the default. A 0 TTL is
	// destructive: the access record is never written and the token is
	// issued with exp=now (every /api/* returns 401), and the refresh
	// record expires the moment it is born (all users are logged out
	// instantly). That is why identity.Start validates the TTLs EXPLICITLY
	// and NEVER STARTS if one is <= 0.

	// Rate-limit defaults (OP login/signup endpoints).
	//
	// RATIONALE (not made up, measured/quoted):
	//   - login_ip_per_min = 10: the cost measured during review is
	//     ~258 ms CPU per request (bcrypt cost 12, see identity.bcryptCost).
	//     10/min ties one IP to ~4.3% of a CPU core, which closes the CPU
	//     DoS path. A human logging in by hand does not approach 10 POSTs
	//     a minute. Also consistent with the repo's own scale:
	//     ratelimit.lyrics_per_min = 30 (login is a rarer operation).
	//   - login_email_per_hour = 60: distributed brute force (a different
	//     IP per request) bypasses the IP counter; the per-account counter
	//     bounds it. NIST SP 800-63B 5.2.2 sets an upper bound of 100 on
	//     consecutive failed attempts - 60/hour is clearly below that, yet
	//     higher than a real user could reach in an hour.
	//   - signup_ip_per_hour = 5: email verification and CAPTCHA are
	//     deliberately absent in the MVP, so this is the ONLY thing
	//     limiting account creation. It is enough for a few people behind
	//     a shared egress NAT and ends bulk account creation.
	//   - trusted_proxies = 0: the SAFE-BY-DEFAULT side. A default of 1
	//     would, in a setup with NO proxy in front, trust a forged
	//     single-element X-Forwarded-For and an attacker could write a
	//     different IP on every request and bypass the IP limit COMPLETELY.
	//     With 0 the header is NEVER read and RemoteAddr is used. Behind a
	//     reverse proxy such as Traefik this value must be set EXPLICITLY
	//     to the number of proxies (AUTH_TRUSTED_PROXIES, 1 in the
	//     cluster manifest).
	viper.SetDefault("auth.login_ip_per_min", 10)
	viper.SetDefault("auth.login_email_per_hour", 60)
	viper.SetDefault("auth.signup_ip_per_hour", 5)
	viper.SetDefault("auth.trusted_proxies", 0)

	// Try to read config file (optional)
	_ = viper.ReadInConfig()

	cfg := &Config{
		Server: ServerConfig{
			Port:      viper.GetString("server.port"),
			Env:       viper.GetString("server.env"),
			LogLevel:  viper.GetString("server.log_level"),
			APIDomain: viper.GetString("server.api_domain"),
		},
		Redis: RedisConfig{
			Addr:     viper.GetString("redis.addr"),
			Password: viper.GetString("redis.password"),
			DB:       viper.GetInt("redis.db"),
		},
		RateLimit: RateLimitConfig{
			LyricsPerMin:   viper.GetInt("ratelimit.lyrics_per_min"),
			RenderPerHour:  viper.GetInt("ratelimit.render_per_hour"),
			MasterPerHour:  viper.GetInt("ratelimit.master_per_hour"),
			ExportPerHour:  viper.GetInt("ratelimit.export_per_hour"),
			UploadPerHour:  viper.GetInt("ratelimit.upload_per_hour"),
			WSTicketPerMin: viper.GetInt("ratelimit.ws_ticket_per_min"),
			SignPerMin:     viper.GetInt("ratelimit.sign_per_min"),

			SocialPublicationPerHour: viper.GetInt("ratelimit.sosyal_yayin_per_hour"),
			SocialCommentPerMin:      viper.GetInt("ratelimit.sosyal_yorum_per_min"),
			SocialCoverPerHour:       viper.GetInt("ratelimit.sosyal_cover_per_hour"),
			PublicPerMin:             viper.GetInt("ratelimit.public_per_min"),
			PublicWebPerMin:          viper.GetInt("ratelimit.public_web_per_min"),
		},
		Groq: GroqConfig{
			APIKey:  viper.GetString("groq.api_key"),
			BaseURL: viper.GetString("groq.base_url"),
			Model:   viper.GetString("groq.model"),

			MaxTokens:           viper.GetInt("groq.max_tokens"),
			TemperatureGenerate: viper.GetFloat64("groq.temperature_generate"),
			TemperatureRewrite:  viper.GetFloat64("groq.temperature_rewrite"),
		},
		R2: R2Config{
			AccountID:       viper.GetString("r2.account_id"),
			AccessKeyID:     viper.GetString("r2.access_key_id"),
			SecretAccessKey: viper.GetString("r2.secret_access_key"),
			PublicBucket:    viper.GetString("r2.public_bucket"),
			PrivateBucket:   viper.GetString("r2.private_bucket"),
			PublicURL:       viper.GetString("r2.public_url"),
			PresignTTL:      viper.GetDuration("r2.presign_ttl"),
		},
		Suno: SunoConfig{
			APIKey:      viper.GetString("suno.api_key"),
			BaseURL:     viper.GetString("suno.base_url"),
			Model:       viper.GetString("suno.model"),
			CallbackURL: viper.GetString("suno.callback_url"),
		},
		Audio: AudioConfig{
			ServiceURL: viper.GetString("audio.service_url"),
			Timeout:    viper.GetInt("audio.timeout"),
		},
		Auth: AuthConfig{
			Issuer:        viper.GetString("auth.issuer"),
			Port:          viper.GetString("auth.port"),
			DBURL:         viper.GetString("auth.db_url"),
			SigningKeyPEM: viper.GetString("auth.signing_key_pem"),
			CryptoKey:     viper.GetString("auth.crypto_key"),
			ClientID:      viper.GetString("auth.client_id"),
			RedirectURIs:  viper.GetStringSlice("auth.redirect_uris"),
			AccessTTL:     viper.GetDuration("auth.access_ttl"),
			RefreshTTL:    viper.GetDuration("auth.refresh_ttl"),

			LoginIPPerMin:     viper.GetInt("auth.login_ip_per_min"),
			LoginEmailPerHour: viper.GetInt("auth.login_email_per_hour"),
			SignupIPPerHour:   viper.GetInt("auth.signup_ip_per_hour"),
			TrustedProxies:    viper.GetInt("auth.trusted_proxies"),

			Federation: buildFederationClients(),
		},
		Social: SocialConfig{
			WebKey: viper.GetString("sosyal.web_anahtari"),
		},
	}

	// An env var set to the empty string must DISABLE the Cloudflare
	// header, but viper treats an empty env var as unset (and would fall
	// back to the default list), so it is read directly.
	rawRanges := viper.GetString("ratelimit.cloudflare_ranges")
	if v, ok := os.LookupEnv("RATELIMIT_CLOUDFLARE_RANGES"); ok {
		rawRanges = v
	}
	var ranges []string
	for _, r := range strings.Split(rawRanges, ",") {
		if r = strings.TrimSpace(r); r != "" {
			ranges = append(ranges, r)
		}
	}
	cloudflare, err := clientip.ParseCIDRs(ranges)
	if err != nil {
		return nil, fmt.Errorf("ratelimit.cloudflare_ranges: %w", err)
	}
	cfg.RateLimit.Cloudflare = cloudflare
	cfg.Auth.Cloudflare = cloudflare

	return cfg, nil
}

// buildFederationClients builds the federation provider list from the
// AUTH_FED_* values read from viper.
//
// SILENT-OMISSION RULE: if a provider's client_id is empty, that provider
// is NEVER added to the list. Starting with a half-configured provider
// would mean the user presses the button and sees an error; when it is not
// added to the list, no button appears on the login page either (see the
// FederationClient comment). That all three of Apple's fields
// (TeamID/KeyID/KeyP8) are filled is NOT checked here: that check is done
// in validateConfig inside identity.Start, and if anything is missing
// Start RETURNS AN ERROR (a startup error, not a silent skip).
func buildFederationClients() []FederationClient {
	var list []FederationClient

	if id := viper.GetString("auth.fed_google_client_id"); id != "" {
		list = append(list, FederationClient{
			Name:         "google",
			ClientID:     id,
			ClientSecret: viper.GetString("auth.fed_google_client_secret"),
		})
	}

	if id := viper.GetString("auth.fed_apple_client_id"); id != "" {
		list = append(list, FederationClient{
			Name:        "apple",
			ClientID:    id,
			AppleTeamID: viper.GetString("auth.fed_apple_team_id"),
			AppleKeyID:  viper.GetString("auth.fed_apple_key_id"),
			AppleKeyP8:  viper.GetString("auth.fed_apple_key_p8"),
		})
	}

	return list
}
