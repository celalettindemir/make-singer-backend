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
	// FAIL-SAFE: varsayilan 0, yani X-Forwarded-For HIC okunmaz. 1
	// varsayilani, onunde proxy olmayan bir kurulumda uydurma tek
	// elemanli bir XFF'i guvenilir sayar ve IP limiti tamamen atlanir
	// (bkz. config.go trusted_proxies notu). Proxy arkasinda deger
	// ACIKCA verilir; bir sonraki test ezmenin calistigini olcer.
	if cfg.Auth.TrustedProxies != 0 {
		t.Errorf("TrustedProxies = %d, beklenen 0 (varsayilan fail-safe olmali)", cfg.Auth.TrustedProxies)
	}
}

// Ortam degiskeni gercekten ezebiliyor mu (yalnizca varsayilan degil).
func TestLoad_HizLimitiOrtamDegiskeniEzer(t *testing.T) {
	os.Setenv("AUTH_LOGIN_IP_PER_MIN", "3")
	os.Setenv("AUTH_TRUSTED_PROXIES", "2")
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
	// Varsayilandan FARKLI bir deger: aksi halde bu dal varsayilanla
	// ayni sonucu olcer ve "ortam degiskeni ezer" iddiasi vacuous olur.
	if cfg.Auth.TrustedProxies != 2 {
		t.Errorf("TrustedProxies = %d, beklenen 2", cfg.Auth.TrustedProxies)
	}
}

// federasyonEnvTemizle, AUTH_FED_* ortam degiskenlerinin HICBIRINI
// birakmadan silinmesini saglar. Her federasyon testi bunu hem basta hem
// defer ile cagirir: aksi halde bir testin birakttigi deger baska bir
// testi yanlis sebepten gecirir/dusurur (ornegin paralel calisan baska
// bir paket testi degil ama bu paket icindeki sonraki test).
func federasyonEnvTemizle() {
	for _, ad := range []string{
		"AUTH_FED_GOOGLE_CLIENT_ID", "AUTH_FED_GOOGLE_CLIENT_SECRET",
		"AUTH_FED_APPLE_CLIENT_ID", "AUTH_FED_APPLE_TEAM_ID",
		"AUTH_FED_APPLE_KEY_ID", "AUTH_FED_APPLE_KEY_P8",
	} {
		os.Unsetenv(ad)
	}
}

// federasyonBul, listede verilen Ad'a sahip girdiyi (varsa) doner. Testler
// bunu kullanarak "hangi saglayicilar VAR" sorusunu "liste uzunlugu kac"
// sorusundan ayri test eder; uzunluk-only bir kontrol, fonksiyon yanlis
// saglayiciyi dondurse de gecerdi.
func federasyonBul(liste []FederasyonIstemci, ad string) (FederasyonIstemci, bool) {
	for _, f := range liste {
		if f.Ad == ad {
			return f, true
		}
	}
	return FederasyonIstemci{}, false
}

// Her iki saglayici da tam yapilandirilmisken ikisi de listede olmali ve
// alanlari (Apple'in ucu dahil) BOZULMADAN tasinmali: Gorev 4'teki
// JetonDegis, Apple client_secret'ini tam bu uc alandan uretir.
func TestLoad_FederasyonIkisiTamYapilandirilinca(t *testing.T) {
	federasyonEnvTemizle()
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_ID", "google-cid")
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_SECRET", "google-sir")
	os.Setenv("AUTH_FED_APPLE_CLIENT_ID", "apple-cid")
	os.Setenv("AUTH_FED_APPLE_TEAM_ID", "team-1")
	os.Setenv("AUTH_FED_APPLE_KEY_ID", "key-1")
	os.Setenv("AUTH_FED_APPLE_KEY_P8", "-----BEGIN PRIVATE KEY-----p8-verisi")
	defer federasyonEnvTemizle()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if len(cfg.Auth.Federasyon) != 2 {
		t.Fatalf("Federasyon uzunlugu = %d, beklenen 2: %+v", len(cfg.Auth.Federasyon), cfg.Auth.Federasyon)
	}

	google, ok := federasyonBul(cfg.Auth.Federasyon, "google")
	if !ok {
		t.Fatalf("google saglayicisi listede yok: %+v", cfg.Auth.Federasyon)
	}
	if google.IstemciID != "google-cid" || google.IstemciSir != "google-sir" {
		t.Errorf("google = %+v, beklenen IstemciID=google-cid IstemciSir=google-sir", google)
	}

	apple, ok := federasyonBul(cfg.Auth.Federasyon, "apple")
	if !ok {
		t.Fatalf("apple saglayicisi listede yok: %+v", cfg.Auth.Federasyon)
	}
	if apple.IstemciID != "apple-cid" {
		t.Errorf("apple.IstemciID = %q, beklenen apple-cid", apple.IstemciID)
	}
	if apple.AppleEkipID != "team-1" || apple.AppleAnahtarID != "key-1" || apple.AppleAnahtarP8 != "-----BEGIN PRIVATE KEY-----p8-verisi" {
		t.Errorf("apple'in uc alani bozulmus tasindi: %+v", apple)
	}
}

// Google'in client_id'si bossa Google listeye HIC EKLENMEMELI (bos
// girdiyle degil, yoklukla). Apple tek basina kalmali.
func TestLoad_FederasyonGoogleBossaEklenmez(t *testing.T) {
	federasyonEnvTemizle()
	os.Setenv("AUTH_FED_APPLE_CLIENT_ID", "apple-cid")
	os.Setenv("AUTH_FED_APPLE_TEAM_ID", "team-1")
	os.Setenv("AUTH_FED_APPLE_KEY_ID", "key-1")
	os.Setenv("AUTH_FED_APPLE_KEY_P8", "p8-verisi")
	defer federasyonEnvTemizle()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if _, ok := federasyonBul(cfg.Auth.Federasyon, "google"); ok {
		t.Errorf("google client_id bossa listede olmamali: %+v", cfg.Auth.Federasyon)
	}
	apple, ok := federasyonBul(cfg.Auth.Federasyon, "apple")
	if !ok {
		t.Fatalf("apple listede yok: %+v", cfg.Auth.Federasyon)
	}
	if apple.IstemciID != "apple-cid" {
		t.Errorf("apple.IstemciID = %q, beklenen apple-cid", apple.IstemciID)
	}
	if len(cfg.Auth.Federasyon) != 1 {
		t.Errorf("Federasyon uzunlugu = %d, beklenen 1 (sadece apple)", len(cfg.Auth.Federasyon))
	}
}

// Apple'in client_id'si bossa Apple listeye HIC EKLENMEMELI, uc alani
// (team/key/p8) dolu olsa bile. Google tek basina kalmali.
func TestLoad_FederasyonAppleBossaEklenmez(t *testing.T) {
	federasyonEnvTemizle()
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_ID", "google-cid")
	os.Setenv("AUTH_FED_GOOGLE_CLIENT_SECRET", "google-sir")
	// Apple'in client_id'si BILEREK bos; uc alani dolu olsa da Apple
	// eklenmemeli (client_id eksikse digerleri anlamsiz).
	os.Setenv("AUTH_FED_APPLE_TEAM_ID", "team-1")
	os.Setenv("AUTH_FED_APPLE_KEY_ID", "key-1")
	os.Setenv("AUTH_FED_APPLE_KEY_P8", "p8-verisi")
	defer federasyonEnvTemizle()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if _, ok := federasyonBul(cfg.Auth.Federasyon, "apple"); ok {
		t.Errorf("apple client_id bossa listede olmamali: %+v", cfg.Auth.Federasyon)
	}
	google, ok := federasyonBul(cfg.Auth.Federasyon, "google")
	if !ok {
		t.Fatalf("google listede yok: %+v", cfg.Auth.Federasyon)
	}
	if google.IstemciID != "google-cid" {
		t.Errorf("google.IstemciID = %q, beklenen google-cid", google.IstemciID)
	}
	if len(cfg.Auth.Federasyon) != 1 {
		t.Errorf("Federasyon uzunlugu = %d, beklenen 1 (sadece google)", len(cfg.Auth.Federasyon))
	}
}

// Ikisi de bossa liste bos/nil olmali ve hic bir sey panik atmamali.
func TestLoad_FederasyonIkisiDeBossaBos(t *testing.T) {
	federasyonEnvTemizle()
	defer federasyonEnvTemizle()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if len(cfg.Auth.Federasyon) != 0 {
		t.Errorf("Federasyon uzunlugu = %d, beklenen 0: %+v", len(cfg.Auth.Federasyon), cfg.Auth.Federasyon)
	}
	// Erisim panik atmamali (nil dilimde indeksleme yapilmiyor, ama
	// range ve len guvenle calismali).
	for _, f := range cfg.Auth.Federasyon {
		t.Errorf("beklenmeyen girdi: %+v", f)
	}
}

func TestLoad_SunoVarsayilanlari(t *testing.T) {
	os.Unsetenv("SUNO_MODEL")
	os.Unsetenv("SUNO_CALLBACK_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.Suno.Model != "V6" {
		t.Errorf("Suno.Model = %q, beklenen V6", cfg.Suno.Model)
	}
	if cfg.Suno.CallbackURL == "" {
		t.Error("Suno.CallbackURL bos; saglayici zorunlu tutuyor")
	}
}

func TestLoad_SunoModelEnvIleDegisir(t *testing.T) {
	os.Setenv("SUNO_MODEL", "V6_MINI")
	defer os.Unsetenv("SUNO_MODEL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.Suno.Model != "V6_MINI" {
		t.Errorf("Suno.Model = %q, beklenen V6_MINI", cfg.Suno.Model)
	}
}
