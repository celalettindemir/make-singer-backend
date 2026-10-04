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

// Bu test bir DAVRANIS BELGESIDIR, bir sozlesme degil: viper'in
// cozulemeyen bir sure degerinde varsayilana DUSMEDIGINI, sessizce 0s
// verdigini kayda geciriyor. "15min" ve "60d", dokumanlarin her yerinde
// gecen "15 dakika"/"60 gun" ifadelerinin dogal ama
// time.ParseDuration'in ANLAMADIGI yazimlaridir, yani bu gercekci bir
// yanlis yapilandirma. Sonuclari yikici (AccessTTL=0 => her /api/* 401;
// RefreshTTL=0 => tum kullanicilar aninda disari atilir), bu yuzden
// kimlik.Start TTL'leri ACIKCA dogrular ve <= 0 ise HIC BASLAMAZ
// (bkz. TestGecersizTTLIleBaslamaz).
func TestLoad_CozulemeyenTTLVarsayilanaDusmezSessizce0Olur(t *testing.T) {
	os.Setenv("AUTH_ACCESS_TTL", "15min")
	os.Setenv("AUTH_REFRESH_TTL", "60d")
	defer func() {
		os.Unsetenv("AUTH_ACCESS_TTL")
		os.Unsetenv("AUTH_REFRESH_TTL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.Auth.AccessTTL != 0 {
		t.Errorf("AccessTTL = %v; bu test viper'in 0 verdigini belgeliyor, davranis degistiyse kimlik.Start dogrulamasi gozden gecirilmeli", cfg.Auth.AccessTTL)
	}
	if cfg.Auth.RefreshTTL != 0 {
		t.Errorf("RefreshTTL = %v; ayni not", cfg.Auth.RefreshTTL)
	}
}

// Hiz limiti varsayilanlari gercekten yukleniyor mu: 0 kalirsa
// kimlik.NewHizLimit hata doner ve OP hic baslamaz.
func TestLoad_HizLimitiVarsayilanlari(t *testing.T) {
	for _, ad := range []string{
		"AUTH_LOGIN_IP_PER_MIN", "AUTH_LOGIN_EMAIL_PER_HOUR",
		"AUTH_SIGNUP_IP_PER_HOUR", "AUTH_TRUSTED_PROXIES",
	} {
		os.Unsetenv(ad)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.Auth.LoginIPPerMin != 10 {
		t.Errorf("LoginIPPerMin = %d, beklenen 10", cfg.Auth.LoginIPPerMin)
	}
	if cfg.Auth.LoginEmailPerHour != 60 {
		t.Errorf("LoginEmailPerHour = %d, beklenen 60", cfg.Auth.LoginEmailPerHour)
	}
	if cfg.Auth.SignupIPPerHour != 5 {
		t.Errorf("SignupIPPerHour = %d, beklenen 5", cfg.Auth.SignupIPPerHour)
	}
	if cfg.Auth.TrustedProxies != 1 {
		t.Errorf("TrustedProxies = %d, beklenen 1", cfg.Auth.TrustedProxies)
	}
}

// Ortam degiskeni gercekten ezebiliyor mu (yalnizca varsayilan degil).
func TestLoad_HizLimitiOrtamDegiskeniEzer(t *testing.T) {
	os.Setenv("AUTH_LOGIN_IP_PER_MIN", "3")
	os.Setenv("AUTH_TRUSTED_PROXIES", "0")
	defer func() {
		os.Unsetenv("AUTH_LOGIN_IP_PER_MIN")
		os.Unsetenv("AUTH_TRUSTED_PROXIES")
	}()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.Auth.LoginIPPerMin != 3 {
		t.Errorf("LoginIPPerMin = %d, beklenen 3", cfg.Auth.LoginIPPerMin)
	}
	if cfg.Auth.TrustedProxies != 0 {
		t.Errorf("TrustedProxies = %d, beklenen 0", cfg.Auth.TrustedProxies)
	}
}
