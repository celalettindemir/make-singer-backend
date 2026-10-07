package kimlik

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/auth"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// ---------------------------------------------------------------------
// Yardimcilar: sahte yukari akis saglayicisi (AG YOK, kendi RSA
// anahtarlarimiz). Testler hicbir zaman Apple/Google'a cikmaz.
// ---------------------------------------------------------------------

// fedKid, bir RSA anahtarindan deterministik bir "kid" uretir: ayni
// anahtar hem JWKS'te yayinlanirken hem token imzalanirken ayni kid'i
// tasimali, aksi halde dogrulayici dogru anahtari secemez.
func fedKid(anahtar *rsa.PrivateKey) string {
	toplam := sha256.Sum256(anahtar.N.Bytes())
	return base64.RawURLEncoding.EncodeToString(toplam[:8])
}

func fedRSAAnahtar(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	anahtar, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa anahtari uretilemedi: %v", err)
	}
	return anahtar
}

func fedJWK(anahtar *rsa.PrivateKey) map[string]any {
	pub := anahtar.PublicKey
	return map[string]any{
		"kty": "RSA",
		"kid": fedKid(anahtar),
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// sahteSaglayici, bir yukari akis saglayicisinin (Apple/Google) JWKS ve
// token uclarini taklit eder. Token ucunun uretecegi id_token'in her
// alani testten ayarlanabilir: saldiri testleri tam olarak bu alanlari
// bozar (nonce, aud, imza anahtari).
type sahteSaglayici struct {
	ad        string
	istemciID string
	sunucu    *httptest.Server

	// yayinAnahtar, JWKS'te YAYINLANAN anahtar; imzaAnahtar ise
	// token'i GERCEKTEN imzalayan anahtar. Ikisi farkli olursa imza
	// dogrulanamaz (gecersiz imza testi).
	yayinAnahtar *rsa.PrivateKey

	mu               sync.Mutex
	imzaAnahtar      *rsa.PrivateKey
	sub              string
	eposta           string
	isim             string
	epostaDogrulandi bool
	nonce            string
	audZorla         string
	issZorla         string
	tokenSayisi      int
	sonKod           string
	sonDogrulayici   string
	// uretilenTokenlar, token ucunun dondurdugu TUM id_token'lar:
	// loglarin hicbirinde gecmedigini dogrulamak icin tutulur.
	uretilenTokenlar []string
}

func yeniSahteSaglayici(t *testing.T, ad string) *sahteSaglayici {
	t.Helper()
	anahtar := fedRSAAnahtar(t)
	s := &sahteSaglayici{
		ad:               ad,
		istemciID:        ad + "-istemci",
		yayinAnahtar:     anahtar,
		imzaAnahtar:      anahtar,
		sub:              ad + "-sub-1",
		eposta:           ad + "@ornek.com",
		isim:             "Test Kullanici",
		epostaDogrulandi: true,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{fedJWK(s.yayinAnahtar)}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.tokenSayisi++
		s.sonKod = r.PostFormValue("code")
		s.sonDogrulayici = r.PostFormValue("code_verifier")
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": s.idToken(t)})
	})
	s.sunucu = httptest.NewServer(mux)
	t.Cleanup(s.sunucu.Close)
	return s
}

// idToken, saglayicinin token ucundan donecek id_token'i uretir.
func (s *sahteSaglayici) idToken(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	iss := s.sunucu.URL
	if s.issZorla != "" {
		iss = s.issZorla
	}
	aud := s.istemciID
	if s.audZorla != "" {
		aud = s.audZorla
	}
	simdi := time.Now()
	jeton := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":            iss,
		"aud":            aud,
		"sub":            s.sub,
		"email":          s.eposta,
		"email_verified": s.epostaDogrulandi,
		"name":           s.isim,
		"nonce":          s.nonce,
		"iat":            simdi.Unix(),
		"exp":            simdi.Add(time.Hour).Unix(),
	})
	jeton.Header["kid"] = fedKid(s.imzaAnahtar)
	imzali, err := jeton.SignedString(s.imzaAnahtar)
	if err != nil {
		t.Fatalf("id_token imzalanamadi: %v", err)
	}
	s.uretilenTokenlar = append(s.uretilenTokenlar, imzali)
	return imzali
}

func (s *sahteSaglayici) ayarla(f func(*sahteSaglayici)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// tokenlar, token ucunun dondurdugu id_token'larin kopyasini doner.
func (s *sahteSaglayici) tokenlar() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.uretilenTokenlar...)
}

func (s *sahteSaglayici) sayilar() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenSayisi
}

// sahteFedereIstekler, FedereIstekDepo'nun bellek ici karsiligi: hem
// bekleyen istegi okur (Baslat'in var olma kontrolu) hem tamamlama
// sozlesmesini uygular (ikinci cagri Subject'i EZMEZ).
type sahteFedereIstekler struct {
	mu       sync.Mutex
	istekler map[string]*AuthIstek
	baglanan map[string]string
	amrler   map[string][]string
}

func newSahteFedereIstekler() *sahteFedereIstekler {
	return &sahteFedereIstekler{
		istekler: map[string]*AuthIstek{},
		baglanan: map[string]string{},
		amrler:   map[string][]string{},
	}
}

func (s *sahteFedereIstekler) ekle(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.istekler[id] = &AuthIstek{ID: id, ClientID: "mobil"}
}

func (s *sahteFedereIstekler) IDileOku(_ context.Context, id string) (*AuthIstek, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	istek, varMi := s.istekler[id]
	if !varMi {
		return nil, ErrIstekYok
	}
	kopya := *istek
	return &kopya, nil
}

func (s *sahteFedereIstekler) TamamlandiIsaretle(_ context.Context, id string, userID string, amr []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, varMi := s.istekler[id]; !varMi {
		return ErrIstekYok
	}
	if mevcut, varMi := s.baglanan[id]; varMi {
		if mevcut == userID {
			return nil
		}
		return ErrIstekZatenTamamlandi
	}
	s.baglanan[id] = userID
	s.amrler[id] = append([]string(nil), amr...)
	return nil
}

func (s *sahteFedereIstekler) subject(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baglanan[id]
}

func (s *sahteFedereIstekler) amrOku(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.amrler[id]
}

// ---------------------------------------------------------------------
// Test ortami
// ---------------------------------------------------------------------

const fedTestIssuer = "https://op.ornek"

type fedOrtam struct {
	f         *FedereSayfalar
	mux       *http.ServeMux
	depo      *IstekDepo
	kullanici *SahteUserStore
	federe    *SahteFedereDepo
	oturumlar *SahteOturumDepo
	istekler  *sahteFedereIstekler
	google    *sahteSaglayici
	apple     *sahteSaglayici
	authReqID string

	// oturumCerez, authReqID'yi BU tarayiciya baglayan Faz 1 oturum
	// cerezi: /authorize bacaginin uretecegi cerezin karsiligi. Baslat
	// onu ZORUNLU kilar (bkz. C1).
	oturumCerez *http.Cookie
}

// testFedereSayfalar, federasyon uclarini GERCEK Redis (durum ve
// bekleme kayitlari), GERCEK id_token dogrulamasi (kendi RSA
// anahtarlarimiz) ve bellek ici depolarla kurar.
func testFedereSayfalar(t *testing.T) *fedOrtam {
	t.Helper()
	rdb := testRedis(t) // Redis yoksa burada atlanir
	depo := NewIstekDepo(rdb)
	kullanici := NewSahteUserStore()
	federe := NewSahteFedereDepo(kullanici)
	oturumlar := NewSahteOturumDepo()
	istekler := newSahteFedereIstekler()

	google := yeniSahteSaglayici(t, saglayiciGoogle)
	apple := yeniSahteSaglayici(t, saglayiciApple)
	appleP8, _ := testP8Uret(t)

	akislar := map[string]*YukariAkis{
		saglayiciGoogle: {
			Ad: saglayiciGoogle, IstemciID: google.istemciID, IstemciSir: "google-sir",
			AuthURL: google.sunucu.URL + "/authorize", TokenURL: google.sunucu.URL + "/token",
			JWKSURL: google.sunucu.URL + "/jwks", Issuer: google.sunucu.URL,
			Kapsamlar: []string{"openid", "email", "profile"}, PKCEDestekli: true,
		},
		saglayiciApple: {
			Ad: saglayiciApple, IstemciID: apple.istemciID,
			AuthURL: apple.sunucu.URL + "/authorize", TokenURL: apple.sunucu.URL + "/token",
			JWKSURL: apple.sunucu.URL + "/jwks", Issuer: apple.sunucu.URL,
			Kapsamlar: []string{"openid", "name", "email"}, FormPost: true,
			// Apple'in client_secret'i her degisimde ES256 ile uretilir
			// (K5), bu yuzden testte de GERCEK bir .p8 anahtari gerekir.
			AppleEkipID: "EKIP123", AppleAnahtarID: "ANAHTAR9", AppleAnahtarP8: appleP8,
		},
	}
	dogrulayicilar := map[string]*auth.YukariAkisDogrulayici{}
	for ad, s := range map[string]*sahteSaglayici{saglayiciGoogle: google, saglayiciApple: apple} {
		d, err := auth.NewYukariAkisDogrulayici(context.Background(), s.sunucu.URL+"/jwks", s.sunucu.URL, s.istemciID)
		if err != nil {
			t.Fatalf("%s dogrulayicisi kurulamadi: %v", ad, err)
		}
		dogrulayicilar[ad] = d
	}

	geri := func(_ context.Context, id string) string {
		return "/authorize/callback?" + url.Values{"id": {id}}.Encode()
	}
	// cerezGuvenli = true: issuer https, yani uretim davranisi.
	f := NewFedereSayfalar(akislar, dogrulayicilar, depo, federe, kullanici, istekler,
		oturumlar, geri, fedTestIssuer, true)
	f.istemci = &http.Client{Timeout: 5 * time.Second}

	mux := http.NewServeMux()
	araci := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	f.Bagla(mux, araci, nil)
	f.BaglamaBagla(mux, araci, nil)

	o := &fedOrtam{
		f: f, mux: mux, depo: depo, kullanici: kullanici, federe: federe,
		oturumlar: oturumlar, istekler: istekler, google: google, apple: apple,
		authReqID: "istek-1",
	}
	istekler.ekle(o.authReqID)
	o.oturumCerez = o.oturumBagla(t, o.authReqID)
	return o
}

// durumOku, Redis'teki state kaydini TUKETMEDEN okur (testin akisi
// bozmadan nonce'u ogrenmesi icin). Uretim yolu DurumAl'dir ve o GETDEL
// kullanir.
func (o *fedOrtam) durumOku(t *testing.T, state string) FedereDurum {
	t.Helper()
	veri, err := o.depo.rdb.Get(context.Background(), durumAnahtar(state)).Bytes()
	if err != nil {
		t.Fatalf("durum kaydi okunamadi: %v", err)
	}
	var d FedereDurum
	if err := json.Unmarshal(veri, &d); err != nil {
		t.Fatalf("durum kaydi cozumlenemedi: %v", err)
	}
	return d
}

// baslatHam, baslat ucunu VERILEN oturum cerezi ve istege gore cagirir.
// capraz=true ise Sec-Fetch-Site: cross-site gonderir.
func (o *fedOrtam) baslatHam(saglayici, authReqID string, cerez *http.Cookie, capraz bool) *httptest.ResponseRecorder {
	hedef := "/federasyon/" + saglayici + "/baslat?" + url.Values{"authRequestID": {authReqID}}.Encode()
	r := httptest.NewRequest(http.MethodGet, hedef, nil)
	if cerez != nil {
		r.AddCookie(cerez)
	}
	if capraz {
		r.Header.Set(baslikFetchSite, fetchSiteCapraz)
	}
	w := httptest.NewRecorder()
	o.mux.ServeHTTP(w, r)
	return w
}

// durumSayisi, Redis'te kac federasyon state kaydi oldugunu doner:
// "reddedilen bir baslatma HICBIR kayit yazmadi" iddiasinin kaniti.
func (o *fedOrtam) durumSayisi(t *testing.T) int {
	t.Helper()
	anahtarlar, err := o.depo.rdb.Keys(context.Background(), durumAnahtar("*")).Result()
	if err != nil {
		t.Fatalf("durum anahtarlari okunamadi: %v", err)
	}
	return len(anahtarlar)
}

// fedCerezVarMi, yanitta kimlik_fed cerezi olup olmadigini soyler.
func fedCerezVarMi(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == cerezAdFederasyon {
			return true
		}
	}
	return false
}

// baslat, GET /federasyon/<saglayici>/baslat cagirir ve (state, fed
// cerezi, yanit) doner. Istegi BASLATAN tarayicinin oturum cerezi
// GONDERILIR: Baslat onu zorunlu kilar (C1).
func (o *fedOrtam) baslat(t *testing.T, saglayici, authReqID string) (string, *http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	w := o.baslatHam(saglayici, authReqID, o.oturumCerez, false)
	if w.Code != http.StatusFound {
		return "", nil, w
	}
	konum, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location cozumlenemedi: %v", err)
	}
	state := konum.Query().Get("state")
	var cerez *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cerezAdFederasyon {
			cerez = c
		}
	}
	return state, cerez, w
}

// geri, geri donus ucunu cagirir. post=true ise Apple'in form_post
// donusunu (CAPRAZ-SITE POST) taklit eder.
func (o *fedOrtam) geri(saglayici, state, kod string, cerez *http.Cookie, post bool) *httptest.ResponseRecorder {
	yol := "/federasyon/" + saglayici + "/geri"
	deger := url.Values{"state": {state}, "code": {kod}}
	var r *http.Request
	if post {
		r = httptest.NewRequest(http.MethodPost, yol, strings.NewReader(deger.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// Apple'in form_post donusu capraz-sitedir (K3).
		r.Header.Set(baslikFetchSite, fetchSiteCapraz)
	} else {
		r = httptest.NewRequest(http.MethodGet, yol+"?"+deger.Encode(), nil)
	}
	if cerez != nil {
		r.AddCookie(cerez)
	}
	w := httptest.NewRecorder()
	o.mux.ServeHTTP(w, r)
	return w
}

// akisKos, mutlu yolun tamamini kostur: baslat -> (nonce'u sahte
// saglayiciya ver) -> geri. Donen yanit son adimin yanitidir.
func (o *fedOrtam) akisKos(t *testing.T, saglayici string, post bool) *httptest.ResponseRecorder {
	t.Helper()
	state, cerez, w := o.baslat(t, saglayici, o.authReqID)
	if state == "" {
		t.Fatalf("baslat basarisiz: durum = %d", w.Code)
	}
	d := o.durumOku(t, state)
	o.saglayiciAl(saglayici).ayarla(func(s *sahteSaglayici) { s.nonce = d.Nonce })
	return o.geri(saglayici, state, "kod-1", cerez, post)
}

func (o *fedOrtam) saglayiciAl(ad string) *sahteSaglayici {
	if ad == saglayiciApple {
		return o.apple
	}
	return o.google
}

// ---------------------------------------------------------------------
// Step 1: Baslatma testleri
// ---------------------------------------------------------------------

// Baslatma, istegi BASLATAN tarayiciya baglanmali: Faz 1'in C1
// (hesap devralma) acigini federasyon tarafindan yeniden acmamak icin.
func TestBaslatCerezKoyarVeDurumYazar(t *testing.T) {
	o := testFedereSayfalar(t)
	state, cerez, w := o.baslat(t, saglayiciGoogle, o.authReqID)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302", w.Code)
	}
	if state == "" {
		t.Fatal("Location'da state yok")
	}
	if cerez == nil || cerez.Value == "" {
		t.Fatal("federasyon cerezi konmadi")
	}
	konum, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location cozumlenemedi: %v", err)
	}
	if !strings.HasPrefix(w.Header().Get("Location"), o.google.sunucu.URL+"/authorize") {
		t.Errorf("Location yukari akis authorize adresi degil")
	}
	q := konum.Query()
	if q.Get("client_id") != o.google.istemciID {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("nonce") == "" {
		t.Error("nonce gonderilmedi")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, beklenen S256", q.Get("code_challenge_method"))
	}
	beklenenGeri := fedTestIssuer + "/federasyon/" + saglayiciGoogle + "/geri"
	if q.Get("redirect_uri") != beklenenGeri {
		t.Errorf("redirect_uri = %q, beklenen %q", q.Get("redirect_uri"), beklenenGeri)
	}

	d := o.durumOku(t, state)
	if d.Cerez != cerez.Value {
		t.Error("durum kaydindaki cerez degeri, yanittaki cerezle ayni degil")
	}
	if d.AuthRequestID != o.authReqID {
		t.Errorf("AuthRequestID = %q", d.AuthRequestID)
	}
	if d.Saglayici != saglayiciGoogle {
		t.Errorf("Saglayici = %q", d.Saglayici)
	}
	if d.Nonce != q.Get("nonce") {
		t.Error("kayitteki nonce, authorize URL'indeki nonce ile ayni degil")
	}
	if d.KodDogrulayici == "" {
		t.Error("kayitta kod dogrulayicisi yok")
	}
	// Dogrulayici URL'e DUZ METIN girmemeli; yalnizca ozeti gider.
	if strings.Contains(w.Header().Get("Location"), d.KodDogrulayici) {
		t.Error("kod dogrulayicisi authorize URL'ine duz metin kondu")
	}
}

// K2: Apple'in form_post donusu CROSS-SITE'tir ve SameSite=Lax cerez
// o POST'ta GONDERILMEZ. Bu yuzden federasyon cerezi None olmali --
// ama HttpOnly ve Secure'dan VAZGECILMEZ.
func TestFederasyonCereziSameSiteNone(t *testing.T) {
	o := testFedereSayfalar(t)
	_, cerez, _ := o.baslat(t, saglayiciApple, o.authReqID)
	if cerez == nil {
		t.Fatal("federasyon cerezi konmadi")
	}
	if cerez.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, beklenen None (K2: Apple form_post capraz-site POST'tur)", cerez.SameSite)
	}
	if !cerez.HttpOnly {
		t.Error("HttpOnly kapali")
	}
	if !cerez.Secure {
		t.Error("Secure kapali (issuer https)")
	}
	if cerez.Path != "/federasyon" {
		t.Errorf("Path = %q, beklenen /federasyon (oturum cerezini EZMEMELI)", cerez.Path)
	}
}

// Oturum cerezi DEGISMEMELI: SameSite=None'a cevirmek Faz 1'in
// login-CSRF korumasini zayiflatirdi.
func TestBaslatOturumCerezineDokunmaz(t *testing.T) {
	o := testFedereSayfalar(t)
	_, _, w := o.baslat(t, saglayiciGoogle, o.authReqID)
	for _, c := range w.Result().Cookies() {
		if c.Name == cerezAdOturum {
			t.Fatalf("baslat oturum cerezine dokundu: %+v", c)
		}
	}
}

func TestBaslatBilinmeyenSaglayici404(t *testing.T) {
	o := testFedereSayfalar(t)
	_, _, w := o.baslat(t, "facebook", o.authReqID)
	if w.Code != http.StatusNotFound {
		t.Errorf("durum = %d, beklenen 404", w.Code)
	}
}

func TestBaslatGecersizAuthRequestIDReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	// "var olmayan id" icin oturum baglamasi KURULUR: aksi halde test
	// 403'te durur ve "istek var mi" kontrolunu hic olcmez.
	yokCerez := o.oturumBagla(t, "yok-boyle-bir-istek")
	for _, d := range []struct {
		ad    string
		id    string
		cerez *http.Cookie
	}{
		{"bos id", "", o.oturumCerez},
		{"var olmayan id", "yok-boyle-bir-istek", yokCerez},
	} {
		t.Run(d.ad, func(t *testing.T) {
			w := o.baslatHam(saglayiciGoogle, d.id, d.cerez, false)
			if w.Code != http.StatusBadRequest {
				t.Errorf("durum = %d, beklenen 400", w.Code)
			}
			if fedCerezVarMi(w) {
				t.Error("gecersiz istekte federasyon cerezi konmamali")
			}
			if !strings.Contains(w.Body.String(), hataIstekGecersiz) {
				t.Errorf("govde = %q", w.Body.String())
			}
		})
	}
}

// C1: authRequestID TEK BASINA yetki girdisi DEGILDIR. Baslat, istegi
// /authorize'da BASLATAN tarayicinin oturum baglamasini ZORUNLU kilar.
// Aksi halde canli bir authRequestID'yi ogrenen biri federasyon
// bacagini KENDI tarayicisinda kosturup Geri'de istegi KENDI hesabina
// tamamlatabilir (Faz 1'in C1 hesap devralmasi, yeni bir kapidan).
func TestBaslatOturumBaglamasiZorunlu(t *testing.T) {
	for _, d := range []struct {
		ad    string
		cerez func(o *fedOrtam, t *testing.T) *http.Cookie
	}{
		{"oturum cerezi yok", func(*fedOrtam, *testing.T) *http.Cookie { return nil }},
		{"uydurma oturum cerezi", func(*fedOrtam, *testing.T) *http.Cookie {
			return &http.Cookie{Name: cerezAdOturum, Value: "saldirganin-uydurdugu-deger"}
		}},
		{"BASKA bir istege bagli oturum", func(o *fedOrtam, t *testing.T) *http.Cookie {
			return o.oturumBagla(t, "saldirganin-kendi-istegi")
		}},
	} {
		t.Run(d.ad, func(t *testing.T) {
			o := testFedereSayfalar(t)
			oncekiKayit := o.durumSayisi(t)

			w := o.baslatHam(saglayiciGoogle, o.authReqID, d.cerez(o, t), false)
			if w.Code != http.StatusForbidden {
				t.Errorf("durum = %d, beklenen 403", w.Code)
			}
			if fedCerezVarMi(w) {
				t.Error("baglamasiz istege federasyon cerezi KONMAMALI")
			}
			if sonra := o.durumSayisi(t); sonra != oncekiKayit {
				t.Errorf("state kaydi yazildi (%d -> %d); baglamasiz istek HICBIR kayit uretmemeli",
					oncekiKayit, sonra)
			}
			if w.Header().Get("Location") != "" {
				t.Errorf("yukari akisa yonlendirildi: %q", w.Header().Get("Location"))
			}
		})
	}
}

// C1 (b): K3'un muafiyeti YALNIZCA geri donus ucuna aittir. Baslat
// capraz-siteden cagrilamaz; AuthorizeSar'in capraz-site dalinda
// baglama KURMAMA karari ancak tamamlama yollarinin HEPSI baglama
// aradiginda ise yarar.
func TestBaslatCaprazSiteReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	oncekiKayit := o.durumSayisi(t)

	// Oturum cerezi GECERLI: reddin TEK sebebi capraz-site olmali.
	w := o.baslatHam(saglayiciGoogle, o.authReqID, o.oturumCerez, true)
	if w.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", w.Code)
	}
	if fedCerezVarMi(w) {
		t.Error("capraz-site istege federasyon cerezi KONMAMALI")
	}
	if sonra := o.durumSayisi(t); sonra != oncekiKayit {
		t.Errorf("state kaydi yazildi (%d -> %d)", oncekiKayit, sonra)
	}
}

// C1 (b)'nin degismezi: AuthorizeSar, /authorize'a capraz-siteden
// gelindiginde baglama KURMAZ ve "auth istegi olusur ama hicbir
// tarayici ona bagli olmadigi icin ... istek TAMAMLANAMAZ" varsayimina
// guvenir. Federasyon bacagi o varsayimi KIRMAMALI: baglamasiz bir
// istek federasyon uzerinden de tamamlanamaz.
func TestBaslatBaglamasizIstekFederasyonlaTamamlanamaz(t *testing.T) {
	o := testFedereSayfalar(t)
	// AuthorizeSar'in geride biraktigi durum: istek VAR, baglama YOK.
	baglamasiz := "baglamasiz-istek"
	o.istekler.ekle(baglamasiz)

	w := o.baslatHam(saglayiciGoogle, baglamasiz, nil, false)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, beklenen 403", w.Code)
	}
	// Tamamlanabilecek hicbir sey uretilmedi: state yok, cerez yok.
	if fedCerezVarMi(w) {
		t.Error("federasyon cerezi konmus")
	}
	if n := o.durumSayisi(t); n != 0 {
		t.Errorf("state kaydi sayisi = %d, beklenen 0", n)
	}
	// Ve istek HICBIR kullaniciya baglanmadi.
	if s := o.istekler.subject(baglamasiz); s != "" {
		t.Errorf("Subject = %q, beklenen bos (TamamlandiIsaretle'ye ULASILMAMALI)", s)
	}
}

// Yapilandirilmamis saglayici butonu hic gorunmedigi icin buraya
// gelinmemeli; gelinirse 404, "500" degil.
func TestBaslatYapilandirilmamisSaglayici404(t *testing.T) {
	o := testFedereSayfalar(t)
	// Apple yapilandirilmamis bir kurulumu taklit et.
	delete(o.f.akislar, saglayiciApple)
	_, _, w := o.baslat(t, saglayiciApple, o.authReqID)
	if w.Code != http.StatusNotFound {
		t.Errorf("durum = %d, beklenen 404", w.Code)
	}
}

// ---------------------------------------------------------------------
// Step 2: Geri donus testleri
// ---------------------------------------------------------------------

// beklenenCallback, mutlu yolda beklenen yonlendirme adresi.
func (o *fedOrtam) beklenenCallback() string {
	return "/authorize/callback?" + url.Values{"id": {o.authReqID}}.Encode()
}

// Mutlu yol: bilinen (saglayici, sub) -> o kullaniciya giris.
func TestGeriBilinenKimlikGirisYapar(t *testing.T) {
	o := testFedereSayfalar(t)
	mevcut, err := o.kullanici.Create(context.Background(), "bagli@ornek.com", "Bagli", "cokgizlisifre")
	if err != nil {
		t.Fatalf("kullanici olusturulamadi: %v", err)
	}
	if err := o.federe.Bagla(context.Background(), saglayiciGoogle, o.google.sub, mevcut.ID); err != nil {
		t.Fatalf("kimlik baglanamadi: %v", err)
	}

	w := o.akisKos(t, saglayiciGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302, govde = %q", w.Code, w.Body.String())
	}
	if konum := w.Header().Get("Location"); konum != o.beklenenCallback() {
		t.Errorf("Location = %q, beklenen %q", konum, o.beklenenCallback())
	}
	if s := o.istekler.subject(o.authReqID); s != mevcut.ID {
		t.Errorf("Subject = %q, beklenen %q", s, mevcut.ID)
	}
	if got := o.istekler.amrOku(o.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, beklenen [fed]", got)
	}
	if o.google.sayilar() != 1 {
		t.Errorf("token ucu %d kez cagrildi, beklenen 1", o.google.sayilar())
	}
}

// Yeni kimlik -> hesap olusturulur ve giris olur.
func TestGeriYeniKimlikHesapAcar(t *testing.T) {
	o := testFedereSayfalar(t)
	o.google.ayarla(func(s *sahteSaglayici) { s.eposta = "yeni@ornek.com" })

	w := o.akisKos(t, saglayiciGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302, govde = %q", w.Code, w.Body.String())
	}
	yeni, err := o.kullanici.ByEmail(context.Background(), "yeni@ornek.com")
	if err != nil {
		t.Fatalf("yeni kullanici bulunamadi: %v", err)
	}
	if yeni.PasswordHash != "" {
		t.Error("federe hesabin sifresi olmamali")
	}
	bagli, err := o.federe.KimlikleKullanici(context.Background(), saglayiciGoogle, o.google.sub)
	if err != nil {
		t.Fatalf("federe kimlik yazilmamis: %v", err)
	}
	if bagli.ID != yeni.ID {
		t.Errorf("kimlik %q kullanicisina bagli, beklenen %q", bagli.ID, yeni.ID)
	}
	if s := o.istekler.subject(o.authReqID); s != yeni.ID {
		t.Errorf("Subject = %q, beklenen %q", s, yeni.ID)
	}
	if got := o.istekler.amrOku(o.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, beklenen [fed]", got)
	}
}

// Apple'in form_post donusu CAPRAZ-SITE bir POST'tur (K2/K3): hem POST
// kabul edilmeli hem de Sec-Fetch-Site: cross-site REDDEDILMEMELI.
func TestGeriCaprazSitePostKabulEdilir(t *testing.T) {
	o := testFedereSayfalar(t)
	w := o.akisKos(t, saglayiciApple, true)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302, govde = %q", w.Code, w.Body.String())
	}
	if o.istekler.subject(o.authReqID) == "" {
		t.Error("istek tamamlanmadi")
	}
}

// --- SALDIRI TESTLERI ---

// state yok veya bilinmiyor: akis durmali. Bu, callback'i tahmin edip
// kurbanin istegini tamamlamaya calisan saldirganin ilk denemesidir.
func TestGeriBilinmeyenStateReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	for _, d := range []struct {
		ad    string
		state string
	}{
		{"bos state", ""},
		{"uydurma state", "uydurma-state-degeri"},
	} {
		t.Run(d.ad, func(t *testing.T) {
			w := o.geri(saglayiciGoogle, d.state, "kod-1", &http.Cookie{Name: cerezAdFederasyon, Value: "herhangi"}, false)
			if w.Code != http.StatusForbidden {
				t.Errorf("durum = %d, beklenen 403", w.Code)
			}
			if o.istekler.subject(o.authReqID) != "" {
				t.Error("istek TAMAMLANMAMALI")
			}
			if o.google.sayilar() != 0 {
				t.Error("token degisimi HIC yapilmamali")
			}
		})
	}
}

// state TEK KULLANIMLIK: ayni callback iki kez oynatilamaz.
func TestGeriStateTekrariReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	o.google.ayarla(func(s *sahteSaglayici) { s.eposta = "tekrar@ornek.com" })

	state, cerez, w := o.baslat(t, saglayiciGoogle, o.authReqID)
	if state == "" {
		t.Fatalf("baslat basarisiz: durum = %d", w.Code)
	}
	d := o.durumOku(t, state)
	o.google.ayarla(func(s *sahteSaglayici) { s.nonce = d.Nonce })

	ilk := o.geri(saglayiciGoogle, state, "kod-1", cerez, false)
	if ilk.Code != http.StatusFound {
		t.Fatalf("ilk donus durum = %d, beklenen 302, govde = %q", ilk.Code, ilk.Body.String())
	}
	ikinci := o.geri(saglayiciGoogle, state, "kod-1", cerez, false)
	if ikinci.Code != http.StatusForbidden {
		t.Errorf("ikinci donus durum = %d, beklenen 403 (state tek kullanimlik)", ikinci.Code)
	}
	if o.google.sayilar() != 1 {
		t.Errorf("token ucu %d kez cagrildi, beklenen 1 (tekrar oynatma token degisimine ULASMAMALI)", o.google.sayilar())
	}
}

// K2'nin kanitlanmasi: state DOGRU ama cerez YOK/YANLIS ise
// reddedilmeli. Aksi halde saldirgan kendi yetkilendirmesinden aldigi
// gecerli bir state+code ciftini kurbanin tarayicisinda acarak
// kurbanin akisini kendi hesabina tamamlatabilir.
func TestGeriCerezEslesmezseReddedilir(t *testing.T) {
	for _, d := range []struct {
		ad        string
		cerezUret func(dogru *http.Cookie) *http.Cookie
	}{
		{"cerez yok", func(*http.Cookie) *http.Cookie { return nil }},
		{"yanlis cerez", func(*http.Cookie) *http.Cookie {
			return &http.Cookie{Name: cerezAdFederasyon, Value: "saldirganin-cerezi"}
		}},
		{"bos cerez", func(*http.Cookie) *http.Cookie {
			return &http.Cookie{Name: cerezAdFederasyon, Value: ""}
		}},
	} {
		t.Run(d.ad, func(t *testing.T) {
			o := testFedereSayfalar(t)
			// 1) Saldirgan kendi tarayicisinda gecerli bir state alir.
			state, dogruCerez, w := o.baslat(t, saglayiciGoogle, o.authReqID)
			if state == "" {
				t.Fatalf("baslat basarisiz: durum = %d", w.Code)
			}
			durum := o.durumOku(t, state)
			o.google.ayarla(func(s *sahteSaglayici) { s.nonce = durum.Nonce })

			// 2) Ayni state ile kurbanin tarayicisindan (cerez yok/yanlis) gelinir.
			yanit := o.geri(saglayiciGoogle, state, "kod-1", d.cerezUret(dogruCerez), false)
			if yanit.Code != http.StatusForbidden {
				t.Errorf("durum = %d, beklenen 403", yanit.Code)
			}
			if o.istekler.subject(o.authReqID) != "" {
				t.Error("istek TAMAMLANMAMALI (Subject bos kalmali)")
			}
			if o.google.sayilar() != 0 {
				t.Error("token degisimi HIC yapilmamali")
			}
		})
	}
}

// nonce: Task 2'deki DogrulaNonce'un akista gercekten cagrildiginin
// kaniti. Yukari akis BASKA bir nonce'lu id_token dondururse akis
// durmali.
func TestGeriNonceEslesmezseReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	state, cerez, w := o.baslat(t, saglayiciGoogle, o.authReqID)
	if state == "" {
		t.Fatalf("baslat basarisiz: durum = %d", w.Code)
	}
	// Saglayici BASKA bir nonce ile imzaliyor: saldirganin kendi
	// yetkilendirmesinden aldigi gecerli (imza/iss/aud dogru) bir
	// id_token'i kurbanin akisina enjekte etmesi senaryosu.
	o.google.ayarla(func(s *sahteSaglayici) { s.nonce = "saldirganin-nonce-degeri" })

	yanit := o.geri(saglayiciGoogle, state, "kod-1", cerez, false)
	if yanit.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", yanit.Code)
	}
	if o.istekler.subject(o.authReqID) != "" {
		t.Error("istek TAMAMLANMAMALI")
	}
	if _, err := o.kullanici.ByEmail(context.Background(), o.google.eposta); err == nil {
		t.Error("nonce eslesmezken hesap OLUSTURULMAMALI")
	}
}

// Imzasi gecersiz veya baska bir anahtarla imzalanmis id_token
// reddedilmeli.
func TestGeriGecersizImzaReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	// JWKS'te yayinlanmayan BASKA bir anahtarla imzala.
	o.google.ayarla(func(s *sahteSaglayici) { s.imzaAnahtar = fedRSAAnahtar(t) })

	w := o.akisKos(t, saglayiciGoogle, false)
	if w.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", w.Code)
	}
	if o.istekler.subject(o.authReqID) != "" {
		t.Error("istek TAMAMLANMAMALI")
	}
}

// Yukari akis aud'u BASKA bir istemciye verilmis bir id_token
// dondururse reddedilmeli (baska uygulamanin tokeniyle giris).
func TestGeriYanlisAudienceReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	o.google.ayarla(func(s *sahteSaglayici) { s.audZorla = "baska-uygulama" })

	w := o.akisKos(t, saglayiciGoogle, false)
	if w.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", w.Code)
	}
	if o.istekler.subject(o.authReqID) != "" {
		t.Error("istek TAMAMLANMAMALI")
	}
}

// authRequestID BASKA bir kullaniciya zaten tamamlanmissa
// ErrIstekZatenTamamlandi yolundan reddedilmeli (Faz 1'in kurali
// federasyonda da gecerli).
func TestGeriTamamlanmisIstekBaskaKullaniciyaReddedilir(t *testing.T) {
	o := testFedereSayfalar(t)
	if err := o.istekler.TamamlandiIsaretle(context.Background(), o.authReqID, "baska-kullanici", []string{amrFederasyon}); err != nil {
		t.Fatalf("on tamamlama: %v", err)
	}
	w := o.akisKos(t, saglayiciGoogle, false)
	if w.Code != http.StatusConflict {
		t.Errorf("durum = %d, beklenen 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), hataIstekBagli) {
		t.Errorf("govde = %q", w.Body.String())
	}
	if s := o.istekler.subject(o.authReqID); s != "baska-kullanici" {
		t.Errorf("Subject ezildi: %q", s)
	}
}

// Saglayici, YOLDAN degil state KAYDINDAN okunur. Aksi halde saldirgan
// Google'da baslatilmis bir akisi /federasyon/apple/geri'ye getirip
// dogrulayici secimini karistirabilir.
func TestGeriSaglayiciKayittanOkunur(t *testing.T) {
	o := testFedereSayfalar(t)
	o.google.ayarla(func(s *sahteSaglayici) { s.eposta = "kayittan@ornek.com" })

	state, cerez, w := o.baslat(t, saglayiciGoogle, o.authReqID)
	if state == "" {
		t.Fatalf("baslat basarisiz: durum = %d", w.Code)
	}
	d := o.durumOku(t, state)
	o.google.ayarla(func(s *sahteSaglayici) { s.nonce = d.Nonce })

	// Google'da baslatilmis akis, APPLE yolundan geri gelir.
	yanit := o.geri(saglayiciApple, state, "kod-1", cerez, false)
	if yanit.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302 (akis kayittaki GOOGLE ile surmeli), govde = %q",
			yanit.Code, yanit.Body.String())
	}
	if o.google.sayilar() != 1 {
		t.Errorf("google token ucu %d kez cagrildi, beklenen 1", o.google.sayilar())
	}
	if o.apple.sayilar() != 0 {
		t.Errorf("apple token ucu %d kez cagrildi, beklenen 0 (yol degeri kullanilmamali)", o.apple.sayilar())
	}
	bagli, err := o.federe.KimlikleKullanici(context.Background(), saglayiciGoogle, o.google.sub)
	if err != nil {
		t.Fatalf("kimlik GOOGLE olarak yazilmamis: %v", err)
	}
	if bagli.Email != "kayittan@ornek.com" {
		t.Errorf("e-posta = %q", bagli.Email)
	}
}

// Geri donus ucu kimlik dogrulamasizdir ve her istek bir Redis okumasi
// (ve gecerli state'te yukari akisa bir HTTP cagrisi) tetikler; sinir
// GIRIS ve BAGLAMA uclarindan AYRI bir sayacta tutulmali.
func TestGeriHizLimitiUygulanir(t *testing.T) {
	o := testFedereSayfalar(t)
	hiz, err := NewHizLimit(o.depo.rdb, HizLimitAyar{
		GirisIPPerMin: 1, GirisEpostaPerSaat: 100, KayitIPPerSaat: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("hiz limiti kurulamadi: %v", err)
	}
	mux := http.NewServeMux()
	araci := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	o.f.Bagla(mux, araci, hiz)
	o.mux = mux

	ilk := o.geri(saglayiciGoogle, "uydurma-state", "kod-1", nil, false)
	if ilk.Code == http.StatusTooManyRequests {
		t.Fatal("ilk deneme 429 aldi; sinir yanlis kurulmus")
	}
	ikinci := o.geri(saglayiciGoogle, "uydurma-state", "kod-1", nil, false)
	if ikinci.Code != http.StatusTooManyRequests {
		t.Errorf("ikinci deneme durum = %d, beklenen 429", ikinci.Code)
	}
	anahtarlar, err := o.depo.rdb.Keys(context.Background(), hizLimitOnEki+":*").Result()
	if err != nil {
		t.Fatalf("anahtarlar okunamadi: %v", err)
	}
	var geriVar bool
	for _, a := range anahtarlar {
		if strings.Contains(a, ":federasyon-geri:") {
			geriVar = true
		}
		if strings.Contains(a, ":giris:") || strings.Contains(a, ":baglama:") {
			t.Errorf("federasyon donusu baska bir ucun sayacini tuketti: %q", a)
		}
	}
	if !geriVar {
		t.Errorf("federasyon-geri sayaci olusmadi; anahtarlar: %v", anahtarlar)
	}
}

// Hicbir log satirinda state, kod, id_token, cerez veya e-posta
// GECMEMELI. Faz 1'de bu bir Critical'di.
func TestGeriLoglarSizdirmaz(t *testing.T) {
	o := testFedereSayfalar(t)
	o.google.ayarla(func(s *sahteSaglayici) { s.eposta = "sizmasin@ornek.com" })

	var tampon strings.Builder
	eskiCikti := log.Writer()
	eskiBayrak := log.Flags()
	log.SetOutput(&tampon)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(eskiCikti)
		log.SetFlags(eskiBayrak)
	})

	// 1) Mutlu yol.
	state, cerez, w := o.baslat(t, saglayiciGoogle, o.authReqID)
	if state == "" {
		t.Fatalf("baslat basarisiz: durum = %d", w.Code)
	}
	d := o.durumOku(t, state)
	o.google.ayarla(func(s *sahteSaglayici) { s.nonce = d.Nonce })
	o.geri(saglayiciGoogle, state, "gizli-kod-degeri", cerez, false)

	// 2) Hatali yollar: bilinmeyen state, cerez uyusmazligi, nonce
	//    uyusmazligi, tekrar oynatma.
	o.geri(saglayiciGoogle, "uydurma-state", "gizli-kod-degeri", cerez, false)
	o.geri(saglayiciGoogle, state, "gizli-kod-degeri", nil, false)
	state2, cerez2, _ := o.baslat(t, saglayiciGoogle, o.authReqID)
	o.google.ayarla(func(s *sahteSaglayici) { s.nonce = "yanlis-nonce" })
	o.geri(saglayiciGoogle, state2, "gizli-kod-degeri", cerez2, false)

	ciktilar := tampon.String()
	yasakli := map[string]string{
		"state":       state,
		"state-2":     state2,
		"kod":         "gizli-kod-degeri",
		"cerez":       cerez.Value,
		"cerez-2":     cerez2.Value,
		"nonce":       d.Nonce,
		"eposta":      "sizmasin@ornek.com",
		"istek id":    o.authReqID,
		"dogrulayici": d.KodDogrulayici,
	}
	for ad, deger := range yasakli {
		if deger == "" {
			continue
		}
		if strings.Contains(ciktilar, deger) {
			t.Errorf("log %s degerini sizdirdi; loglar: %q", ad, ciktilar)
		}
	}
	// id_token: hem mutlu yolda hem dogrulamanin DUSTUGU yolda uretildi;
	// hicbiri (ne tamami ne de bir parcasi) loga girmemeli.
	tokenlar := o.google.tokenlar()
	if len(tokenlar) == 0 {
		t.Fatal("hic id_token uretilmedi; test id_token sizmasini OLCMUYOR")
	}
	for i, jeton := range tokenlar {
		if strings.Contains(ciktilar, jeton) {
			t.Errorf("log %d. id_token'i sizdirdi; loglar: %q", i, ciktilar)
		}
		// Imza parcasi da aranir: kirpilmis bir token da sizmadir.
		for _, parca := range strings.Split(jeton, ".") {
			if len(parca) > 16 && strings.Contains(ciktilar, parca) {
				t.Errorf("log %d. id_token'in bir parcasini sizdirdi; loglar: %q", i, ciktilar)
			}
		}
	}
}
