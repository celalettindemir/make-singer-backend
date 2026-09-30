package kimlik

import (
	"context"
	"strings"
	"testing"

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
