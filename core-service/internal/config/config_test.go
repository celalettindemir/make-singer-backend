package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_R2IkiBucket(t *testing.T) {
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
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.R2.PublicBucket != "makeasinger-public" {
		t.Errorf("PublicBucket = %q, beklenen makeasinger-public", cfg.R2.PublicBucket)
	}
	if cfg.R2.PrivateBucket != "makeasinger-private" {
		t.Errorf("PrivateBucket = %q, beklenen makeasinger-private", cfg.R2.PrivateBucket)
	}
	if cfg.R2.PresignTTL != time.Hour {
		t.Errorf("PresignTTL = %v, beklenen 1h", cfg.R2.PresignTTL)
	}
}

func TestLoad_PresignTTLVarsayilani(t *testing.T) {
	os.Unsetenv("R2_PRESIGN_TTL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.R2.PresignTTL != time.Hour {
		t.Errorf("varsayilan PresignTTL = %v, beklenen 1h", cfg.R2.PresignTTL)
	}
}

func TestR2Config_PresignTTLOrDefault(t *testing.T) {
	var nilCfg *R2Config
	if got := nilCfg.PresignTTLOrDefault(); got != time.Hour {
		t.Errorf("nil config icin PresignTTLOrDefault = %v, beklenen 1h", got)
	}

	zeroCfg := &R2Config{}
	if got := zeroCfg.PresignTTLOrDefault(); got != time.Hour {
		t.Errorf("sifir PresignTTL icin PresignTTLOrDefault = %v, beklenen 1h", got)
	}

	setCfg := &R2Config{PresignTTL: 2 * time.Hour}
	if got := setCfg.PresignTTLOrDefault(); got != 2*time.Hour {
		t.Errorf("PresignTTLOrDefault = %v, beklenen config'teki 2h (sabit 1h'e duselmemeliydi)", got)
	}
}

func TestAuthVarsayilanlari(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Port != "8001" {
		t.Errorf("Auth.Port = %q, beklenen %q", cfg.Auth.Port, "8001")
	}
	if cfg.Auth.AccessTTL != 15*time.Minute {
		t.Errorf("Auth.AccessTTL = %v, beklenen 15m", cfg.Auth.AccessTTL)
	}
	if cfg.Auth.RefreshTTL != 1440*time.Hour {
		t.Errorf("Auth.RefreshTTL = %v, beklenen 1440h", cfg.Auth.RefreshTTL)
	}
}
