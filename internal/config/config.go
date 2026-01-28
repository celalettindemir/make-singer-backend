package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig
	Redis    RedisConfig
	JWT      JWTConfig
	RateLimit RateLimitConfig
}

type ServerConfig struct {
	Port string
	Env  string
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
	LyricsPerMin   int
	RenderPerHour  int
	MasterPerHour  int
	ExportPerHour  int
	UploadPerHour  int
}

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// Environment variables
	viper.AutomaticEnv()

	// Defaults
	viper.SetDefault("server.port", "3000")
	viper.SetDefault("server.env", "development")
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

	// Try to read config file (optional)
	_ = viper.ReadInConfig()

	cfg := &Config{
		Server: ServerConfig{
			Port: viper.GetString("server.port"),
			Env:  viper.GetString("server.env"),
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
	}

	return cfg, nil
}
