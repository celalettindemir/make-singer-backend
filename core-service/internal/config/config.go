package config

import (
	"os"
	"strings"
	"time"

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
	JWT       JWTConfig
	RateLimit RateLimitConfig
	Groq      GroqConfig
	R2        R2Config
	Zitadel   ZitadelConfig
	Suno      SunoConfig
	Audio     AudioConfig
	Gateway   GatewayConfig
	Auth      AuthConfig
}

type ServerConfig struct {
	Port      string
	Env       string
	LogLevel  string
	ApiDomain string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type JWTConfig struct {
	Secret     string
	Expiration int // hours
}

type RateLimitConfig struct {
	LyricsPerMin  int
	RenderPerHour int
	MasterPerHour int
	ExportPerHour int
	UploadPerHour int
}

type GroqConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// R2Config, Cloudflare R2 icin genel (public) ve ozel (private) bucket
// yapilandirmasini tutar. Public bucket export dosyalari, private bucket
// kullanici calisma dosyalari icindir.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	PublicBucket    string
	PrivateBucket   string
	PublicURL       string
	PresignTTL      time.Duration
}

// PresignTTLOrDefault, presigned URL suresini dondurur. Config eksikse
// (nil) veya PresignTTL ayarlanmamissa (<=0), 1 saatlik varsayilana
// duser. Bu, TTL degerini okuyan TEK yer olsun diye vardir; sabit
// "1 saat" degeri baska yerlerde tekrar yazilmamali.
func (c *R2Config) PresignTTLOrDefault() time.Duration {
	if c == nil || c.PresignTTL <= 0 {
		return time.Hour
	}
	return c.PresignTTL
}

type ZitadelConfig struct {
	Domain   string
	ClientID string
	Issuer   string
}

type SunoConfig struct {
	APIKey  string
	BaseURL string
}

type AudioConfig struct {
	ServiceURL string
	Timeout    int // seconds
}

type GatewayConfig struct {
	Enabled bool
}

// AuthConfig, kendi OpenID Provider'imizin yapilandirmasi.
// Issuer bos ise OP hic baslamaz; API o zaman eski auth yolunda kalir.
type AuthConfig struct {
	Issuer        string        // https://makesinger-auth.celalettindemir.dev
	Port          string        // ikinci dinleyicinin portu
	DBURL         string        // Postgres DSN
	SigningKeyPEM string        // RSA ozel anahtar, PEM
	CryptoKey     string        // op.Config.CryptoKey icin 32 baytlik sir
	ClientID      string        // mobil public client
	RedirectURIs  []string      // izinli redirect adresleri
	AccessTTL     time.Duration // access token omru
	RefreshTTL    time.Duration // refresh token omru

	// OP dinleyicisinin (ikinci net/http sunucusu) giris/kayit uclari
	// icin hiz limiti. Bu uclar ana API'nin Fiber zincirinden AYRIDIR,
	// yani internal/middleware/ratelimit.go onlari HIC gormez; limitler
	// burada tanimlanir ve internal/kimlik/hizlimit.go uygular.
	LoginIPPerMin     int // POST /giris, IP basina dakikada
	LoginEmailPerHour int // POST /giris, e-posta basina saatte
	SignupIPPerHour   int // POST /kayit, IP basina saatte

	// TrustedProxies, istek OP'ye ulasmadan once gecen GUVENILEN ters
	// proxy sayisidir. X-Forwarded-For'un SONDAN bu kadarinci elemani
	// gercek istemci adresi sayilir; 0 ise baslik HIC okunmaz ve
	// RemoteAddr kullanilir. Bkz. kimlik.istemciIP.
	//
	// VARSAYILAN 0 (FAIL-SAFE). Onde proxy YOKSA 1 degeri tek elemanli
	// uydurma bir XFF'i guvenilir sayar: saldirgan her istekte farkli
	// bir IP yazip IP limitini TAMAMEN atlar. Traefik gibi bir ters
	// proxy arkasindaysa bu deger ACIKCA proxy sayisina ayarlanmali
	// (AUTH_TRUSTED_PROXIES).
	TrustedProxies int
}

func Load() (*Config, error) {
	// Read Docker Swarm secrets from _FILE env vars before Viper binds
	readSecret("REDIS_PASSWORD")
	readSecret("GROQ_API_KEY")
	readSecret("SUNO_API_KEY")
	readSecret("R2_ACCOUNT_ID")
	readSecret("R2_ACCESS_KEY_ID")
	readSecret("R2_SECRET_ACCESS_KEY")
	readSecret("ZITADEL_CLIENT_ID")
	readSecret("AUTH_DB_URL")
	readSecret("AUTH_SIGNING_KEY")
	readSecret("AUTH_CRYPTO_KEY")

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
	_ = viper.BindEnv("jwt.secret", "JWT_SECRET")
	_ = viper.BindEnv("jwt.expiration", "JWT_EXPIRATION")
	_ = viper.BindEnv("groq.api_key", "GROQ_API_KEY")
	_ = viper.BindEnv("groq.base_url", "GROQ_BASE_URL")
	_ = viper.BindEnv("groq.model", "GROQ_MODEL")
	_ = viper.BindEnv("r2.account_id", "R2_ACCOUNT_ID")
	_ = viper.BindEnv("r2.access_key_id", "R2_ACCESS_KEY_ID")
	_ = viper.BindEnv("r2.secret_access_key", "R2_SECRET_ACCESS_KEY")
	_ = viper.BindEnv("r2.public_bucket", "R2_PUBLIC_BUCKET")
	_ = viper.BindEnv("r2.private_bucket", "R2_PRIVATE_BUCKET")
	_ = viper.BindEnv("r2.public_url", "R2_PUBLIC_URL")
	_ = viper.BindEnv("r2.presign_ttl", "R2_PRESIGN_TTL")
	_ = viper.BindEnv("zitadel.domain", "ZITADEL_DOMAIN")
	_ = viper.BindEnv("zitadel.client_id", "ZITADEL_CLIENT_ID")
	_ = viper.BindEnv("zitadel.issuer", "ZITADEL_ISSUER")
	_ = viper.BindEnv("suno.api_key", "SUNO_API_KEY")
	_ = viper.BindEnv("suno.base_url", "SUNO_BASE_URL")
	_ = viper.BindEnv("audio.service_url", "AUDIO_SERVICE_URL")
	_ = viper.BindEnv("audio.timeout", "AUDIO_SERVICE_TIMEOUT")
	_ = viper.BindEnv("server.api_domain", "API_DOMAIN")
	_ = viper.BindEnv("gateway.enabled", "GATEWAY_ENABLED")
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

	// Defaults
	viper.SetDefault("server.port", "8000")
	viper.SetDefault("server.env", "development")
	viper.SetDefault("server.log_level", "info")
	viper.SetDefault("redis.addr", "localhost:6379")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("jwt.secret", "change-me-in-production")
	viper.SetDefault("jwt.expiration", 24)
	viper.SetDefault("ratelimit.lyrics_per_min", 30)
	viper.SetDefault("ratelimit.render_per_hour", 5)
	viper.SetDefault("ratelimit.master_per_hour", 10)
	viper.SetDefault("ratelimit.export_per_hour", 20)
	viper.SetDefault("ratelimit.upload_per_hour", 50)

	// Groq defaults
	viper.SetDefault("groq.base_url", "https://api.groq.com/openai/v1")
	viper.SetDefault("groq.model", "llama-3.3-70b-versatile")

	// Suno defaults
	viper.SetDefault("suno.base_url", "https://api.sunoapi.org")

	// Audio service defaults
	viper.SetDefault("audio.service_url", "http://localhost:8084")
	viper.SetDefault("audio.timeout", 120)

	// Gateway defaults
	viper.SetDefault("gateway.enabled", false)

	// R2 presigned URL varsayilan omru
	viper.SetDefault("r2.presign_ttl", "1h")

	// Auth (kendi OpenID Provider) varsayilanlari
	viper.SetDefault("auth.port", "8001")
	viper.SetDefault("auth.access_ttl", "15m")
	viper.SetDefault("auth.refresh_ttl", "1440h") // 60 gun

	// DIKKAT: viper.SetDefault, deger VAR ama COZULEMIYORSA devreye
	// GIRMEZ. Ornegin AUTH_ACCESS_TTL="15min" veya AUTH_REFRESH_TTL="60d"
	// (dokumanlardaki "15 dakika"/"60 gun" ifadelerinin dogal ama
	// time.ParseDuration'in ANLAMADIGI yazimlari) sessizce 0s olur,
	// varsayilana DUSMEZ. 0 TTL yikicidir: access kaydi hic yazilmaz ve
	// jeton exp=now ile uretilir (her /api/* 401), refresh kaydi
	// dogdugu an olur (tum kullanicilar aninda disari atilir). Bu yuzden
	// kimlik.Start TTL'leri ACIKCA dogrular ve <= 0 ise HIC BASLAMAZ.

	// Hiz limiti varsayilanlari (OP giris/kayit uclari).
	//
	// GEREKCELER (uydurma degil, olculen/alintilanan):
	//   - login_ip_per_min = 10: inceleme sirasinda olculen maliyet istek
	//     basina ~258 ms CPU (bcrypt cost 12, bkz. kimlik.bcryptCost).
	//     10/dk bir IP'yi ~%4,3 CPU cekirdegine baglar, yani CPU DoS
	//     yolu kapanir. Elle giris yapan bir insan dakikada 10 POST'a
	//     yaklasmaz. Deponun kendi kalibiyla da tutarli:
	//     ratelimit.lyrics_per_min = 30 (giris ondan seyrek bir islem).
	//   - login_email_per_hour = 60: dagitik brute-force (her istek ayri
	//     IP) IP sayacini atlar; hesap basina sayac onu sinirlar. NIST SP
	//     800-63B 5.2.2 ardisik basarisiz deneme sayisina en fazla 100
	//     ust siniri verir — 60/saat bunun belirgin altinda, ama gercek
	//     bir kullanicinin bir saatte ulasamayacagi kadar yukarida.
	//   - signup_ip_per_hour = 5: e-posta dogrulama ve CAPTCHA MVP'de
	//     bilerek yok, yani hesap uretimini sinirlayan TEK sey bu.
	//     Paylasimli bir cikis NAT'i arkasindaki birkac kisiye yeter,
	//     toplu hesap uretimini bitirir.
	//   - trusted_proxies = 0: VARSAYILAN GUVENLI OLAN taraftir. 1
	//     varsayilani, onunde proxy OLMAYAN bir kurulumda tek elemanli
	//     uydurma bir X-Forwarded-For'u guvenilir sayardi ve saldirgan
	//     her istekte farkli bir IP yazip IP limitini TAMAMEN atlardi.
	//     0 iken baslik HIC okunmaz, RemoteAddr kullanilir. Traefik gibi
	//     bir ters proxy arkasinda bu deger ACIKCA proxy sayisina
	//     ayarlanmalidir (AUTH_TRUSTED_PROXIES, kume manifestinde 1).
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
			ApiDomain: viper.GetString("server.api_domain"),
		},
		Redis: RedisConfig{
			Addr:     viper.GetString("redis.addr"),
			Password: viper.GetString("redis.password"),
			DB:       viper.GetInt("redis.db"),
		},
		JWT: JWTConfig{
			Secret:     viper.GetString("jwt.secret"),
			Expiration: viper.GetInt("jwt.expiration"),
		},
		RateLimit: RateLimitConfig{
			LyricsPerMin:  viper.GetInt("ratelimit.lyrics_per_min"),
			RenderPerHour: viper.GetInt("ratelimit.render_per_hour"),
			MasterPerHour: viper.GetInt("ratelimit.master_per_hour"),
			ExportPerHour: viper.GetInt("ratelimit.export_per_hour"),
			UploadPerHour: viper.GetInt("ratelimit.upload_per_hour"),
		},
		Groq: GroqConfig{
			APIKey:  viper.GetString("groq.api_key"),
			BaseURL: viper.GetString("groq.base_url"),
			Model:   viper.GetString("groq.model"),
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
		Zitadel: ZitadelConfig{
			Domain:   viper.GetString("zitadel.domain"),
			ClientID: viper.GetString("zitadel.client_id"),
			Issuer:   viper.GetString("zitadel.issuer"),
		},
		Suno: SunoConfig{
			APIKey:  viper.GetString("suno.api_key"),
			BaseURL: viper.GetString("suno.base_url"),
		},
		Audio: AudioConfig{
			ServiceURL: viper.GetString("audio.service_url"),
			Timeout:    viper.GetInt("audio.timeout"),
		},
		Gateway: GatewayConfig{
			Enabled: viper.GetBool("gateway.enabled"),
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
		},
	}

	return cfg, nil
}
