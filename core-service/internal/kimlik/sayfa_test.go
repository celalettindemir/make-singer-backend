package kimlik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testSayfalar(t *testing.T) (*Sayfalar, UserStore) {
	t.Helper()
	kullanici := NewSahteUserStore()
	// Gercek geri cagirma op.AuthCallbackURL'den gelir; testte sabit bir
	// adres yeterli, cunku olculen sey giris mantigi.
	geri := func(_ context.Context, id string) string { return "/bitti?id=" + id }
	return NewSayfalar(kullanici, nil, geri), kullanici
}

func TestGirisSayfasiFormGosterir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, yolGiris+"?authRequestID=abc", nil)
	s.Giris(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	govde := w.Body.String()
	for _, beklenen := range []string{`name="eposta"`, `name="sifre"`, `name="authRequestID"`, "abc"} {
		if !strings.Contains(govde, beklenen) {
			t.Errorf("sayfa %q icermiyor", beklenen)
		}
	}
}

// authRequestID olmadan giris sayfasi anlamsizdir; form gosterilmemeli.
func TestGirisAuthRequestIDsizReddedilir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	s.Giris(w, httptest.NewRequest(http.MethodGet, yolGiris, nil))
	if w.Code == http.StatusOK {
		t.Error("authRequestID olmadan 200 dondu")
	}
}

// Yanlis sifre ile giriste hata mesaji, hesabin var olup olmadigini
// SOYLEMEMELI: aksi halde form bir e-posta sayim araci olur.
func TestYanlisGirisKullaniciVarligiSizdirmaz(t *testing.T) {
	s, kullanici := testSayfalar(t)
	if _, err := kullanici.Create(t.Context(), "var@ornek.com", "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	govdeler := map[string]string{}
	for ad, form := range map[string]url.Values{
		"var olan, yanlis sifre": {"eposta": {"var@ornek.com"}, "sifre": {"yanlis"}, "authRequestID": {"abc"}},
		"olmayan hesap":          {"eposta": {"yok@ornek.com"}, "sifre": {"herhangi"}, "authRequestID": {"abc"}},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, yolGiris, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		s.Giris(w, r)
		govdeler[ad] = w.Body.String()
	}
	if govdeler["var olan, yanlis sifre"] != govdeler["olmayan hesap"] {
		t.Error("iki hata yaniti farkli; hesabin varligi sizdiriliyor")
	}
}

// Sayfa kullanici girdisini kacirmali; sablon html/template ile
// uretildigi icin bu otomatiktir, ama regresyona karsi test edilir.
func TestSayfaGirdiKacirir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	kotu := `"><script>alert(1)</script>`
	r := httptest.NewRequest(http.MethodGet, yolGiris+"?authRequestID="+url.QueryEscape(kotu), nil)
	s.Giris(w, r)
	if strings.Contains(w.Body.String(), "<script>") {
		t.Error("girdi kacirilmamis, XSS mumkun")
	}
}

func TestKayitSayfasiFormGosterir(t *testing.T) {
	s, _ := testSayfalar(t)
	w := httptest.NewRecorder()
	s.Kayit(w, httptest.NewRequest(http.MethodGet, yolKayit+"?authRequestID=abc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d", w.Code)
	}
	for _, beklenen := range []string{`name="eposta"`, `name="sifre"`, `name="ad"`} {
		if !strings.Contains(w.Body.String(), beklenen) {
			t.Errorf("sayfa %q icermiyor", beklenen)
		}
	}
}

// Kullanilan e-posta ile kayit, kullaniciya anlasilir bir hata vermeli
// ama 500 olmamali.
func TestKullanilanEpostaIleKayit(t *testing.T) {
	s, kullanici := testSayfalar(t)
	if _, err := kullanici.Create(t.Context(), "var@ornek.com", "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	form := url.Values{
		"eposta": {"var@ornek.com"}, "ad": {"Yeni"},
		"sifre": {"baskaSifre12"}, "authRequestID": {"abc"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, yolKayit, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Kayit(w, r)
	if w.Code >= 500 {
		t.Errorf("durum = %d, 5xx olmamali", w.Code)
	}
}
