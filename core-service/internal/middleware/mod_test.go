package middleware

import "testing"

// Mod secimi YAPILANDIRMADAN okunur. En kritik guvence: OP issuer'i
// doluyken legacy (ve gateway) yolu SECILEMEZ. Eskiden secim
// `kimlikSunucu != nil` ile yapiliyordu; OP baslatilamadigi her durumda
// (DB erisilemez, migration patladi, anahtar yuklenemedi) sessizce
// legacy HMAC'e dusuluyordu ve legacy dogrulama ne exp ne iss ne aud
// istiyor, sirri da varsayilan `change-me-in-production` olabiliyor.
func TestAPIAuthModuSec(t *testing.T) {
	durumlar := []struct {
		ad       string
		issuer   string
		gateway  bool
		beklenen APIAuthModu
	}{
		{"issuer dolu, gateway kapali", "https://kimlik.ornek.dev", false, ModOP},
		// Issuer doluyken gateway bayragi mod secimini DEGISTIREMEZ.
		{"issuer dolu, gateway acik", "https://kimlik.ornek.dev", true, ModOP},
		{"issuer bos, gateway acik", "", true, ModGateway},
		{"issuer bos, gateway kapali", "", false, ModLegacy},
	}
	for _, d := range durumlar {
		if secilen := APIAuthModuSec(d.issuer, d.gateway); secilen != d.beklenen {
			t.Errorf("%s: mod = %v, beklenen %v", d.ad, secilen, d.beklenen)
		}
	}
}

// OP baslatma hatasi senaryosunun ozu: Start basarisiz olsa bile
// (kimlikSunucu == nil) mod secimi yapilandirmadan okundugu icin legacy
// yol SECILEMEZ. main.go bu durumda ayrica log.Fatalf ile servisi hic
// baslatmaz; burada olculen sey, secim mantiginin calisma zamani
// basarisina bakmadigidir.
func TestOPBaslatmaHatasindaLegacySecilemez(t *testing.T) {
	const issuer = "https://kimlik.ornek.dev"
	for _, gateway := range []bool{false, true} {
		secilen := APIAuthModuSec(issuer, gateway)
		if secilen == ModLegacy {
			t.Fatalf("gateway=%v: legacy mod secildi — fail-open", gateway)
		}
		if secilen == ModGateway {
			t.Fatalf("gateway=%v: gateway modu secildi — fail-open", gateway)
		}
		if secilen != ModOP {
			t.Fatalf("gateway=%v: mod = %v, beklenen ModOP", gateway, secilen)
		}
	}
}
