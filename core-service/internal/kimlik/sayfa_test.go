package kimlik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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

// Numaralandirma sizintisi, AYNI girdi icin hesap durumuna gore FARKLI
// cikti donmekten dogar. Bu yuzden burada IKI FARKLI e-posta degil,
// AYNI e-posta ile iki hesap DURUMU (hic kayitli degil / kayitli ama
// sifre yanlis) karsilastirilir. Cikti (durum kodu, govde, header'lar)
// birebir ayni olmali; aksi halde saldirgan hesabin var olup olmadigini
// cikarabilir.
func TestYanlisGirisKullaniciVarligiSizdirmaz(t *testing.T) {
	s, kullanici := testSayfalar(t)
	const eposta = "ayni@ornek.com"

	girisDene := func() *httptest.ResponseRecorder {
		form := url.Values{"eposta": {eposta}, "sifre": {"yanlis"}, "authRequestID": {"abc"}}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, yolGiris, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		s.Giris(w, r)
		return w
	}

	// Durum A: bu e-postayla hic hesap yok.
	a := girisDene()

	// Durum B: hesap var, ama "yanlis" sifresi gercek sifreden farkli;
	// yani bu dal da GIRIS BASARISIZ olan yolda kaliyor.
	if _, err := kullanici.Create(t.Context(), eposta, "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	b := girisDene()

	if a.Code != http.StatusUnauthorized {
		t.Fatalf("durum A = %d, beklenen %d (giris basarisiz olmali)", a.Code, http.StatusUnauthorized)
	}
	if a.Code != b.Code {
		t.Fatalf("durum kodlari farkli: A=%d B=%d", a.Code, b.Code)
	}
	if a.Body.String() != b.Body.String() {
		t.Error("iki govde farkli; hesabin varligi sizdiriliyor")
	}
	if !reflect.DeepEqual(a.Header(), b.Header()) {
		t.Error("iki yanitin header'lari farkli; hesabin varligi sizdiriliyor")
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

// Basarisiz giriste girilen e-posta forma geri yansitilir (numaralandirma
// SIZDIRMAZ, cunku saldirgan zaten kendi yazdigi degeri goruyor). Ancak bu
// echo XSS yuzeyi acar; html/template'in bunu kacisladigini burada
// dogrudan olcuyoruz.
func TestGirisEpostaEchoKacar(t *testing.T) {
	s, _ := testSayfalar(t)
	kotu := `"><script>alert(1)</script>`
	form := url.Values{"eposta": {kotu}, "sifre": {"yanlis"}, "authRequestID": {"abc"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, yolGiris, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Giris(w, r)

	govde := w.Body.String()
	if strings.Contains(govde, "<script>") {
		t.Error("ham <script> govdede bulundu, XSS mumkun")
	}
	if !strings.Contains(govde, "&lt;script&gt;") {
		t.Error("kacislanmis <script> govdede bulunamadi; echo hic gorunmuyor olabilir")
	}
	if !strings.Contains(govde, "&#34;") {
		t.Error("kacislanmis cift tirnak govdede bulunamadi")
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
