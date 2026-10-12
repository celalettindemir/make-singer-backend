package identity

import (
	"net/http"
	"net/url"
	"testing"
)

// Bu dosya OLCUM ARACINI olcer: cerezKavanozu ve yardimcilari
// (tasinirMi, cerezYoluEsler, adimBaglami) federasyon saldiri
// testlerinin ZEMINIDIR.
//
// NEDEN GEREKLI: kavanoz fazla GEVSEK olursa saldiri testleri YANLIS
// YESIL verir ve bunu hicbir sey kirmiziya cevirmez. Ornek: tasinirMi
// "hep true" donerse Apple form_post testi Google GET akisinin
// kopyasina doner ve SameSite=None zorunlulugunun (K2) canli kaniti
// SESSIZCE kaybolur. Burada kavanozun kendisi dogrudan olculuyor,
// boylece o gevseme derhal gorulur.
//
// Kavanozun davranisi uctan uca testlerin hicbir yerinde dolayli
// olarak olculemez: akis yesil kaldiginda kavanozun kurali uyguladigi
// mi, kurali hic uygulamadigi mi bilinmez.

// Testlerde kullanilan sabit adres: kavanoz host + path uzerinden
// secim yapar, scheme'i kullanmaz.
const kavanozHedef = "http://kimlik.ornek/federasyon/google/geri"

// TestCerezKavanozuSameSiteUygular, tasinirMi'nin tarayici kuralini
// GERCEKTEN uyguladigini olcer. Standart net/http/cookiejar SameSite'i
// yalnizca KAYDEDER, istek seciminde kullanmaz; bu kavanozun tum varlik
// sebebi o bosluktur.
func TestCerezKavanozuSameSiteUygular(t *testing.T) {
	durumlar := []struct {
		ad       string
		sameSite http.SameSite
		baglam   cerezBaglam
		beklenen bool
	}{
		{
			// Apple'in form_post donusu tam budur: Lax cerez
			// TASINMAZ, bu yuzden federasyon cerezi None olmak
			// zorundadir (K2).
			ad:       "Lax capraz-site POST'ta tasinmaz",
			sameSite: http.SameSiteLaxMode, baglam: baglamCaprazPost, beklenen: false,
		},
		{
			ad:       "Lax ayni-site'ta tasinir",
			sameSite: http.SameSiteLaxMode, baglam: baglamAyniSite, beklenen: true,
		},
		{
			ad:       "None capraz-site POST'ta tasinir",
			sameSite: http.SameSiteNoneMode, baglam: baglamCaprazPost, beklenen: true,
		},
		{
			// Google'in 302 donusu ust-duzey capraz-site GET'tir:
			// Lax tasinir, Strict tasinmaz.
			ad:       "Strict capraz-site GET'te tasinmaz",
			sameSite: http.SameSiteStrictMode, baglam: baglamCaprazGet, beklenen: false,
		},
		{
			ad:       "Lax capraz-site GET'te tasinir",
			sameSite: http.SameSiteLaxMode, baglam: baglamCaprazGet, beklenen: true,
		},
		{
			// Kullanicinin kendi baslattigi navigasyon (native
			// uygulama sistem tarayicisini acar): her sey tasinir.
			ad:       "Strict baslatma navigasyonunda tasinir",
			sameSite: http.SameSiteStrictMode, baglam: baglamBaslatma, beklenen: true,
		},
	}

	hedef, err := url.Parse(kavanozHedef)
	if err != nil {
		t.Fatalf("hedef cozulemedi: %v", err)
	}

	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			kavanoz := yeniCerezKavanozu()
			kavanoz.SetCookies(hedef, []*http.Cookie{{
				Name: "deneme", Value: "deger", Path: "/federasyon", SameSite: d.sameSite,
			}})
			kavanoz.baglamKur(d.baglam)
			tasindi := len(kavanoz.Cookies(hedef)) == 1
			if tasindi != d.beklenen {
				t.Errorf("tasindi = %v, beklenen %v", tasindi, d.beklenen)
			}
		})
	}
}

// TestCerezKavanozuYolSiniri, cerezYoluEsler'in RFC 6265 yol onekini
// uyguladigini olcer. Federasyon cerezi Path=/federasyon ile yazilir:
// kavanoz yolu yok sayarsa cerezin yuzeyinin sinirli oldugu iddiasi
// (TestUctanUcaAppleGirisiFormPost) OLCULMEZ olur.
func TestCerezKavanozuYolSiniri(t *testing.T) {
	durumlar := []struct {
		cerezYol string
		istekYol string
		beklenen bool
	}{
		{"/federasyon", "/federasyon", true},
		{"/federasyon", "/federasyon/google/geri", true},
		{"/federasyon/", "/federasyon/google", true},
		// Yol onekini KARAKTER olarak degil SEGMENT olarak uygula:
		// /federasyonlar, /federasyon'un alti degildir.
		{"/federasyon", "/federasyonlar", false},
		{"/federasyon", "/giris", false},
		{"/", "/herhangi", true},
	}

	for _, d := range durumlar {
		if sonuc := cerezYoluEsler(d.cerezYol, d.istekYol); sonuc != d.beklenen {
			t.Errorf("cerezYoluEsler(%q, %q) = %v, beklenen %v",
				d.cerezYol, d.istekYol, sonuc, d.beklenen)
		}
	}

	// Yol KAVANOZDA da uygulanmali: yalnizca yardimcinin dogru olmasi
	// Cookies()'in onu cagirdigini kanitlamaz.
	yazan, err := url.Parse("http://kimlik.ornek/federasyon/google/baslat")
	if err != nil {
		t.Fatalf("adres cozulemedi: %v", err)
	}
	kavanoz := yeniCerezKavanozu()
	kavanoz.SetCookies(yazan, []*http.Cookie{{
		Name: "deneme", Value: "deger", Path: "/federasyon", SameSite: http.SameSiteNoneMode,
	}})
	kavanoz.baglamKur(baglamAyniSite)
	disYol, err := url.Parse("http://kimlik.ornek/giris")
	if err != nil {
		t.Fatalf("adres cozulemedi: %v", err)
	}
	if n := len(kavanoz.Cookies(disYol)); n != 0 {
		t.Errorf("/giris'e %d cerez tasindi, beklenen 0 (Path=/federasyon)", n)
	}
	if n := len(kavanoz.Cookies(yazan)); n != 1 {
		t.Errorf("/federasyon/... yoluna %d cerez tasindi, beklenen 1", n)
	}
}

// TestCerezKavanozuHostAyrimi, cerezlerin host basina tutuldugunu
// olcer: sahte yukari akis ve kendi dinleyicimiz AYRI host'lardir ve
// birinin cerezi digerine GITMEMELI.
func TestCerezKavanozuHostAyrimi(t *testing.T) {
	biz, err := url.Parse("http://kimlik.ornek/federasyon")
	if err != nil {
		t.Fatalf("adres cozulemedi: %v", err)
	}
	yukari, err := url.Parse("http://yukari.ornek/federasyon")
	if err != nil {
		t.Fatalf("adres cozulemedi: %v", err)
	}
	kavanoz := yeniCerezKavanozu()
	kavanoz.SetCookies(biz, []*http.Cookie{{
		Name: "deneme", Value: "deger", Path: "/federasyon", SameSite: http.SameSiteNoneMode,
	}})
	kavanoz.baglamKur(baglamAyniSite)
	if n := len(kavanoz.Cookies(yukari)); n != 0 {
		t.Errorf("baska host'a %d cerez tasindi, beklenen 0", n)
	}
	if n := len(kavanoz.Cookies(biz)); n != 1 {
		t.Errorf("kendi host'una %d cerez tasindi, beklenen 1", n)
	}
}

// TestCerezKavanozuSilme, cerezSil'in TUM host'lardan sildigini olcer:
// "cerez olmadan tamamlanamaz" iddiasinda tek degiskenin cerez oldugu
// buna dayanir (TestSaldiriCerezsizCallbackReddedilir).
func TestCerezKavanozuSilme(t *testing.T) {
	hedef, err := url.Parse(kavanozHedef)
	if err != nil {
		t.Fatalf("hedef cozulemedi: %v", err)
	}
	kavanoz := yeniCerezKavanozu()
	kavanoz.SetCookies(hedef, []*http.Cookie{
		{Name: cerezAdFederasyon, Value: "deger", Path: "/federasyon", SameSite: http.SameSiteNoneMode},
		{Name: "baska", Value: "kalsin", Path: "/", SameSite: http.SameSiteNoneMode},
	})
	kavanoz.baglamKur(baglamAyniSite)
	if n := len(kavanoz.Cookies(hedef)); n != 2 {
		t.Fatalf("silme oncesi %d cerez, beklenen 2", n)
	}
	kavanoz.cerezSil(cerezAdFederasyon)
	kalan := kavanoz.Cookies(hedef)
	if len(kalan) != 1 || kalan[0].Name != "baska" {
		t.Errorf("silmeden sonra kalan = %v, beklenen yalnizca \"baska\"", kalan)
	}
	if kavanoz.cerezDegeri(cerezAdFederasyon) != "" {
		t.Error("cerezDegeri silinmis cerezi hala donduruyor")
	}
}

// TestAdimBaglami, yonlendirme zincirinin her adimindaki cerez
// baglaminin kaynak/hedef host ve yonteme gore dogru siniflandigini
// olcer. Yanlis siniflandirma, capraz-site donusu ayni-site saymaya
// yol acar ve SameSite kurali hic devreye girmez.
func TestAdimBaglami(t *testing.T) {
	coz := func(ham string) *url.URL {
		t.Helper()
		u, err := url.Parse(ham)
		if err != nil {
			t.Fatalf("adres cozulemedi (%q): %v", ham, err)
		}
		return u
	}
	biz := coz("http://kimlik.ornek/federasyon/google/baslat")
	bizBaska := coz("http://kimlik.ornek/authorize/callback")
	yukari := coz("http://yukari.ornek/authorize")

	durumlar := []struct {
		ad       string
		kaynak   *url.URL
		hedef    *url.URL
		yontem   string
		beklenen cerezBaglam
	}{
		{"ayni host GET ayni-site", biz, bizBaska, http.MethodGet, baglamAyniSite},
		{"ayni host POST ayni-site", biz, bizBaska, http.MethodPost, baglamAyniSite},
		{"baska host GET capraz-GET", yukari, bizBaska, http.MethodGet, baglamCaprazGet},
		// Apple'in form_post donusu: bizim icin capraz-site POST.
		{"baska host POST capraz-POST", yukari, bizBaska, http.MethodPost, baglamCaprazPost},
		// Zincirin ilk adiminda kaynak YOKTUR; capraz sayilmak
		// GUVENLI yondur (daha az cerez tasinir).
		{"kaynaksiz GET capraz-GET", nil, biz, http.MethodGet, baglamCaprazGet},
	}

	for _, d := range durumlar {
		if sonuc := adimBaglami(d.kaynak, d.hedef, d.yontem); sonuc != d.beklenen {
			t.Errorf("%s: adimBaglami = %v, beklenen %v", d.ad, sonuc, d.beklenen)
		}
	}
}
