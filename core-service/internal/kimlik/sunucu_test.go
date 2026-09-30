package kimlik

import (
	"context"
	"testing"

	"github.com/makeasinger/api/internal/config"
)

// Issuer bossa OP hic baslamamali: bos issuer ile uretilen discovery
// belgesi kullanilamaz ve hata ancak ilk giris denemesinde gorunur.
func TestIssuerBossaBaslamaz(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = ""
	if _, err := Start(context.Background(), cfg, nil); err == nil {
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
	if _, err := Start(context.Background(), temel(), nil); err == nil {
		t.Error("gecersiz sirlarla baslatildi")
	}
}
