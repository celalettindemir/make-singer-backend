package kimlik

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// sayimUserStore, UserStore cagrilarini sayar. "Reddedilen bir POST
// SIFRE DOGRULAMASINA HIC GITMEDI" iddiasini kanitlamak icin: sayac
// sifir kalmazsa reddin kullanici deposundan sonra oldugu anlasilir.
type sayimUserStore struct {
	ic      UserStore
	mu      sync.Mutex
	byEmail int
	create  int
}

func (s *sayimUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	s.mu.Lock()
	s.create++
	s.mu.Unlock()
	return s.ic.Create(ctx, email, name, password)
}

func (s *sayimUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	s.mu.Lock()
	s.byEmail++
	s.mu.Unlock()
	return s.ic.ByEmail(ctx, email)
}

// ByID sayilmaz: sayfa akisinda kullanilmiyor, dogrudan devredilir.
func (s *sayimUserStore) ByID(ctx context.Context, id string) (*User, error) {
	return s.ic.ByID(ctx, id)
}

func (s *sayimUserStore) sayilar() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byEmail, s.create
}

// sahteTamamlayici, IstekTamamlayici'nin bellek ici karsiligi. Hangi
// istegin hangi kullaniciya baglandigini kaydeder; TamamlandiIsaretle'nin
// "ikinci cagri Subject'i EZMEZ" sozlesmesini de birebir uygular (gercegi
// istek.go'da, Redis testinde olculur).
type sahteTamamlayici struct {
	mu       sync.Mutex
	baglanan map[string]string
	Hata     error
}

func newSahteTamamlayici() *sahteTamamlayici {
	return &sahteTamamlayici{baglanan: map[string]string{}}
}

func (t *sahteTamamlayici) TamamlandiIsaretle(_ context.Context, id string, userID string) error {
	if t.Hata != nil {
		return t.Hata
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if mevcut, varMi := t.baglanan[id]; varMi {
		if mevcut == userID {
			return nil
		}
		return ErrIstekZatenTamamlandi
	}
	t.baglanan[id] = userID
	return nil
}

func (t *sahteTamamlayici) subject(id string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.baglanan[id]
}

type sayfaTestOrtami struct {
	sayfalar   *Sayfalar
	kullanici  *sayimUserStore
	oturumlar  *SahteOturumDepo
	tamamlayan *sahteTamamlayici
}

func testSayfalar(t *testing.T) *sayfaTestOrtami {
	t.Helper()
	kullanici := &sayimUserStore{ic: NewSahteUserStore()}
	oturumlar := NewSahteOturumDepo()
	tamamlayan := newSahteTamamlayici()
	// Gercek geri cagirma op.AuthCallbackURL'den gelir; testte sabit bir
	// adres yeterli, cunku olculen sey giris mantigi.
	geri := func(_ context.Context, id string) string { return "/bitti?id=" + id }
	// cerezGuvenli=false: testte TLS yok, Secure cerez httptest'te de
	// tasinabilir ama yerel gelistirme davranisini taklit ediyoruz.
	return &sayfaTestOrtami{
		sayfalar:   NewSayfalar(kullanici, tamamlayan, oturumlar, geri, false),
		kullanici:  kullanici,
		oturumlar:  oturumlar,
		tamamlayan: tamamlayan,
	}
}

var csrfDeseni = regexp.MustCompile(`name="csrf" value="([^"]*)"`)

// baglamaKur, /authorize bacaginin yaptigi isi taklit eder: verilen
// authRequestID'yi CAGIRAN tarayiciya baglar ve cerezi doner.
//
// Uretimde bunu YALNIZCA /authorize yapar (Sayfalar.AuthorizeSar);
// GET /giris ve GET /kayit baglama MINTLEMEZ. Testler de o yoldan
// gecer: once baglama, sonra form.
func baglamaKur(t *testing.T, o *sayfaTestOrtami, id string, mevcut *http.Cookie) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	if mevcut != nil {
		r.AddCookie(mevcut)
	}
	if err := o.sayfalar.BaglamaKur(w, r, id); err != nil {
		t.Fatalf("BaglamaKur: %v", err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cerezAdOturum && c.Value != "" {
			return c
		}
	}
	t.Fatal("BaglamaKur oturum cerezi vermedi")
	return nil
}

// formAc, bir tarayicinin akisi /authorize ile baslatmasini ve ardindan
// GET ile formu acmasini taklit eder; (oturum cerezi, CSRF jetonu)
// doner.
func formAc(t *testing.T, o *sayfaTestOrtami, yol, id string) (*http.Cookie, string) {
	t.Helper()
	cerez := baglamaKur(t, o, id, nil)
	return cerez, formGoster(t, o, yol, id, cerez)
}

// formGoster, var olan bir cerezle GET formunu acar ve formdaki CSRF
// jetonunu doner.
func formGoster(t *testing.T, o *sayfaTestOrtami, yol, id string, cerez *http.Cookie) string {
	t.Helper()
	w := formIstek(o, yol, id, cerez)
	if w.Code != http.StatusOK {
		t.Fatalf("form acilamadi: durum = %d", w.Code)
	}
	esler := csrfDeseni.FindStringSubmatch(w.Body.String())
	if esler == nil || esler[1] == "" {
		t.Fatal("formda CSRF jetonu yok")
	}
	return esler[1]
}

// formIstek, GET /giris veya GET /kayit'i verilen cerez (nil olabilir)
// ile cagirir.
func formIstek(o *sayfaTestOrtami, yol, id string, cerez *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, yol+"?authRequestID="+url.QueryEscape(id), nil)
	if cerez != nil {
		r.AddCookie(cerez)
	}
	if yol == yolGiris {
		o.sayfalar.Giris(w, r)
	} else {
		o.sayfalar.Kayit(w, r)
	}
	return w
}

// postEt, verilen cerez (nil olabilir) ile form POST'u yapar.
func postEt(o *sayfaTestOrtami, yol string, form url.Values, cerez *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, yol, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cerez != nil {
		r.AddCookie(cerez)
	}
	if yol == yolGiris {
		o.sayfalar.Giris(w, r)
	} else {
		o.sayfalar.Kayit(w, r)
	}
	return w
}

func TestGirisSayfasiFormGosterir(t *testing.T) {
	o := testSayfalar(t)
	cerez := baglamaKur(t, o, "abc", nil)
	w := formIstek(o, yolGiris, "abc", cerez)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	govde := w.Body.String()
	for _, beklenen := range []string{`name="eposta"`, `name="sifre"`, `name="authRequestID"`, `name="csrf"`, "abc"} {
		if !strings.Contains(govde, beklenen) {
			t.Errorf("sayfa %q icermiyor", beklenen)
		}
	}
}

// /authorize baglamayi kurarken tarayiciya HttpOnly + SameSite=Lax bir
// oturum cerezi vermeli; bu cerez akisin tek baglama noktasidir.
func TestAuthorizeOturumCereziVerir(t *testing.T) {
	o := testSayfalar(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/authorize", nil)
	if err := o.sayfalar.BaglamaKur(w, r, "abc"); err != nil {
		t.Fatalf("BaglamaKur: %v", err)
	}
	var cerez *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cerezAdOturum {
			cerez = c
		}
	}
	if cerez == nil {
		t.Fatal("oturum cerezi yok")
	}
	if !cerez.HttpOnly {
		t.Error("cerez HttpOnly degil")
	}
	if cerez.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, beklenen Lax", cerez.SameSite)
	}
	if cerez.Path != "/" {
		t.Errorf("Path = %q, beklenen /", cerez.Path)
	}
	// 32 bayt -> base64 raw url ile 43 karakter.
	if len(cerez.Value) < 43 {
		t.Errorf("cerez degeri %d karakter, 32 bayt entropi bekleniyordu", len(cerez.Value))
	}
	// Baglama depoya yazilmis olmali.
	baglama, err := o.oturumlar.OturumOku(context.Background(), cerez.Value)
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	csrf, bagliMi := baglama.CSRFBul("abc")
	if !bagliMi {
		t.Fatalf("authRequestID baglanmamis, kayit sayisi = %d", len(baglama.Kayitlar))
	}
	if csrf == "" {
		t.Error("baglamada CSRF jetonu yok")
	}
}

// C1 (re-review bulgusu): GET /giris ve GET /kayit baglama MINTLEMEZ.
// Baglamasiz (veya baska bir id'ye bagli cerezle) gelen GET'te form
// BILE gosterilmez. Bu, "saldirgan kendi tarayicisinda baglama alir,
// linki kurbana yollar" somurusunun kokunu kapatir.
func TestFormGetBaglamasizReddedilir(t *testing.T) {
	for _, yol := range []string{yolGiris, yolKayit} {
		t.Run(yol, func(t *testing.T) {
			o := testSayfalar(t)

			// (a) Hic cerez yok.
			w := formIstek(o, yol, "istek-A", nil)
			if w.Code != http.StatusForbidden {
				t.Errorf("cerezsiz GET durumu = %d, beklenen 403", w.Code)
			}
			if strings.Contains(w.Body.String(), `name="csrf"`) {
				t.Error("reddedilen GET formu gosterdi")
			}
			if len(w.Result().Cookies()) != 0 {
				t.Error("reddedilen GET cerez yazdi (baglama mintlendi)")
			}

			// (b) BASKA bir istege bagli cerez.
			cerezB := baglamaKur(t, o, "istek-B", nil)
			w = formIstek(o, yol, "istek-A", cerezB)
			if w.Code != http.StatusForbidden {
				t.Errorf("baska id'ye bagli cerezle GET durumu = %d, beklenen 403", w.Code)
			}
			if strings.Contains(w.Body.String(), `name="csrf"`) {
				t.Error("eslesmeyen cerezle form gosterildi")
			}

			// (c) Uydurma cerez degeri.
			w = formIstek(o, yol, "istek-A", &http.Cookie{Name: cerezAdOturum, Value: "uydurma"})
			if w.Code != http.StatusForbidden {
				t.Errorf("uydurma cerezle GET durumu = %d, beklenen 403", w.Code)
			}

			// (d) NEGATIF KONTROL: dogru baglama ile form GERCEKTEN
			//     acilir, yani yukaridaki 403'ler baglamadan kaynakli.
			cerezA := baglamaKur(t, o, "istek-A", nil)
			w = formIstek(o, yol, "istek-A", cerezA)
			if w.Code != http.StatusOK {
				t.Fatalf("bagli cerezle GET durumu = %d, beklenen 200", w.Code)
			}
			if !strings.Contains(w.Body.String(), `name="csrf"`) {
				t.Error("bagli cerezle form gosterilmedi")
			}
		})
	}
}

// PARALEL AKIS (birim duzeyi): ayni cerezle iki ayri auth istegi
// baslatilirsa IKISI de bagli kalmali. Baglama /authorize'a tasinirken
// "tek id" modeli korunsa ikinci /authorize birincisini ezer ve birinci
// sekme 403 olurdu. Ucan uca karsiligi akis_test.go'da.
func TestParalelIkiIstekAyniCerezeBaglanir(t *testing.T) {
	o := testSayfalar(t)
	cerez := baglamaKur(t, o, "istek-1", nil)
	cerez2 := baglamaKur(t, o, "istek-2", cerez)
	if cerez2.Value != cerez.Value {
		t.Fatalf("ikinci /authorize cerez degerini degistirdi: %q -> %q", cerez.Value, cerez2.Value)
	}
	baglama, err := o.oturumlar.OturumOku(context.Background(), cerez2.Value)
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	for _, id := range []string{"istek-1", "istek-2"} {
		if _, bagliMi := baglama.CSRFBul(id); !bagliMi {
			t.Errorf("%s baglamada yok", id)
		}
	}
	// Her iki sekmenin formu da acilabilmeli.
	for _, id := range []string{"istek-1", "istek-2"} {
		if w := formIstek(o, yolGiris, id, cerez2); w.Code != http.StatusOK {
			t.Errorf("%s formu durumu = %d, beklenen 200", id, w.Code)
		}
	}
}

// Kume SINIRSIZ buyumemeli (sisirme yuzeyi): ust siniri asinca EN ESKI
// kayit duser, en yeniler kalir.
func TestOturumBaglamaUstSiniriUygulanir(t *testing.T) {
	o := testSayfalar(t)
	var cerez *http.Cookie
	toplam := oturumBaglamaUstSinir + 3
	for i := 0; i < toplam; i++ {
		cerez = baglamaKur(t, o, "istek-"+strconv.Itoa(i), cerez)
	}
	baglama, err := o.oturumlar.OturumOku(context.Background(), cerez.Value)
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	if len(baglama.Kayitlar) != oturumBaglamaUstSinir {
		t.Fatalf("kayit sayisi = %d, beklenen %d", len(baglama.Kayitlar), oturumBaglamaUstSinir)
	}
	// En eski kayitlar dusmus, en yeni kalmis olmali.
	if _, bagliMi := baglama.CSRFBul("istek-0"); bagliMi {
		t.Error("ust sinir asildi ama en eski kayit dusmedi")
	}
	if _, bagliMi := baglama.CSRFBul("istek-" + strconv.Itoa(toplam-1)); !bagliMi {
		t.Error("en yeni kayit dusmus")
	}
}

// issuer https ise cerez Secure olmali; http:// yerel gelistirmede
// olmamali (aksi halde tarayici cerezi hic saklamaz ve akis kirilir).
func TestCerezSecureBayragiIssuerSemasinaBagli(t *testing.T) {
	for _, d := range []struct {
		ad      string
		guvenli bool
	}{{"https", true}, {"http-yerel", false}} {
		t.Run(d.ad, func(t *testing.T) {
			s := NewSayfalar(NewSahteUserStore(), newSahteTamamlayici(), NewSahteOturumDepo(),
				func(_ context.Context, id string) string { return "/bitti?id=" + id }, d.guvenli)
			w := httptest.NewRecorder()
			if err := s.BaglamaKur(w, httptest.NewRequest(http.MethodGet, "/authorize", nil), "abc"); err != nil {
				t.Fatalf("BaglamaKur: %v", err)
			}
			cerezler := w.Result().Cookies()
			if len(cerezler) == 0 {
				t.Fatal("cerez yok")
			}
			if cerezler[0].Secure != d.guvenli {
				t.Errorf("Secure = %v, beklenen %v", cerezler[0].Secure, d.guvenli)
			}
		})
	}
}

// authRequestID olmadan giris sayfasi anlamsizdir; form gosterilmemeli.
func TestGirisAuthRequestIDsizReddedilir(t *testing.T) {
	o := testSayfalar(t)
	w := httptest.NewRecorder()
	o.sayfalar.Giris(w, httptest.NewRequest(http.MethodGet, yolGiris, nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("durum = %d, beklenen 400 (authRequestID eksik)", w.Code)
	}
}

// C1: Cerez YOKSA POST /giris reddedilir ve kullanici deposuna HIC
// DOKUNULMAZ (yani sifre dogrulamasina gidilmez).
func TestGirisCerezsizReddedilirVeSifreDogrulanmaz(t *testing.T) {
	o := testSayfalar(t)
	if _, err := o.kullanici.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	onceByEmail, onceCreate := o.kullanici.sayilar()

	form := url.Values{
		"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
		"authRequestID": {"abc"}, csrfAlan: {"ne-olursa"},
	}
	w := postEt(o, yolGiris, form, nil)

	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403 (cerezsiz POST reddedilmeli)", w.Code)
	}
	sonByEmail, sonCreate := o.kullanici.sayilar()
	if sonByEmail != onceByEmail || sonCreate != onceCreate {
		t.Errorf("kullanici deposuna dokunuldu (ByEmail %d->%d, Create %d->%d); red sifre dogrulamasindan SONRA olmus",
			onceByEmail, sonByEmail, onceCreate, sonCreate)
	}
	if w.Header().Get("Location") != "" {
		t.Error("reddedilen POST yonlendirme dondu")
	}
}

// C1: BASKA bir tarayicinin cerezi + BASKA bir authRequestID ile POST
// reddedilir. Saldirgan kendi auth istegine ait cerezi tasiyip kurbanin
// istegini tamamlatamamali (ve tersi).
func TestGirisBaskaAuthRequestIDileReddedilir(t *testing.T) {
	o := testSayfalar(t)
	if _, err := o.kullanici.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Tarayici B "istek-B" icin bir oturum acar.
	cerezB, csrfB := formAc(t, o, yolGiris, "istek-B")
	onceByEmail, _ := o.kullanici.sayilar()

	// Ayni cerezle, BASKA bir auth istegini (istek-A) tamamlamaya calis.
	form := url.Values{
		"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
		"authRequestID": {"istek-A"}, csrfAlan: {csrfB},
	}
	w := postEt(o, yolGiris, form, cerezB)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403 (cereze bagli id ile form id'si eslesmiyor)", w.Code)
	}
	if sonByEmail, _ := o.kullanici.sayilar(); sonByEmail != onceByEmail {
		t.Error("eslesmeyen id'de sifre dogrulamasina gidildi")
	}
	if o.tamamlayan.subject("istek-A") != "" {
		t.Error("istek-A bir kullaniciya baglandi")
	}
}

// C1: Tanimsiz/uydurma bir cerez degeri de reddedilir (depoda baglama
// yok).
func TestGirisUydurmaCerezReddedilir(t *testing.T) {
	o := testSayfalar(t)
	form := url.Values{
		"eposta": {"a@ornek.com"}, "sifre": {"x"},
		"authRequestID": {"abc"}, csrfAlan: {"y"},
	}
	w := postEt(o, yolGiris, form, &http.Cookie{Name: cerezAdOturum, Value: "uydurma-deger"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
}

// C1: CSRF jetonu eksik veya yanlis ise reddedilir.
func TestGirisCSRFEksikVeYanlisReddedilir(t *testing.T) {
	o := testSayfalar(t)
	if _, err := o.kullanici.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cerez, csrf := formAc(t, o, yolGiris, "istek-1")

	for _, d := range []struct {
		ad    string
		jeton string
	}{
		{"eksik", ""},
		{"yanlis", csrf + "x"},
		{"baska-rasgele", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	} {
		t.Run(d.ad, func(t *testing.T) {
			onceByEmail, _ := o.kullanici.sayilar()
			form := url.Values{
				"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
				"authRequestID": {"istek-1"}, csrfAlan: {d.jeton},
			}
			w := postEt(o, yolGiris, form, cerez)
			if w.Code != http.StatusForbidden {
				t.Fatalf("durum = %d, beklenen 403", w.Code)
			}
			if sonByEmail, _ := o.kullanici.sayilar(); sonByEmail != onceByEmail {
				t.Error("CSRF reddi sifre dogrulamasindan sonra olmus")
			}
		})
	}
}

// C1 mutlu yol: gecerli cerez + eslesen authRequestID + dogru CSRF ile
// giris CALISIR ve akis geri cagirmaya doner.
func TestGirisGecerliCerezleCalisir(t *testing.T) {
	o := testSayfalar(t)
	kullanici, err := o.kullanici.Create(t.Context(), "kurban@ornek.com", "K", "dogruSifre12")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cerez, csrf := formAc(t, o, yolGiris, "istek-1")
	form := url.Values{
		"eposta": {"kurban@ornek.com"}, "sifre": {"dogruSifre12"},
		"authRequestID": {"istek-1"}, csrfAlan: {csrf},
	}
	w := postEt(o, yolGiris, form, cerez)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302. govde: %s", w.Code, w.Body.String())
	}
	if konum := w.Header().Get("Location"); konum != "/bitti?id=istek-1" {
		t.Errorf("Location = %q", konum)
	}
	if got := o.tamamlayan.subject("istek-1"); got != kullanici.ID {
		t.Errorf("istek baglanan subject = %q, beklenen kullanici ID", got)
	}
}

// Kayit da ayni baglamaya tabi.
func TestKayitCerezsizReddedilirVeKullaniciOlusmaz(t *testing.T) {
	o := testSayfalar(t)
	form := url.Values{
		"eposta": {"yeni@ornek.com"}, "ad": {"Yeni"},
		"sifre": {"uzunSifre123"}, "authRequestID": {"abc"}, csrfAlan: {"x"},
	}
	w := postEt(o, yolKayit, form, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
	if _, create := o.kullanici.sayilar(); create != 0 {
		t.Error("reddedilen kayit POST'unda kullanici olusturuldu")
	}
}

// C1 ek bulgu: AYNI authRequestID ile ikinci bir kayit, Subject'i
// EZMEMELI. Her POST kendi gecerli cerez/CSRF'ini tasisa bile bekleyen
// bir istek kod uretilene kadar farkli bir kimlige yeniden
// yonlendirilememeli.
func TestIkinciKayitSubjectEzmez(t *testing.T) {
	o := testSayfalar(t)
	const id = "istek-1"

	kayitOl := func(eposta string) *httptest.ResponseRecorder {
		cerez, csrf := formAc(t, o, yolKayit, id)
		form := url.Values{
			"eposta": {eposta}, "ad": {"K"}, "sifre": {"uzunSifre123"},
			"authRequestID": {id}, csrfAlan: {csrf},
		}
		return postEt(o, yolKayit, form, cerez)
	}

	if w := kayitOl("birinci@ornek.com"); w.Code != http.StatusFound {
		t.Fatalf("ilk kayit durumu = %d, beklenen 302. govde: %s", w.Code, w.Body.String())
	}
	ilkSubject := o.tamamlayan.subject(id)
	if ilkSubject == "" {
		t.Fatal("ilk kayit istegi baglamadi")
	}

	w := kayitOl("ikinci@ornek.com")
	if w.Code == http.StatusFound {
		t.Error("ikinci kayit 302 dondu; istek yeniden baglanabiliyor")
	}
	if son := o.tamamlayan.subject(id); son != ilkSubject {
		t.Errorf("Subject EZILDI: %q -> %q", ilkSubject, son)
	}
}

// Numaralandirma sizintisi, AYNI girdi icin hesap durumuna gore FARKLI
// cikti donmekten dogar. Bu yuzden burada IKI FARKLI e-posta degil,
// AYNI e-posta ile iki hesap DURUMU (hic kayitli degil / kayitli ama
// sifre yanlis) karsilastirilir. Cikti (durum kodu, govde, header'lar)
// birebir ayni olmali; aksi halde saldirgan hesabin var olup olmadigini
// cikarabilir. Iki deneme de AYNI gecerli cerez/CSRF'i tasir: aksi
// halde ikisi de 403'te durur ve test vacuous olurdu.
func TestYanlisGirisKullaniciVarligiSizdirmaz(t *testing.T) {
	o := testSayfalar(t)
	const eposta = "ayni@ornek.com"
	cerez, csrf := formAc(t, o, yolGiris, "istek-1")

	girisDene := func() *httptest.ResponseRecorder {
		form := url.Values{
			"eposta": {eposta}, "sifre": {"yanlis"},
			"authRequestID": {"istek-1"}, csrfAlan: {csrf},
		}
		return postEt(o, yolGiris, form, cerez)
	}

	// Durum A: bu e-postayla hic hesap yok.
	a := girisDene()

	// Durum B: hesap var, ama "yanlis" sifresi gercek sifreden farkli;
	// yani bu dal da GIRIS BASARISIZ olan yolda kaliyor.
	if _, err := o.kullanici.Create(t.Context(), eposta, "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	b := girisDene()

	if a.Code != http.StatusUnauthorized {
		t.Fatalf("durum A = %d, beklenen %d (giris basarisiz olmali, 403 ise cerez baglamasinda durmus)",
			a.Code, http.StatusUnauthorized)
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
	o := testSayfalar(t)
	kotu := `"><script>alert(1)</script>`
	// Baglama KURULMALI: aksi halde GET 403'te durur, govde hic
	// uretilmez ve test vacuous olur.
	cerez := baglamaKur(t, o, kotu, nil)
	w := formIstek(o, yolGiris, kotu, cerez)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200 (sayfa gercekten uretildi mi?)", w.Code)
	}
	if strings.Contains(w.Body.String(), "<script>") {
		t.Error("girdi kacirilmamis, XSS mumkun")
	}
	if !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Error("kacislanmis <script> govdede yok; girdi hic yansitilmiyor olabilir")
	}
}

// Basarisiz giriste girilen e-posta forma geri yansitilir (numaralandirma
// SIZDIRMAZ, cunku saldirgan zaten kendi yazdigi degeri goruyor). Ancak bu
// echo XSS yuzeyi acar; html/template'in bunu kacisladigini burada
// dogrudan olcuyoruz.
func TestGirisEpostaEchoKacar(t *testing.T) {
	o := testSayfalar(t)
	cerez, csrf := formAc(t, o, yolGiris, "istek-1")
	kotu := `"><script>alert(1)</script>`
	form := url.Values{
		"eposta": {kotu}, "sifre": {"yanlis"},
		"authRequestID": {"istek-1"}, csrfAlan: {csrf},
	}
	w := postEt(o, yolGiris, form, cerez)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("durum = %d, beklenen 401 (form gercekten islendi mi?)", w.Code)
	}

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
	o := testSayfalar(t)
	cerez := baglamaKur(t, o, "abc", nil)
	w := formIstek(o, yolKayit, "abc", cerez)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d", w.Code)
	}
	for _, beklenen := range []string{`name="eposta"`, `name="sifre"`, `name="ad"`, `name="csrf"`} {
		if !strings.Contains(w.Body.String(), beklenen) {
			t.Errorf("sayfa %q icermiyor", beklenen)
		}
	}
}

// Kullanilan e-posta ile kayit, kullaniciya anlasilir bir hata vermeli
// ama 500 olmamali.
func TestKullanilanEpostaIleKayit(t *testing.T) {
	o := testSayfalar(t)
	if _, err := o.kullanici.Create(t.Context(), "var@ornek.com", "V", "dogruSifre12"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cerez, csrf := formAc(t, o, yolKayit, "istek-1")
	form := url.Values{
		"eposta": {"var@ornek.com"}, "ad": {"Yeni"},
		"sifre": {"baskaSifre12"}, "authRequestID": {"istek-1"}, csrfAlan: {csrf},
	}
	w := postEt(o, yolKayit, form, cerez)
	if w.Code != http.StatusConflict {
		t.Errorf("durum = %d, beklenen 409 (e-posta kullanimda)", w.Code)
	}
}

// ---- /authorize/callback bacagi ----

// callbackIstek, /authorize/callback'i verilen cerez ile cagirir.
func callbackIstek(o *sayfaTestOrtami, id string, cerez *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/authorize/callback?id="+url.QueryEscape(id), nil)
	if cerez != nil {
		r.AddCookie(cerez)
	}
	o.sayfalar.Callback(w, r)
	return w
}

// delegeIzleyici, kutuphane handler'inin yerine gecer: cagrildi mi?
type delegeIzleyici struct{ cagrildi bool }

func (d *delegeIzleyici) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	d.cagrildi = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("delege"))
}

// C1: /authorize/callback cerezsiz REDDEDILIR ve kutuphane handler'ina
// HIC gidilmez (yani kod uretilmez).
func TestCallbackCerezsizReddedilir(t *testing.T) {
	o := testSayfalar(t)
	izleyici := &delegeIzleyici{}
	o.sayfalar.CallbackDelege(izleyici)

	w := callbackIstek(o, "istek-1", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
	if izleyici.cagrildi {
		t.Error("kutuphane callback handler'ina delege edildi")
	}
}

// C1: Baska bir auth istegine bagli cerezle callback REDDEDILIR.
func TestCallbackYanlisCerezReddedilir(t *testing.T) {
	o := testSayfalar(t)
	izleyici := &delegeIzleyici{}
	o.sayfalar.CallbackDelege(izleyici)
	cerez, _ := formAc(t, o, yolGiris, "istek-B")

	w := callbackIstek(o, "istek-A", cerez)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
	if izleyici.cagrildi {
		t.Error("eslesmeyen cerezle delege edildi")
	}
}

// C1 mutlu yol: dogru cerezle callback kutuphane handler'ina delege
// eder.
func TestCallbackDogruCerezleDelegeEder(t *testing.T) {
	o := testSayfalar(t)
	izleyici := &delegeIzleyici{}
	o.sayfalar.CallbackDelege(izleyici)
	cerez, _ := formAc(t, o, yolGiris, "istek-1")

	w := callbackIstek(o, "istek-1", cerez)
	if w.Code != http.StatusOK {
		t.Fatalf("durum = %d, beklenen 200", w.Code)
	}
	if !izleyici.cagrildi {
		t.Error("dogru cerezle delege edilmedi")
	}
}

// id parametresi olmadan callback anlamsizdir.
func TestCallbackIDsizReddedilir(t *testing.T) {
	o := testSayfalar(t)
	izleyici := &delegeIzleyici{}
	o.sayfalar.CallbackDelege(izleyici)
	w := httptest.NewRecorder()
	o.sayfalar.Callback(w, httptest.NewRequest(http.MethodGet, "/authorize/callback", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("durum = %d, beklenen 400", w.Code)
	}
	if izleyici.cagrildi {
		t.Error("id olmadan delege edildi")
	}
}

// GERCEK URETIM HATASI (I4a): kayit "strings.TrimSpace(email)"
// uyguluyor, giris ise form degerini HAM geciyordu. E-postasini bastaki
// veya sondaki boslukla yazan kullanici kayit oluyor (" x@y.com " ->
// "x@y.com" saklaniyor), sonra AYNI girdiyle giris yapmaya calistiginda
// bulunamiyor ve "E-posta veya sifre hatali" aliyordu: hesabi var ama o
// girdiyle asla giremiyordu.
//
// Bu test o yolu ucan uca olcer: bosluklu e-postayla KAYIT, ardindan
// hem BOSLUKLU hem BOSLUKSUZ girdiyle GIRIS.
func TestBoslukluEpostaIleKayitSonrasiGirisCalisir(t *testing.T) {
	const sifre = "gecerliSifre12"
	const bosluklu = "  bosluk@ornek.com  "
	const bosluksuz = "bosluk@ornek.com"

	o := testSayfalar(t)

	// Kayit: bosluklu girdi.
	cerez, csrf := formAc(t, o, yolKayit, "istek-kayit")
	w := postEt(o, yolKayit, url.Values{
		"authRequestID": {"istek-kayit"},
		csrfAlan:        {csrf},
		"eposta":        {bosluklu},
		"ad":            {"Bosluk Kullanici"},
		"sifre":         {sifre},
	}, cerez)
	if w.Code != http.StatusFound {
		t.Fatalf("kayit durumu = %d, beklenen 302 (govde: %s)", w.Code, w.Body.String())
	}

	// Saklanan deger normalize edilmis olmali.
	kayitli, err := o.kullanici.ByEmail(context.Background(), bosluksuz)
	if err != nil {
		t.Fatalf("kayitli kullanici bulunamadi: %v", err)
	}
	if kayitli.Email != bosluksuz {
		t.Errorf("saklanan e-posta = %q, beklenen %q", kayitli.Email, bosluksuz)
	}

	// Giris: her iki girdi de CALISMALI.
	for _, girdi := range []string{bosluklu, bosluksuz, "  BOSLUK@ORNEK.COM "} {
		t.Run("giris girdisi="+strconv.Quote(girdi), func(t *testing.T) {
			id := "istek-giris-" + girdi
			cerez, csrf := formAc(t, o, yolGiris, id)
			w := postEt(o, yolGiris, url.Values{
				"authRequestID": {id},
				csrfAlan:        {csrf},
				"eposta":        {girdi},
				"sifre":         {sifre},
			}, cerez)
			if w.Code != http.StatusFound {
				t.Errorf("giris durumu = %d, beklenen 302 (govde: %s)", w.Code, w.Body.String())
			}
		})
	}

	// NEGATIF KONTROL: duzeltme "her sifre gecer" haline gelmesin.
	// Yanlis sifre hala 401 almali, yani yukaridaki 302'ler
	// normalizasyon sayesinde, dogrulamanin gevsemesi sayesinde DEGIL.
	cerez, csrf = formAc(t, o, yolGiris, "istek-yanlis")
	w = postEt(o, yolGiris, url.Values{
		"authRequestID": {"istek-yanlis"},
		csrfAlan:        {csrf},
		"eposta":        {bosluklu},
		"sifre":         {"yanlisSifre12"},
	}, cerez)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("yanlis sifre durumu = %d, beklenen 401", w.Code)
	}
}
