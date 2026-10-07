package kimlik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// ---------------------------------------------------------------------
// Yardimcilar
// ---------------------------------------------------------------------

const testSifre = "cokgizlisifre"

// iddiaKur, dogrulanmis bir yukari akis id_token'inin iddialarini
// taklit eder. kullaniciCoz'un girdisi tam olarak budur: imza/nonce
// dogrulamasi ondan ONCE yapilir (bkz. Geri).
func iddiaKur(sub, eposta string, dogrulandi bool) *auth.Claims {
	return &auth.Claims{
		UserID:        sub,
		Email:         eposta,
		EmailVerified: auth.EsnekBool(dogrulandi),
		Name:          "Test Kullanici",
	}
}

// oturumBagla, /authorize bacaginin yaptigi isi taklit eder: verilen
// authRequestID'yi bir tarayiciya baglar ve o tarayicinin oturum
// cerezini doner. /baglama sayfasi Faz 1'in giris sayfasiyla AYNI
// oturum baglamasini zorunlu kilar.
func (o *fedOrtam) oturumBagla(t *testing.T, authReqID string) *http.Cookie {
	t.Helper()
	oturum, err := rasgeleJeton()
	if err != nil {
		t.Fatalf("oturum degeri uretilemedi: %v", err)
	}
	csrf, err := rasgeleJeton()
	if err != nil {
		t.Fatalf("csrf uretilemedi: %v", err)
	}
	var baglama OturumBaglama
	baglama.Ekle(authReqID, csrf)
	if err := o.oturumlar.OturumYaz(context.Background(), oturum, baglama, time.Minute); err != nil {
		t.Fatalf("oturum yazilamadi: %v", err)
	}
	return &http.Cookie{Name: cerezAdOturum, Value: oturum}
}

func (o *fedOrtam) baglamaGetIstek(beklemeID string, cerez *http.Cookie) *httptest.ResponseRecorder {
	hedef := yolBaglama
	if beklemeID != "" {
		hedef += "?" + url.Values{alanBeklemeID: {beklemeID}}.Encode()
	}
	r := httptest.NewRequest(http.MethodGet, hedef, nil)
	if cerez != nil {
		r.AddCookie(cerez)
	}
	w := httptest.NewRecorder()
	o.mux.ServeHTTP(w, r)
	return w
}

func (o *fedOrtam) baglamaPostIstek(deger url.Values, cerez *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, yolBaglama, strings.NewReader(deger.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cerez != nil {
		r.AddCookie(cerez)
	}
	w := httptest.NewRecorder()
	o.mux.ServeHTTP(w, r)
	return w
}

// beklemeKur, GERCEK federasyon akisini kostur: e-postasi cakisan
// SIFRELI bir hesap varken Google ile girilir, akis /baglama'ya
// yonlendirir. Donenler: bekleme kaydinin id'si, formdaki CSRF jetonu,
// tarayicinin oturum cerezi ve cakisan hesap.
func (o *fedOrtam) beklemeKur(t *testing.T) (string, string, *http.Cookie, *User) {
	t.Helper()
	o.google.ayarla(func(s *sahteSaglayici) { s.eposta = "cakisan@ornek.com" })
	mevcut, err := o.kullanici.Create(context.Background(), "cakisan@ornek.com", "Mevcut", testSifre)
	if err != nil {
		t.Fatalf("mevcut hesap olusturulamadi: %v", err)
	}

	w := o.akisKos(t, saglayiciGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302 (/baglama'ya yonlendirme), govde = %q", w.Code, w.Body.String())
	}
	konum, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location cozumlenemedi: %v", err)
	}
	if konum.Path != yolBaglama {
		t.Fatalf("Location = %q, beklenen %s", w.Header().Get("Location"), yolBaglama)
	}
	beklemeID := konum.Query().Get(alanBeklemeID)
	if beklemeID == "" {
		t.Fatal("yonlendirmede beklemeID yok")
	}
	if o.istekler.subject(o.authReqID) != "" {
		t.Fatal("cakisma varken istek TAMAMLANMAMALI")
	}

	cerez := o.oturumCerez
	form := o.baglamaGetIstek(beklemeID, cerez)
	if form.Code != http.StatusOK {
		t.Fatalf("baglama formu acilamadi: durum = %d, govde = %q", form.Code, form.Body.String())
	}
	esler := csrfDeseni.FindStringSubmatch(form.Body.String())
	if esler == nil || esler[1] == "" {
		t.Fatalf("formda CSRF jetonu yok: %q", form.Body.String())
	}
	if !strings.Contains(form.Body.String(), "cakisan@ornek.com") {
		t.Error("form cakisan e-postayi gostermiyor")
	}
	return beklemeID, esler[1], cerez, mevcut
}

// ---------------------------------------------------------------------
// Step 1: Cozum sirasi testleri
// ---------------------------------------------------------------------

//  1. Bagli kimlik HER ZAMAN once gelir: kullanicinin Google'daki
//     e-postasi degismis olabilir ve o zaman e-postaya gore arama
//     BASKA bir hesabi (ya da hic) bulur.
func TestCozumBagliKimlikEpostadanOnceGelir(t *testing.T) {
	o := testFedereSayfalar(t)
	ctx := context.Background()

	a, err := o.kullanici.Create(ctx, "a@ornek.com", "A", testSifre)
	if err != nil {
		t.Fatalf("A olusturulamadi: %v", err)
	}
	if err := o.federe.Bagla(ctx, saglayiciGoogle, "sub-1", a.ID); err != nil {
		t.Fatalf("A baglanamadi: %v", err)
	}
	b, err := o.kullanici.Create(ctx, "yeni@ornek.com", "B", testSifre)
	if err != nil {
		t.Fatalf("B olusturulamadi: %v", err)
	}

	// id_token: sub=sub-1 (A'ya bagli) ama e-posta B'ye ait.
	ku, cakisma, err := o.f.kullaniciCoz(ctx, saglayiciGoogle, iddiaKur("sub-1", "yeni@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if cakisma != nil {
		t.Fatalf("cakisma dondu: %+v (bagli kimlik e-postadan ONCE gelmeli)", cakisma)
	}
	if ku == nil || ku.ID != a.ID {
		t.Fatalf("kullanici = %v, beklenen A (%s)", ku, a.ID)
	}
	if ku.ID == b.ID {
		t.Error("e-posta uzerinden B'ye dusuldu")
	}
}

//  2. Apple gizli aktarma adresi ASLA baglama icin kullanilmaz.
//     Bu, spec'in acikca yasakladigi tek durumdur.
func TestGizliAktarmaAdresiBaglamaTetiklemez(t *testing.T) {
	t.Run("adres SIFRELI bir hesapta kayitli: baglama YOK", func(t *testing.T) {
		o := testFedereSayfalar(t)
		ctx := context.Background()
		// Saldirgan kurbanin relay adresiyle sifreli bir hesap acmis
		// olabilir.
		if _, err := o.kullanici.Create(ctx, "abc"+gizliAktarmaSoneki, "Saldirgan", testSifre); err != nil {
			t.Fatalf("hesap olusturulamadi: %v", err)
		}

		ku, cakisma, err := o.f.kullaniciCoz(ctx, saglayiciApple, iddiaKur("yeni-sub", "abc"+gizliAktarmaSoneki, true))
		if err != nil {
			t.Fatalf("kullaniciCoz: %v", err)
		}
		if ku != nil {
			t.Fatalf("giris yapildi (%s): gizli aktarma adresi baglama icin kullanilamaz", ku.ID)
		}
		if cakisma == nil {
			t.Fatal("cakisma bekleniyordu")
		}
		if cakisma.SifreliHesap {
			t.Error("SifreliHesap = true: gizli aktarma adresi BAGLAMA sayfasina goturulmemeli")
		}
		if cakisma.BeklemeID != "" {
			t.Error("gizli aktarma adresi icin bekleme kaydi acilmamali")
		}
		if _, err := o.federe.KimlikleKullanici(ctx, saglayiciApple, "yeni-sub"); err == nil {
			t.Error("kimlik baglandi: gizli aktarma adresinde baglama YAPILMAMALI")
		}
	})

	t.Run("adres kayitli degil: dogrudan yeni hesap", func(t *testing.T) {
		o := testFedereSayfalar(t)
		ctx := context.Background()
		ku, cakisma, err := o.f.kullaniciCoz(ctx, saglayiciApple, iddiaKur("yeni-sub", "def"+gizliAktarmaSoneki, true))
		if err != nil {
			t.Fatalf("kullaniciCoz: %v", err)
		}
		if cakisma != nil {
			t.Fatalf("cakisma dondu: %+v", cakisma)
		}
		if ku == nil {
			t.Fatal("yeni hesap acilmadi")
		}
		if ku.PasswordHash != "" {
			t.Error("federe hesabin sifresi olmamali")
		}
	})
}

// 4. Sifreli hesap: baglama sayfasi.
func TestEpostaCakismasiSifreliHesapBaglamayaGoturur(t *testing.T) {
	o := testFedereSayfalar(t)
	ctx := context.Background()
	mevcut, err := o.kullanici.Create(ctx, "ortak@ornek.com", "Mevcut", testSifre)
	if err != nil {
		t.Fatalf("hesap olusturulamadi: %v", err)
	}

	ku, cakisma, err := o.f.kullaniciCoz(ctx, saglayiciGoogle, iddiaKur("yeni-sub", "ortak@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if ku != nil {
		t.Fatalf("giris yapildi (%s): sifreli hesapta otomatik baglama YOK", ku.ID)
	}
	if cakisma == nil {
		t.Fatal("cakisma bekleniyordu")
	}
	if !cakisma.SifreliHesap {
		t.Error("SifreliHesap = false, beklenen true")
	}
	if cakisma.BeklemeID == "" {
		t.Error("BeklemeID bos")
	}
	if cakisma.Eposta != "ortak@ornek.com" {
		t.Errorf("Eposta = %q", cakisma.Eposta)
	}
	if _, err := o.federe.KimlikleKullanici(ctx, saglayiciGoogle, "yeni-sub"); err == nil {
		t.Error("kimlik baglandi: sifre sorulmadan baglama YAPILMAMALI")
	}
	if _, err := o.kullanici.ByID(ctx, mevcut.ID); err != nil {
		t.Errorf("mevcut hesap bozuldu: %v", err)
	}
}

// 5. K1: sifresiz hesap -> 409, baglama YOK, yeni hesap YOK.
func TestEpostaCakismasiSifresizHesapEngellenir(t *testing.T) {
	o := testFedereSayfalar(t)
	ctx := context.Background()
	// Google ile acilmis SIFRESIZ hesap.
	mevcut, err := o.federe.FedereKullaniciOlustur(ctx, saglayiciGoogle, "google-sub", "ortak@ornek.com", "Mevcut", true)
	if err != nil {
		t.Fatalf("federe hesap olusturulamadi: %v", err)
	}

	// Apple'dan AYNI e-postayla gelinir.
	ku, cakisma, err := o.f.kullaniciCoz(ctx, saglayiciApple, iddiaKur("apple-sub", "ortak@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if ku != nil {
		t.Fatalf("giris yapildi (%s): K1 sifresiz hesapta baglama YOK", ku.ID)
	}
	if cakisma == nil {
		t.Fatal("cakisma bekleniyordu")
	}
	if cakisma.SifreliHesap {
		t.Error("SifreliHesap = true: sifresiz hesapta sorulacak sifre yok")
	}
	if !reflect.DeepEqual(cakisma.MevcutSaglayicilar, []string{saglayiciGoogle}) {
		t.Errorf("MevcutSaglayicilar = %v, beklenen [google]", cakisma.MevcutSaglayicilar)
	}
	if _, err := o.federe.KimlikleKullanici(ctx, saglayiciApple, "apple-sub"); err == nil {
		t.Error("kimlik baglandi: K1 baglama YASAKLAR")
	}
	// YENI KAYIT OLUSMAMALI: ayni e-posta hala ilk hesaba ait.
	ayni, err := o.kullanici.ByEmail(ctx, "ortak@ornek.com")
	if err != nil {
		t.Fatalf("mevcut hesap kayboldu: %v", err)
	}
	if ayni.ID != mevcut.ID {
		t.Errorf("e-posta %q hesabina gecti, beklenen %q (yeni hesap OLUSMAMALI)", ayni.ID, mevcut.ID)
	}
}

// Spec'in somut saldirisi: saldirgan kurbanin adresiyle sifreli hesap
// acar; kurban Google ile girer. OTOMATIK BAGLANMAMALI.
func TestSaldirganinOnceActigiHesabaSessizceBaglanmaz(t *testing.T) {
	o := testFedereSayfalar(t)
	ctx := context.Background()
	// users: kurban@ornek.com, sifre = SALDIRGANIN sifresi.
	saldirganin, err := o.kullanici.Create(ctx, "kurban@ornek.com", "Kurban", "saldirganin-sifresi")
	if err != nil {
		t.Fatalf("hesap olusturulamadi: %v", err)
	}

	// Google: email dogrulanmis olsa BILE baglanmamali -- testin asil
	// noktasi bu. E-posta dogrulamasini BIZ yapmadigimiz icin
	// email_verified bu hesabin kurbana ait oldugunu kanitlamaz.
	ku, cakisma, err := o.f.kullaniciCoz(ctx, saglayiciGoogle, iddiaKur("kurban-google-sub", "kurban@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if ku != nil {
		t.Fatalf("GIRIS YAPILDI (%s): sifresi saldirganda olan hesaba otomatik baglandi", ku.ID)
	}
	if cakisma == nil {
		t.Fatal("cakisma bekleniyordu")
	}
	if _, err := o.federe.KimlikleKullanici(ctx, saglayiciGoogle, "kurban-google-sub"); err == nil {
		t.Errorf("kimlik %s hesabina SESSIZCE baglandi", saldirganin.ID)
	}
}

// ---------------------------------------------------------------------
// Step 2: Gizli aktarma + e-posta tekilligi cakismasi
// ---------------------------------------------------------------------

// Gizli aktarma adresi 2. adimda dogrudan yeni hesaba gider, ama
// UNIQUE (lower(email)) yuzunden adres zaten varsa INSERT
// ErrEpostaKullanimda doner. Karar: bu durumda da 409 -- asla baglama.
func TestGizliAktarmaAdresiZatenVarsa409(t *testing.T) {
	o := testFedereSayfalar(t)
	ctx := context.Background()
	relay := "abc" + gizliAktarmaSoneki
	// Adres BASKA bir sub ile zaten kayitli (kullanici hesabini silip
	// tekrar kaydolmus olabilir; Apple relay adresi ayni kalir).
	if _, err := o.federe.FedereKullaniciOlustur(ctx, saglayiciApple, "eski-sub", relay, "Eski", true); err != nil {
		t.Fatalf("ilk hesap olusturulamadi: %v", err)
	}
	o.apple.ayarla(func(s *sahteSaglayici) {
		s.sub = "yeni-sub"
		s.eposta = relay
	})

	w := o.akisKos(t, saglayiciApple, true)
	if w.Code != http.StatusConflict {
		t.Fatalf("durum = %d, beklenen 409, govde = %q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<form") {
		t.Error("409 sayfasinda sifre formu var: baglama YOK demek")
	}
	if _, err := o.federe.KimlikleKullanici(ctx, saglayiciApple, "yeni-sub"); err == nil {
		t.Error("kimlik baglandi: baglama YOK")
	}
	if o.istekler.subject(o.authReqID) != "" {
		t.Error("istek tamamlandi: giris OLMAMALI")
	}
}

// ---------------------------------------------------------------------
// Step 3: Baglama sayfasi testleri
// ---------------------------------------------------------------------

// Faz 1'in C1 dersi: form, baglama YOKSA HIC RENDER EDILMEZ.
func TestBaglamaGetBaglamasizReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, _, cerez, _ := o.beklemeKur(t)

	durumlar := []struct {
		ad    string
		id    string
		cerez *http.Cookie
	}{
		{"beklemeID yok", "", cerez},
		{"uydurma beklemeID", "uydurma-bekleme-id", cerez},
		{"oturum cerezi yok", beklemeID, nil},
		{"baska bir istege bagli oturum", beklemeID, o.oturumBagla(t, "baska-istek")},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			w := o.baglamaGetIstek(d.id, d.cerez)
			if w.Code != http.StatusForbidden {
				t.Errorf("durum = %d, beklenen 403", w.Code)
			}
			if strings.Contains(w.Body.String(), "<form") {
				t.Errorf("baglamasiz istege FORM render edildi: %q", w.Body.String())
			}
		})
	}
}

func TestBaglamaPostCSRFYanlisReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, _, cerez, mevcut := o.beklemeKur(t)

	w := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID},
		csrfAlan:      {"yanlis-csrf"},
		"sifre":       {testSifre},
	}, cerez)
	if w.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", w.Code)
	}
	if _, err := o.federe.KimlikleKullanici(context.Background(), saglayiciGoogle, o.google.sub); err == nil {
		t.Error("CSRF yanlisken kimlik baglandi")
	}
	if o.istekler.subject(o.authReqID) == mevcut.ID {
		t.Error("CSRF yanlisken giris yapildi")
	}
}

func TestBaglamaPostDogruSifreBaglarVeGirisYapar(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, csrf, cerez, mevcut := o.beklemeKur(t)

	w := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID},
		csrfAlan:      {csrf},
		"sifre":       {testSifre},
	}, cerez)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302, govde = %q", w.Code, w.Body.String())
	}
	if konum := w.Header().Get("Location"); konum != o.beklenenCallback() {
		t.Errorf("Location = %q, beklenen %q", konum, o.beklenenCallback())
	}
	bagli, err := o.federe.KimlikleKullanici(context.Background(), saglayiciGoogle, o.google.sub)
	if err != nil {
		t.Fatalf("kimlik baglanmadi: %v", err)
	}
	if bagli.ID != mevcut.ID {
		t.Errorf("kimlik %q hesabina baglandi, beklenen %q", bagli.ID, mevcut.ID)
	}
	if s := o.istekler.subject(o.authReqID); s != mevcut.ID {
		t.Errorf("Subject = %q, beklenen %q", s, mevcut.ID)
	}
}

func TestBaglamaPostYanlisSifreBaglamaz(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, csrf, cerez, _ := o.beklemeKur(t)

	w := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID},
		csrfAlan:      {csrf},
		"sifre":       {"yanlis-sifre-degeri"},
	}, cerez)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("durum = %d, beklenen 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), hataGirisBasarisiz) {
		t.Errorf("govde hata metnini tasimiyor: %q", w.Body.String())
	}
	if _, err := o.federe.KimlikleKullanici(context.Background(), saglayiciGoogle, o.google.sub); err == nil {
		t.Error("yanlis sifreyle kimlik BAGLANDI")
	}
	if o.istekler.subject(o.authReqID) != "" {
		t.Error("yanlis sifreyle giris yapildi")
	}
}

// Baglama beklemesi TEK KULLANIMLIK olmali: dogru sifre girildikten
// sonra ayni BeklemeID ile ikinci bir baglama yapilamaz.
func TestBaglamaBeklemesiTekKullanimlik(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, csrf, cerez, _ := o.beklemeKur(t)
	form := url.Values{alanBeklemeID: {beklemeID}, csrfAlan: {csrf}, "sifre": {testSifre}}

	ilk := o.baglamaPostIstek(form, cerez)
	if ilk.Code != http.StatusFound {
		t.Fatalf("ilk POST durum = %d, beklenen 302, govde = %q", ilk.Code, ilk.Body.String())
	}
	ikinci := o.baglamaPostIstek(form, cerez)
	if ikinci.Code != http.StatusForbidden {
		t.Errorf("ikinci POST durum = %d, beklenen 403 (bekleme tek kullanimlik)", ikinci.Code)
	}
	// Form da artik acilmamali.
	if w := o.baglamaGetIstek(beklemeID, cerez); w.Code != http.StatusForbidden {
		t.Errorf("tuketilmis bekleme icin GET durum = %d, beklenen 403", w.Code)
	}
}

// Saldirgan BASKA birinin beklemesini kendi tarayicisinda
// tamamlayamamali.
func TestBaglamaBaskaTarayicidaTamamlanamaz(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, csrf, _, mevcut := o.beklemeKur(t)
	// Saldirganin tarayicisi: kendi akisina bagli bir oturum cerezi var
	// ama KURBANIN istegine bagli degil.
	saldirgan := o.oturumBagla(t, "saldirganin-istegi")

	w := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID},
		csrfAlan:      {csrf},
		"sifre":       {testSifre},
	}, saldirgan)
	if w.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", w.Code)
	}
	if _, err := o.federe.KimlikleKullanici(context.Background(), saglayiciGoogle, o.google.sub); err == nil {
		t.Error("baska tarayicida baglama TAMAMLANDI")
	}
	if o.istekler.subject(o.authReqID) == mevcut.ID {
		t.Error("baska tarayicida giris yapildi")
	}
}

// Sifre denemesi SINIRSIZ olmamali: baglama sayfasi, sifre deneme
// yuzeyinin IKINCISIDIR ve Faz 1'in giris limitinden ayri bir uctur.
func TestBaglamaHizLimitiUygulanir(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, csrf, cerez, _ := o.beklemeKur(t)

	// Ayri bir mux: ayni handler, ama bu kez hiz limitiyle sarili.
	hiz, err := NewHizLimit(o.depo.rdb, HizLimitAyar{
		GirisIPPerMin: 1, GirisEpostaPerSaat: 100, KayitIPPerSaat: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("hiz limiti kurulamadi: %v", err)
	}
	mux := http.NewServeMux()
	araci := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	o.f.BaglamaBagla(mux, araci, hiz)
	o.mux = mux

	form := url.Values{alanBeklemeID: {beklemeID}, csrfAlan: {csrf}, "sifre": {"yanlis-sifre-degeri"}}
	ilk := o.baglamaPostIstek(form, cerez)
	if ilk.Code == http.StatusTooManyRequests {
		t.Fatalf("ilk deneme 429 aldi; sinir yanlis kurulmus")
	}
	ikinci := o.baglamaPostIstek(form, cerez)
	if ikinci.Code != http.StatusTooManyRequests {
		t.Errorf("ikinci deneme durum = %d, beklenen 429", ikinci.Code)
	}

	// Sayac GIRIS ucundan AYRI olmali: giris butcesi tukenmemis olmali.
	ctx := context.Background()
	anahtarlar, err := o.depo.rdb.Keys(ctx, hizLimitOnEki+":*").Result()
	if err != nil && err != redis.Nil {
		t.Fatalf("anahtarlar okunamadi: %v", err)
	}
	var baglamaVar bool
	for _, a := range anahtarlar {
		if strings.Contains(a, ":baglama:") {
			baglamaVar = true
		}
		if strings.Contains(a, ":giris:") {
			t.Errorf("baglama denemesi GIRIS sayacini tuketti: %q", a)
		}
	}
	if !baglamaVar {
		t.Errorf("baglama sayaci olusmadi; anahtarlar: %v", anahtarlar)
	}
}

// Sifre denemeleri HESAP BASINA da sinirli olmali: IP sayacini IP
// degistirerek asan bir saldirgan, her denemede yeni bir bekleme kaydi
// almak icin federasyon turunu tekrarlayabilir. Tek bir hesaba yonelen
// deneme sayisini sinirlayan sey hesap sayacidir.
func TestBaglamaHesapHizLimitiUygulanir(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, csrf, cerez, _ := o.beklemeKur(t)

	// IP siniri BOL (100/dk), hesap siniri 1/saat: reddin TEK sebebi
	// hesap sayaci olmali.
	hiz, err := NewHizLimit(o.depo.rdb, HizLimitAyar{
		GirisIPPerMin: 100, GirisEpostaPerSaat: 1, KayitIPPerSaat: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("hiz limiti kurulamadi: %v", err)
	}
	mux := http.NewServeMux()
	araci := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	o.f.Bagla(mux, araci, hiz)
	o.f.BaglamaBagla(mux, araci, hiz)
	o.mux = mux

	ilk := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID}, csrfAlan: {csrf}, "sifre": {"yanlis-sifre-degeri"},
	}, cerez)
	if ilk.Code != http.StatusUnauthorized {
		t.Fatalf("ilk deneme durum = %d, beklenen 401", ilk.Code)
	}

	// Saldirgan YENI bir bekleme kaydi alir (federasyon turunu
	// tekrarlar): kaydin tekilligi deneme sayisini sinirlamaz.
	ikinciAkis := o.akisKos(t, saglayiciGoogle, false)
	if ikinciAkis.Code != http.StatusFound {
		t.Fatalf("ikinci akis durum = %d, beklenen 302", ikinciAkis.Code)
	}
	konum, err := url.Parse(ikinciAkis.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location cozumlenemedi: %v", err)
	}
	yeniID := konum.Query().Get(alanBeklemeID)
	if yeniID == "" || yeniID == beklemeID {
		t.Fatalf("yeni bekleme kaydi alinamadi: %q", yeniID)
	}
	yeniForm := o.baglamaGetIstek(yeniID, cerez)
	if yeniForm.Code != http.StatusOK {
		t.Fatalf("yeni form acilamadi: durum = %d", yeniForm.Code)
	}
	esler := csrfDeseni.FindStringSubmatch(yeniForm.Body.String())
	if esler == nil {
		t.Fatal("yeni formda CSRF jetonu yok")
	}

	ikinci := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {yeniID}, csrfAlan: {esler[1]}, "sifre": {"yanlis-sifre-degeri"},
	}, cerez)
	if ikinci.Code != http.StatusTooManyRequests {
		t.Errorf("ikinci deneme durum = %d, beklenen 429 (hesap sayaci)", ikinci.Code)
	}

	anahtarlar, err := o.depo.rdb.Keys(context.Background(), hizLimitOnEki+":*").Result()
	if err != nil {
		t.Fatalf("anahtarlar okunamadi: %v", err)
	}
	var hesapVar bool
	for _, a := range anahtarlar {
		if strings.Contains(a, ":baglama-hesap:") {
			hesapVar = true
		}
	}
	if !hesapVar {
		t.Errorf("hesap sayaci olusmadi; anahtarlar: %v", anahtarlar)
	}
	// Hesap kimligi Redis anahtarinda DUZ METIN gecmemeli.
	for _, a := range anahtarlar {
		if strings.Contains(a, o.google.eposta) {
			t.Errorf("sayac anahtari e-postayi duz metin tasiyor: %q", a)
		}
	}
}

// Bekleme kaydi, DOGRULAMALARDAN ONCE tuketilmemeli: beklemeID'yi
// ogrenen ucuncu bir taraf, cerezsiz tek bir POST ile kurbanin bekleyen
// baglamasini YOK EDEMEMELI (yoksa kurban tum federasyon turunu bastan
// yapmak zorunda kalirdi).
func TestBaglamaBeklemesiBaglamasizPostlaYokEdilemez(t *testing.T) {
	o := testFedereSayfalar(t)
	beklemeID, csrf, cerez, mevcut := o.beklemeKur(t)

	// Saldirgan: cerez YOK, CSRF YOK.
	saldiri := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID}, "sifre": {"herhangi"},
	}, nil)
	if saldiri.Code != http.StatusForbidden {
		t.Fatalf("saldiri durum = %d, beklenen 403", saldiri.Code)
	}

	// Kurban kendi akisini HALA tamamlayabilmeli.
	w := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID}, csrfAlan: {csrf}, "sifre": {testSifre},
	}, cerez)
	if w.Code != http.StatusFound {
		t.Fatalf("kurbanin beklemesi yok edildi: durum = %d, govde = %q", w.Code, w.Body.String())
	}
	if s := o.istekler.subject(o.authReqID); s != mevcut.ID {
		t.Errorf("Subject = %q, beklenen %q", s, mevcut.ID)
	}
}

// Sifresiz hesap baglama sayfasina HIC gelmemeli; gelirse 409.
func TestBaglamaSifresizHesapIcinAcilmaz(t *testing.T) {
	o := testFedereSayfalar(t)
	ctx := context.Background()
	// kullaniciCoz bu kaydi uretmez (K1 dali 409 doner); kayit ELLE
	// yazilir, yani bu bir derinlemesine savunma testidir.
	sifresiz, err := o.federe.FedereKullaniciOlustur(ctx, saglayiciGoogle, "google-sub", "sifresiz@ornek.com", "Sifresiz", true)
	if err != nil {
		t.Fatalf("federe hesap olusturulamadi: %v", err)
	}
	beklemeID, err := rasgeleJeton()
	if err != nil {
		t.Fatalf("id uretilemedi: %v", err)
	}
	csrf, err := rasgeleJeton()
	if err != nil {
		t.Fatalf("csrf uretilemedi: %v", err)
	}
	bekleme := BaglamaBekleme{
		AuthRequestID: o.authReqID,
		UserID:        sifresiz.ID,
		Saglayici:     saglayiciApple,
		Sub:           "apple-sub",
		Eposta:        "sifresiz@ornek.com",
		CSRF:          csrf,
	}
	if err := o.f.durum.BeklemeYaz(ctx, beklemeID, bekleme, baglamaBeklemeTTL); err != nil {
		t.Fatalf("bekleme yazilamadi: %v", err)
	}
	cerez := o.oturumCerez

	w := o.baglamaPostIstek(url.Values{
		alanBeklemeID: {beklemeID},
		csrfAlan:      {csrf},
		"sifre":       {testSifre},
	}, cerez)
	if w.Code != http.StatusConflict {
		t.Errorf("durum = %d, beklenen 409", w.Code)
	}
	if _, err := o.federe.KimlikleKullanici(ctx, saglayiciApple, "apple-sub"); err == nil {
		t.Error("sifresiz hesaba baglama yapildi")
	}
	if o.istekler.subject(o.authReqID) != "" {
		t.Error("sifresiz hesapta giris yapildi")
	}
}
