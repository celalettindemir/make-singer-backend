package config

import (
	"os"
	"strings"

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

type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	BucketName      string
	PublicURL       string
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

func Load() (*Config, error) {
	// Read Docker Swarm secrets from _FILE env vars before Viper binds
	readSecret("REDIS_PASSWORD")
	readSecret("GROQ_API_KEY")
	readSecret("SUNO_API_KEY")
	readSecret("R2_ACCOUNT_ID")
	readSecret("R2_ACCESS_KEY_ID")
	readSecret("R2_SECRET_ACCESS_KEY")
	readSecret("ZITADEL_CLIENT_ID")

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
	_ = viper.BindEnv("r2.bucket_name", "R2_BUCKET_NAME")
	_ = viper.BindEnv("r2.public_url", "R2_PUBLIC_URL")
	_ = viper.BindEnv("zitadel.domain", "ZITADEL_DOMAIN")
	_ = viper.BindEnv("zitadel.client_id", "ZITADEL_CLIENT_ID")
	_ = viper.BindEnv("zitadel.issuer", "ZITADEL_ISSUER")
	_ = viper.BindEnv("suno.api_key", "SUNO_API_KEY")
	_ = viper.BindEnv("suno.base_url", "SUNO_BASE_URL")
	_ = viper.BindEnv("audio.service_url", "AUDIO_SERVICE_URL")
	_ = viper.BindEnv("audio.timeout", "AUDIO_SERVICE_TIMEOUT")
	_ = viper.BindEnv("server.api_domain", "API_DOMAIN")
	_ = viper.BindEnv("gateway.enabled", "GATEWAY_ENABLED")

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
			BucketName:      viper.GetString("r2.bucket_name"),
			PublicURL:       viper.GetString("r2.public_url"),
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
	}

	return cfg, nil
}
