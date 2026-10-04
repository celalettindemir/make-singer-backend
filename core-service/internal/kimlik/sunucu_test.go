package kimlik

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// Issuer bossa OP hic baslamamali: bos issuer ile uretilen discovery
// belgesi kullanilamaz ve hata ancak ilk giris denemesinde gorunur.
func TestIssuerBossaBaslamaz(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = ""
	if _, err := Start(context.Background(), cfg, nil, "development"); err == nil {
		t.Error("bos issuer ile baslatildi")
	}
}

// Sirlar eksikse baslamamali; sessizce zayif varsayilana dusmek
// uretimde fark edilmez.
func TestEksikSirlarlaBaslamaz(t *testing.T) {
	temel := func() *config.AuthConfig {
		c := testAuthCfg()
		c.DBURL = "postgres://yok/yok"
		c.SigningKeyPEM = "gecersiz"
		c.CryptoKey = "kisa"
		return c
	}
	if _, err := Start(context.Background(), temel(), nil, "development"); err == nil {
		t.Error("gecersiz sirlarla baslatildi")
	}
}

// http:// issuer + yerel host (localhost) + production olmayan ortam:
// guvensiz moda izin var, yani Start http/https kontrolunu GECER. Sirlar
// (SigningKeyPEM vb.) testAuthCfg'de eksik oldugu icin Start yine de
// hata doner, ama bu hata https ile ILGILI OLMAMALI — aksi halde http
// kontrolunun kendisi engelliyor demektir.
func TestHTTPIssuerYerelGelistirmedeGuvensizModaGecer(t *testing.T) {
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

// http:// issuer ama host yerel degil (uretim benzeri bir alan adi):
// guvensiz moda izin yok, https zorunlu hatasi donmeli ve Start hicbir
// baglanti acmadan durmali.
func TestHTTPIssuerUzakHostReddedilir(t *testing.T) {
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

// http:// issuer + production ortami: host localhost olsa bile izin
// yok. Uretimde AUTH_ISSUER'a yanlislikla http:// yazilirsa TLS'siz OIDC
// sessizce ayaga kalkmamali.
func TestHTTPIssuerProductiondaReddedilir(t *testing.T) {
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

// ---- I2: gecersiz/0 sure degerleri ----

// viper, COZULEMEYEN bir sure degerinde varsayilana DUSMEZ; sessizce 0s
// verir (orn. AUTH_ACCESS_TTL="15min", AUTH_REFRESH_TTL="60d" —
// dokumanlardaki "15 dakika"/"60 gun" ifadelerinin dogal yazimlari).
// 0 TTL yikicidir: AccessTTL=0 ile access kaydi hic yazilmaz ve jeton
// exp=now ile uretilir (her /api/* 401), RefreshTTL=0 ile her refresh
// kaydi dogdugu an olur (tum kullanicilar aninda disari atilir). Start
// bunlari HIC DOGRULAMIYOR ve hatasiz donuyordu.
func TestGecersizTTLIleBaslamaz(t *testing.T) {
	durumlar := []struct {
		ad       string
		degistir func(*config.AuthConfig)
		anahtar  string
	}{
		{"access_ttl 0", func(c *config.AuthConfig) { c.AccessTTL = 0 }, "access_ttl"},
		{"access_ttl negatif", func(c *config.AuthConfig) { c.AccessTTL = -time.Minute }, "access_ttl"},
		{"refresh_ttl 0", func(c *config.AuthConfig) { c.RefreshTTL = 0 }, "refresh_ttl"},
		{"refresh_ttl negatif", func(c *config.AuthConfig) { c.RefreshTTL = -time.Hour }, "refresh_ttl"},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			cfg := testAuthCfg()
			d.degistir(cfg)
			_, err := Start(context.Background(), cfg, nil, "development")
			if err == nil {
				t.Fatal("gecersiz TTL ile baslatildi")
			}
			// Testin ADININ SOYLEDIGI SEBEPTEN reddedildigini dogrula:
			// testAuthCfg'de sirlar da eksik, yani "bir hata dondu"
			// kontrolu vacuous olurdu.
			if !strings.Contains(err.Error(), d.anahtar) {
				t.Errorf("hata %q icermeli, gelen: %v", d.anahtar, err)
			}
		})
	}
}

// ---- I3: bos ClientID / RedirectURIs ----

// ClientID bos oldugunda middleware.NewOPAuthMiddleware kurulumHatasi'na
// duser ve HER /api/* istegini 401 reddeder; /health ise hala
// "kimlik": true donuyordu (tam kesinti + yanlis saglik sinyali).
// RedirectURIs bos oldugunda her /authorize reddedilir, hic kullanici
// giris yapamaz. config.yaml bunun yasak oldugunu YAZIYORDU ama kod
// zorlamiyordu.
func TestBosIstemciYapilandirmasiIleBaslamaz(t *testing.T) {
	durumlar := []struct {
		ad       string
		degistir func(*config.AuthConfig)
		anahtar  string
	}{
		{"client_id bos", func(c *config.AuthConfig) { c.ClientID = "" }, "client_id"},
		{"redirect_uris nil", func(c *config.AuthConfig) { c.RedirectURIs = nil }, "redirect_uris"},
		{"redirect_uris bos dilim", func(c *config.AuthConfig) { c.RedirectURIs = []string{} }, "redirect_uris"},
		{"redirect_uris bos girdi", func(c *config.AuthConfig) { c.RedirectURIs = []string{"  "} }, "redirect_uris"},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			cfg := testAuthCfg()
			d.degistir(cfg)
			_, err := Start(context.Background(), cfg, nil, "development")
			if err == nil {
				t.Fatal("bos istemci yapilandirmasi ile baslatildi")
			}
			if !strings.Contains(err.Error(), d.anahtar) {
				t.Errorf("hata %q icermeli, gelen: %v", d.anahtar, err)
			}
		})
	}
}

// Gecerli bir yapilandirma bu dogrulamalarin HICBIRINE takilmamali:
// aksi halde yukaridaki testler "her sey reddediliyor" diye de gecerdi.
func TestGecerliYapilandirmaDogrulamayiGecer(t *testing.T) {
	if err := yapilandirmaDogrula(testAuthCfg()); err != nil {
		t.Errorf("gecerli yapilandirma reddedildi: %v", err)
	}
}

// ---- I3: /health GERCEK hazirligi yansitsin ----

// Hazir(), "Start basariyla dondu mu" degil "dinleyici su an hizmet
// veriyor mu" sorusunu yanitlar. nil alici icin de guvenli olmali:
// /health, OP hic kurulmadiginda da bunu cagiriyor.
func TestHazirDinleyiciDurumunuYansitir(t *testing.T) {
	var yok *Sunucu
	if yok.Hazir() {
		t.Error("nil Sunucu hazir gorundu")
	}

	s := &Sunucu{}
	if s.Hazir() {
		t.Error("baslatilmamis Sunucu hazir gorundu")
	}
	s.hazir.Store(true)
	if !s.Hazir() {
		t.Error("hazir=true iken Hazir() false dondu")
	}
	// Dinleyici cokerse (veya kapanirsa) /health bunu gormeli.
	s.hazir.Store(false)
	if s.Hazir() {
		t.Error("dinleyici durduktan sonra hala hazir gorunuyor")
	}
}
