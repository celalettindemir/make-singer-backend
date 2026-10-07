package kimlik

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/auth"
)

// Bu dosya, federasyon akisini UCTAN UCA kosturmak icin gereken test
// altyapisini tasir:
//
//  1. sahteOP: yukari akis saglayicisinin (Apple/Google) /authorize,
//     /token ve /jwks uclarini taklit eden, her yanitini BOZABILEN
//     yerel bir httptest sunucusu. Testler hicbir zaman gercek
//     accounts.google.com / appleid.apple.com adresine cikmaz.
//  2. cerezKavanozu: SameSite'i GERCEKTEN uygulayan bir cerez kabi.
//  3. fedTarayici: yonlendirmeleri ve Apple'in form_post formunu
//     gercek bir tarayici gibi takip eden, kendi cerez kabi olan
//     istemci.
//
// Testlerin kendisi federe_akis_test.go'dadir.

// ---------------------------------------------------------------------
// Sahte yukari akis OP'si
// ---------------------------------------------------------------------

// sahteOPAyar, yukari akis saglayicisinin her yanitini BOZABILMEK icin
// vardir: saldiri testleri tam olarak bir alani degistirip geri kalanini
// dogru tutmak zorunda, yoksa testin neyi olctugu belirsizlesir.
type sahteOPAyar struct {
	// Eposta BOS ise email claim'i HIC GONDERILMEZ (Apple'in ikinci
	// giriste e-postayi hic vermemesi senaryosu); bos string gonderen
	// bir claim ile claim'in hic olmamasini ayirmak gerekiyor.
	Eposta           string
	EpostaDogrulandi bool
	// EpostaDogrulandiString bos degilse email_verified STRING olarak
	// gonderilir (Apple'in K8'deki davranisi).
	EpostaDogrulandiString string
	Ad                     string
	Sub                    string
	// Asagidakiler BOZMA anahtarlaridir; bos/false ise dogru deger
	// kullanilir.
	YanlisNonce        string
	YanlisAudience     string
	YanlisIssuer       string
	YanlisImzaAnahtari bool
	TokenDurumKodu     int    // 0 ise 200
	TokenGovdesi       string // bos ise normal {"id_token":...}
	FormPost           bool   // Apple kipi: donuste POST
}

// sahteOP, tek bir yukari akis saglayicisini taklit eder.
//
// NEDEN yeniSahteSaglayici'dan AYRI BIR TIP: o yardimcinin /authorize
// ucu YOKTUR (birim testler state kaydini ve geri bacagini elle
// cagirir). Uctan uca akis ise tarayicinin GERCEKTEN saglayiciya gidip
// donmesini gerektirir: /authorize, kod uretimi ve Apple'in form_post
// donusu burada yasiyor. Birim testlerin yardimcisi BOZULMADAN
// birakildi (federe_sayfa_test.go onu kullanmaya devam ediyor).
type sahteOP struct {
	ad        string
	istemciID string
	sunucu    *httptest.Server

	// yayinAnahtar, JWKS'te YAYINLANAN anahtar; imzaAnahtar ise
	// id_token'i GERCEKTEN imzalayan anahtar. YanlisImzaAnahtari ile
	// ikisi ayrisir ve imza dogrulanamaz.
	yayinAnahtar *rsa.PrivateKey
	imzaAnahtar  *rsa.PrivateKey

	mu           sync.Mutex
	ayar         sahteOPAyar
	kodNonce     map[string]string
	sonAuthorize url.Values
	kodSayisi    int
	tokenSayisi  int
}

// otoFormIsaret, sahte saglayicinin form_post sayfasindaki formu
// isaretler. fedTarayici YALNIZCA bu isareti tasiyan formu kendiliginden
// gonderir: gercek tarayicida da otomatik gonderen sey saglayicinin
// sayfasindaki script'tir, /baglama gibi SIFRE isteyen formlar
// kendiliginden gonderilmez.
const otoFormIsaret = `data-oto="1"`

// sahteYukariAkisOP, verilen ad icin kendi RSA anahtari, kendi JWKS
// ucu, /authorize ve /token uclari olan bir httptest sunucusu kurar.
//
// ad parametresi brief'teki imzaya EKLENDI: uctan uca akislar Google
// (302 donus) ve Apple (form_post donus) saglayicilarinin IKISINI de
// ayni ortamda ister, yani saglayici adi ve istemci kimligi ayirt
// edilebilir olmak zorunda.
func sahteYukariAkisOP(t *testing.T, ad string, ayar sahteOPAyar) *sahteOP {
	t.Helper()
	yayin := fedRSAAnahtar(t)
	imza := yayin
	if ayar.YanlisImzaAnahtari {
		// JWKS'te yayinlanmayan BASKA bir anahtarla imzala.
		imza = fedRSAAnahtar(t)
	}
	s := &sahteOP{
		ad:           ad,
		istemciID:    ad + "-istemci",
		yayinAnahtar: yayin,
		imzaAnahtar:  imza,
		ayar:         ayar,
		kodNonce:     map[string]string{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{fedJWK(s.yayinAnahtar)},
		})
	})
	mux.HandleFunc("/authorize", s.authorize)
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) { s.token(t, w, r) })

	s.sunucu = httptest.NewServer(mux)
	t.Cleanup(s.sunucu.Close)
	return s
}

// authorize, kullanicinin saglayicida onay verdigi adimi taklit eder:
// istegi kaydeder, bir kod uretir ve kullaniciyi bizim geri donus
// adresimize gonderir. form_post kipinde (Apple) donus bir CAPRAZ-SITE
// POST'tur ve bu, K2'nin tum gerekcesidir.
func (s *sahteOP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	s.sonAuthorize = q
	s.kodSayisi++
	kod := fmt.Sprintf("%s-kod-%d", s.ad, s.kodSayisi)
	s.kodNonce[kod] = q.Get("nonce")
	// form_post ya ayarla zorlanir ya da istegin kendisinden okunur:
	// uretimde onu isteyen YukariAkis.FormPost'tur.
	formPost := s.ayar.FormPost || q.Get("response_mode") == "form_post"
	s.mu.Unlock()

	geri := q.Get("redirect_uri")
	durum := q.Get("state")
	if formPost {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Gercek Apple sayfasi da bu formu script ile kendiliginden
		// gonderir; isaret, tarayici yardimcisinin hangi formu
		// gonderecegini belirler.
		_, _ = fmt.Fprintf(w,
			`<!doctype html><html><body><form `+otoFormIsaret+` method="post" action="%s">`+
				`<input type="hidden" name="state" value="%s">`+
				`<input type="hidden" name="code" value="%s">`+
				`</form></body></html>`,
			geri, durum, kod)
		return
	}
	hedef, err := url.Parse(geri)
	if err != nil {
		http.Error(w, "redirect_uri cozulemedi", http.StatusBadRequest)
		return
	}
	sorgu := hedef.Query()
	sorgu.Set("code", kod)
	sorgu.Set("state", durum)
	hedef.RawQuery = sorgu.Encode()
	http.Redirect(w, r, hedef.String(), http.StatusFound)
}

// token, kodu id_token'a cevirir. Ayar ile durum kodu ve govde tumuyle
// bozulabilir: "200 + {\"error\":...}" gibi gercek durumlar da
// olculebilsin.
func (s *sahteOP) token(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	_ = r.ParseForm()
	s.mu.Lock()
	s.tokenSayisi++
	nonce := s.kodNonce[r.PostFormValue("code")]
	ayar := s.ayar
	s.mu.Unlock()

	if ayar.YanlisNonce != "" {
		nonce = ayar.YanlisNonce
	}
	w.Header().Set("Content-Type", "application/json")
	if ayar.TokenDurumKodu != 0 {
		w.WriteHeader(ayar.TokenDurumKodu)
	}
	if ayar.TokenGovdesi != "" {
		_, _ = io.WriteString(w, ayar.TokenGovdesi)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": s.idToken(t, nonce)})
}

// idToken, saglayicinin dondurecegi id_token'i uretir. Bozma anahtarlari
// TEK TEK uygulanir: bir saldiri testi yalnizca bir alani degistirir.
func (s *sahteOP) idToken(t *testing.T, nonce string) string {
	t.Helper()
	s.mu.Lock()
	ayar := s.ayar
	imza := s.imzaAnahtar
	s.mu.Unlock()

	iss := s.sunucu.URL
	if ayar.YanlisIssuer != "" {
		iss = ayar.YanlisIssuer
	}
	aud := s.istemciID
	if ayar.YanlisAudience != "" {
		aud = ayar.YanlisAudience
	}
	sub := ayar.Sub
	if sub == "" {
		sub = s.ad + "-sub-1"
	}
	simdi := time.Now()
	iddialar := jwt.MapClaims{
		"iss":   iss,
		"aud":   aud,
		"sub":   sub,
		"nonce": nonce,
		"iat":   simdi.Unix(),
		"exp":   simdi.Add(time.Hour).Unix(),
	}
	// Eposta BOS ise claim HIC EKLENMEZ: Apple ikinci giriste e-postayi
	// gondermez ve bu, bos string gondermekten FARKLI bir durumdur.
	if ayar.Eposta != "" {
		iddialar["email"] = ayar.Eposta
	}
	if ayar.Ad != "" {
		iddialar["name"] = ayar.Ad
	}
	switch {
	case ayar.EpostaDogrulandiString != "":
		iddialar["email_verified"] = ayar.EpostaDogrulandiString
	default:
		iddialar["email_verified"] = ayar.EpostaDogrulandi
	}

	jeton := jwt.NewWithClaims(jwt.SigningMethodRS256, iddialar)
	// kid HER ZAMAN YAYINLANAN anahtarin kid'idir: yanlis imza
	// anahtarinda bile dogrulayici anahtari bulur ve IMZA dogrulamasi
	// duser (bulamayan bir kid, imza kontrolunu degil anahtar aramasini
	// olcerdi).
	jeton.Header["kid"] = fedKid(s.yayinAnahtar)
	imzali, err := jeton.SignedString(imza)
	if err != nil {
		t.Fatalf("id_token imzalanamadi: %v", err)
	}
	return imzali
}

// ayarla, saglayicinin sonraki yanitlarini degistirir (ornegin ikinci
// giriste e-postayi hic gondermemek).
func (s *sahteOP) ayarla(f func(*sahteOPAyar)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.ayar)
}

// tokenSayaci, token ucuna kac kez gelindigini doner.
func (s *sahteOP) tokenSayaci() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenSayisi
}

// authorizeSorgusu, saglayiciya gelen son /authorize isteginin
// parametrelerini doner (PKCE testi onlari okur).
func (s *sahteOP) authorizeSorgusu() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sonAuthorize
}

// ---------------------------------------------------------------------
// SameSite'i uygulayan cerez kavanozu
// ---------------------------------------------------------------------

// cerezBaglam, bir istegin tarayici acisindan hangi sinifta oldugunu
// soyler. Cerez kurali (SameSite) ve Sec-Fetch-Site basligi bundan
// turetilir.
type cerezBaglam int

const (
	// baglamBaslatma: kullanicinin DOGRUDAN baslattigi navigasyon
	// (native uygulama sistem tarayicisini acar) -> Sec-Fetch-Site:
	// none, tum cerezler tasinir.
	baglamBaslatma cerezBaglam = iota
	// baglamAyniSite: kendi sayfalarimiz arasindaki gezinti.
	baglamAyniSite
	// baglamCaprazGet: BASKA bir siteden gelen UST-DUZEY GET
	// navigasyonu (Google'in 302 ile donusu). Lax cerez TASINIR.
	baglamCaprazGet
	// baglamCaprazPost: BASKA bir siteden gelen POST (Apple'in
	// form_post donusu). YALNIZCA SameSite=None cerez tasinir.
	baglamCaprazPost
)

func (b cerezBaglam) fetchSite() string {
	switch b {
	case baglamBaslatma:
		return fetchSiteYok
	case baglamAyniSite:
		return fetchSiteAyniOrigin
	default:
		return fetchSiteCapraz
	}
}

// cerezKavanozu, SameSite'i GERCEKTEN uygulayan bir http.CookieJar'dir.
//
// NEDEN STANDART KAVANOZ YETMEZ: net/http/cookiejar SameSite
// ozniteligini TUMUYLE YOK SAYAR (Cookies() yalnizca ad/deger doner).
// Onunla kosulan bir "uctan uca" test, federasyon cerezi Lax'e
// cevrilse bile YESIL kalirdi, yani K2'nin kaniti OLCULMEZDI. Bu
// kavanoz tarayici kuralini modeller: Lax capraz-site POST'ta
// GONDERILMEZ, None gonderilir.
//
// Secure bayragi MODELLENMEZ: test dinleyicisi http'dir ve uretim
// kablolamasi cerezGuvenli=false ile kurulur (bkz. testSunucuKur).
type cerezKavanozu struct {
	mu       sync.Mutex
	cerezler map[string]map[string]*http.Cookie // host -> ad\x00path -> cerez
	baglam   cerezBaglam
}

func yeniCerezKavanozu() *cerezKavanozu {
	return &cerezKavanozu{cerezler: map[string]map[string]*http.Cookie{}}
}

// baglamKur, SONRAKI istegin sinifini belirler. http.Client, Jar'a
// istegin "site for cookies" baglamini GECIRMEZ; tek tarayici tek
// goroutine'de kostugu icin baglami istekten hemen once kurmak dogru
// davranisi verir.
func (k *cerezKavanozu) baglamKur(b cerezBaglam) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.baglam = b
}

func (k *cerezKavanozu) SetCookies(u *url.URL, cerezler []*http.Cookie) {
	k.mu.Lock()
	defer k.mu.Unlock()
	host := k.cerezler[u.Host]
	if host == nil {
		host = map[string]*http.Cookie{}
		k.cerezler[u.Host] = host
	}
	for _, c := range cerezler {
		kopya := *c
		if kopya.Path == "" {
			kopya.Path = "/"
		}
		anahtar := kopya.Name + "\x00" + kopya.Path
		if kopya.MaxAge < 0 {
			delete(host, anahtar)
			continue
		}
		host[anahtar] = &kopya
	}
}

// cerezSil, verilen adli cerezi TUM host'lardan siler: "cerez
// olmadan tamamlanamaz" iddiasinda tek degiskeni cerez yapmak icin
// (tarayiciyi degistirmek iki seyi birden degistirirdi).
func (k *cerezKavanozu) cerezSil(ad string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, host := range k.cerezler {
		for anahtar, c := range host {
			if c.Name == ad {
				delete(host, anahtar)
			}
		}
	}
}

// cerezDegeri, kavanozdaki verilen adli cerezin degerini doner (yoksa
// bos string). Testler bunu ONKOSUL kurmak icin kullanir: iki
// tarayicinin federasyon cerezlerinin GERCEKTEN farkli oldugunu
// olcmeden "deger eslesmedi" iddiasi kurulamaz.
func (k *cerezKavanozu) cerezDegeri(ad string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, host := range k.cerezler {
		for _, c := range host {
			if c.Name == ad {
				return c.Value
			}
		}
	}
	return ""
}

func (k *cerezKavanozu) Cookies(u *url.URL) []*http.Cookie {
	k.mu.Lock()
	defer k.mu.Unlock()
	var secilen []*http.Cookie
	for _, c := range k.cerezler[u.Host] {
		if !cerezYoluEsler(c.Path, u.Path) {
			continue
		}
		if !k.tasinirMi(c) {
			continue
		}
		secilen = append(secilen, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	// Sira DETERMINISTIK olmali: map yinelemesi rasgeledir ve
	// basliktaki sira testler arasinda degisirse hata ayiklamasi
	// gereksiz yere zorlasir.
	sort.Slice(secilen, func(i, j int) bool { return secilen[i].Name < secilen[j].Name })
	return secilen
}

// tasinirMi, tarayicinin SameSite kuralini uygular.
func (k *cerezKavanozu) tasinirMi(c *http.Cookie) bool {
	switch k.baglam {
	case baglamBaslatma, baglamAyniSite:
		return true
	case baglamCaprazGet:
		// Ust-duzey capraz-site GET: Lax (ve oznitelik verilmemis =
		// varsayilan Lax) TASINIR, Strict tasinmaz.
		return c.SameSite != http.SameSiteStrictMode
	case baglamCaprazPost:
		// Capraz-site POST: YALNIZCA None. Apple'in form_post donusu
		// tam budur; federasyon cerezi bu yuzden None olmak zorunda.
		return c.SameSite == http.SameSiteNoneMode
	}
	return false
}

// cerezYoluEsler, RFC 6265 yol eslesmesidir.
func cerezYoluEsler(cerezYol, istekYol string) bool {
	if istekYol == "" {
		istekYol = "/"
	}
	if cerezYol == istekYol {
		return true
	}
	if !strings.HasPrefix(istekYol, cerezYol) {
		return false
	}
	return strings.HasSuffix(cerezYol, "/") || strings.HasPrefix(istekYol[len(cerezYol):], "/")
}

// ---------------------------------------------------------------------
// Yanit kaydi
// ---------------------------------------------------------------------

// yanitKaydi, tek bir HTTP yanitinin testin ihtiyac duydugu her seyini
// tutar. Govde ONCEDEN okunur: yonlendirme takibi sirasinda govdeyi
// kapatmayi unutmak sizintiya yol acardi.
type yanitKaydi struct {
	Durum    int
	istekURL *url.URL
	govde    string
	konum    string
	cerezler []*http.Cookie
}

func (y *yanitKaydi) Govde() string            { return y.govde }
func (y *yanitKaydi) Konum() string            { return y.konum }
func (y *yanitKaydi) Cerezler() []*http.Cookie { return y.cerezler }

// KonumKod, yanitin Location basligindaki authorization code'u doner
// (yoksa bos string). Akisin SONU istemcinin redirect_uri'sine
// yapilan yonlendirmedir ve kod oradadir.
func (y *yanitKaydi) KonumKod() string {
	if y.konum == "" {
		return ""
	}
	u, err := url.Parse(y.konum)
	if err != nil {
		return ""
	}
	return u.Query().Get("code")
}

// fedCerezi, yanitta yazilan federasyon cerezini doner (yoksa nil).
func (y *yanitKaydi) fedCerezi() *http.Cookie {
	for _, c := range y.cerezler {
		if c.Name == cerezAdFederasyon {
			return c
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Tarayici
// ---------------------------------------------------------------------

// fedTarayici, kendi cerez kabi olan ve yonlendirmeleri ELLE takip eden
// bir tarayicidir: her bacagin durum kodu ve Location basligi testin
// olctugu seydir.
//
// NEDEN akis_test.go'daki tarayici() yardimcisi yetmez: o, standart
// cookiejar kullanir (SameSite yok) ve yonlendirmeleri takip etmez.
// O yardimci BOZULMADAN birakildi; bu tip onun uctan uca surumudur.
type fedTarayici struct {
	ortam     *akisOrtami
	kavanoz   *cerezKavanozu
	istemci   *http.Client
	verifier  string
	challenge string
}

// YeniTarayici, kendi cerez kabi olan yeni bir tarayici doner. Her
// tarayicinin KENDI PKCE cifti vardir: saldiri testleri "saldirganin
// verifier'i" ile "kurbanin verifier'i"ni ayirmak zorunda.
func (o *akisOrtami) YeniTarayici(t *testing.T) *fedTarayici {
	t.Helper()
	verifier, challenge := pkceS256(t)
	kavanoz := yeniCerezKavanozu()
	return &fedTarayici{
		ortam:   o,
		kavanoz: kavanoz,
		istemci: &http.Client{
			Jar:     kavanoz,
			Timeout: 10 * time.Second,
			// Yonlendirmeler ELLE takip edilir: her adimda cerez
			// baglamini (SameSite) ve Sec-Fetch-Site'i hesaplamak
			// gerekiyor, http.Client bunu yapamaz.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		verifier:  verifier,
		challenge: challenge,
	}
}

// istek, tek bir HTTP istegi yapar. baglam, cerez kuralini VE
// Sec-Fetch-Site basligini belirler.
func (b *fedTarayici) istek(
	t *testing.T, yontem, hedef string, form url.Values, baglam cerezBaglam,
) *yanitKaydi {
	t.Helper()
	var govde io.Reader
	if form != nil {
		govde = strings.NewReader(form.Encode())
	}
	istek, err := http.NewRequestWithContext(t.Context(), yontem, hedef, govde)
	if err != nil {
		t.Fatalf("istek kurulamadi (%s %s): %v", yontem, hedef, err)
	}
	if form != nil {
		istek.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	istek.Header.Set(baslikFetchMode, "navigate")
	istek.Header.Set(baslikFetchSite, baglam.fetchSite())

	b.kavanoz.baglamKur(baglam)
	yanit, err := b.istemci.Do(istek)
	if err != nil {
		t.Fatalf("%s %s basarisiz: %v", yontem, hedef, err)
	}
	defer func() { _ = yanit.Body.Close() }()
	ham, err := io.ReadAll(yanit.Body)
	if err != nil {
		t.Fatalf("govde okunamadi: %v", err)
	}
	return &yanitKaydi{
		Durum:    yanit.StatusCode,
		istekURL: istek.URL,
		govde:    string(ham),
		konum:    yanit.Header.Get("Location"),
		cerezler: yanit.Cookies(),
	}
}

// istekBaslikli, API cagrilari icin (ornegin /userinfo) ek baslikli bir
// GET yapar. Cerez baglami AYNI SITE: bu bir API cagrisidir, akisin
// bacaklarindan biri degil.
func (b *fedTarayici) istekBaslikli(
	t *testing.T, yontem, hedef string, basliklar map[string]string,
) *yanitKaydi {
	t.Helper()
	istek, err := http.NewRequestWithContext(t.Context(), yontem, hedef, nil)
	if err != nil {
		t.Fatalf("istek kurulamadi (%s %s): %v", yontem, hedef, err)
	}
	for ad, deger := range basliklar {
		istek.Header.Set(ad, deger)
	}
	b.kavanoz.baglamKur(baglamAyniSite)
	yanit, err := b.istemci.Do(istek)
	if err != nil {
		t.Fatalf("%s %s basarisiz: %v", yontem, hedef, err)
	}
	defer func() { _ = yanit.Body.Close() }()
	ham, err := io.ReadAll(yanit.Body)
	if err != nil {
		t.Fatalf("govde okunamadi: %v", err)
	}
	return &yanitKaydi{
		Durum:    yanit.StatusCode,
		istekURL: istek.URL,
		govde:    string(ham),
		konum:    yanit.Header.Get("Location"),
		cerezler: yanit.Cookies(),
	}
}

// jsonCoz, bir JSON govdesini haritaya cevirir.
func jsonCoz(t *testing.T, govde string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(govde), &m); err != nil {
		t.Fatalf("JSON cozulemedi (%q): %v", govde, err)
	}
	return m
}

// otoFormDeseni, sahte saglayicinin kendiliginden gonderilen formunu
// bulur (isaret zorunlu, bkz. otoFormIsaret).
var otoFormDeseni = regexp.MustCompile(`(?s)<form ` + regexp.QuoteMeta(otoFormIsaret) + `[^>]*action="([^"]*)"(.*?)</form>`)

var gizliAlanDeseni = regexp.MustCompile(`name="([^"]*)" value="([^"]*)"`)

// otoFormCoz, govdede kendiliginden gonderilecek bir form varsa
// (eylem, alanlar, true) doner.
func otoFormCoz(govde string) (string, url.Values, bool) {
	esler := otoFormDeseni.FindStringSubmatch(govde)
	if esler == nil {
		return "", nil, false
	}
	alanlar := url.Values{}
	for _, alan := range gizliAlanDeseni.FindAllStringSubmatch(esler[2], -1) {
		alanlar.Set(alan[1], alan[2])
	}
	return esler[1], alanlar, true
}

// takipSiniri, yonlendirme zincirinde izlenecek en fazla adim. Gercek
// akis 4-5 adimdir; sinir, bir dongu halinde testin asilmasini onler.
const takipSiniri = 12

// akisiTakipEt, gercek bir tarayici gibi yonlendirmeleri ve
// kendiliginden gonderilen form_post formunu izler. Her adimda
// capraz-site olup olmadigi HOST KARSILASTIRMASIYLA hesaplanir: ust
// duzey navigasyonlarda tarayici da cerez baglamini her adimda yeniden
// hesaplar, bu yuzden saglayicidan BIZE donen POST capraz-site, oradan
// sonraki kendi yonlendirmelerimiz ayni-sitedir.
//
// Custom scheme (com.makesinger.app:/...) adresinde DURULUR: orada
// tarayici isi native uygulamaya devreder, yani akisin sonudur ve kod o
// yanitin Location basligindadir.
func (b *fedTarayici) akisiTakipEt(t *testing.T, yanit *yanitKaydi) *yanitKaydi {
	t.Helper()
	kaynak := yanit.istekURL
	for adim := 0; adim < takipSiniri; adim++ {
		switch yanit.Durum {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			if yanit.konum == "" {
				return yanit
			}
			hedef, err := kaynak.Parse(yanit.konum)
			if err != nil {
				t.Fatalf("Location cozulemedi (%q): %v", yanit.konum, err)
			}
			if hedef.Scheme != "http" && hedef.Scheme != "https" {
				// Istemcinin redirect_uri'si: akisin sonu.
				return yanit
			}
			yanit = b.istek(t, http.MethodGet, hedef.String(), nil, adimBaglami(kaynak, hedef, http.MethodGet))
			kaynak = hedef
		default:
			eylem, alanlar, varMi := otoFormCoz(yanit.govde)
			if !varMi {
				return yanit
			}
			hedef, err := kaynak.Parse(eylem)
			if err != nil {
				t.Fatalf("form action cozulemedi (%q): %v", eylem, err)
			}
			yanit = b.istek(t, http.MethodPost, hedef.String(), alanlar, adimBaglami(kaynak, hedef, http.MethodPost))
			kaynak = hedef
		}
	}
	t.Fatalf("yonlendirme zinciri %d adimda bitmedi", takipSiniri)
	return nil
}

// adimBaglami, bir adimin cerez baglamini kaynak ve hedef host'tan
// hesaplar.
func adimBaglami(kaynak, hedef *url.URL, yontem string) cerezBaglam {
	if kaynak != nil && kaynak.Host == hedef.Host {
		return baglamAyniSite
	}
	if yontem == http.MethodPost {
		return baglamCaprazPost
	}
	return baglamCaprazGet
}

// fedKapsamlar, uctan uca akislarda istenen kapsamlar. email ve profile
// ZORUNLU: akisin sonunda id_token/userinfo iddialarini olcuyoruz.
// offline_access de zorunlu: refresh rotasyonu akisin parcasidir.
const fedKapsamlar = "openid profile email offline_access"

// AuthorizeBaslat, native uygulamanin sistem tarayicisini acmasini
// taklit eder (Sec-Fetch-Site: none) ve authRequestID doner.
func (b *fedTarayici) AuthorizeBaslat(t *testing.T) string {
	t.Helper()
	return b.authorizeBaslatKapsam(t, fedKapsamlar, "S256")
}

// authorizeBaslatKapsam, AuthorizeBaslat'in kapsam ve PKCE yontemi
// secilebilen surumudur: PKCE saldiri testi code_challenge'i HIC
// gondermeden /authorize'a gelmeyi dener.
func (b *fedTarayici) authorizeBaslatKapsam(t *testing.T, kapsam, yontem string) string {
	t.Helper()
	challenge := b.challenge
	if yontem == "" {
		challenge = ""
	}
	yanit := b.istek(t, http.MethodGet,
		b.ortam.authorizeURLKapsamli(challenge, yontem, kapsam), nil, baglamBaslatma)
	if yanit.Durum != http.StatusFound {
		t.Fatalf("/authorize durumu = %d, beklenen 302. govde: %s", yanit.Durum, yanit.Govde())
	}
	konum, err := url.Parse(yanit.Konum())
	if err != nil {
		t.Fatalf("Location cozulemedi: %v", err)
	}
	if konum.Path != yolGiris {
		t.Fatalf("Location yolu = %q, beklenen %q", konum.Path, yolGiris)
	}
	id := konum.Query().Get("authRequestID")
	if id == "" {
		t.Fatal("Location'da authRequestID yok")
	}
	return id
}

// FederasyonAkisiniKostur, giris sayfasindaki saglayici butonuna
// basmaktan akisin sonuna kadar her adimi kosturur ve SON yaniti doner.
func (b *fedTarayici) FederasyonAkisiniKostur(t *testing.T, saglayici, istekID string) *yanitKaydi {
	t.Helper()
	return b.akisiTakipEt(t, b.FederasyonBaslat(t, saglayici, istekID))
}

// FederasyonBaslat, YALNIZCA ilk bacagi kosturur (baslat ucu) ve onun
// yanitini doner: saldiri testleri zincirin ortasina girmek icin bu
// yanitin state'ine ve cerezine ihtiyac duyar.
func (b *fedTarayici) FederasyonBaslat(t *testing.T, saglayici, istekID string) *yanitKaydi {
	t.Helper()
	hedef := b.ortam.issuer + federasyonBaslatYolu(saglayici) +
		"?" + url.Values{"authRequestID": {istekID}}.Encode()
	// Kullanici giris sayfasindaki butona basar: ayni-site ust-duzey GET.
	return b.istek(t, http.MethodGet, hedef, nil, baglamAyniSite)
}

// IstekTamamlandiMi, bekleyen auth isteginin bir kimlige baglanip
// baglanmadigini soyler: "giris oldu mu" sorusunun depo tarafindaki
// kesin cevabi.
func (b *fedTarayici) IstekTamamlandiMi(t *testing.T, istekID string) bool {
	t.Helper()
	return b.ortam.IstekTamamlandiMi(t, istekID)
}

// KodDegis, kodu TOPLAYIP jetona cevirmeyi dener. Basarisizsa hata
// doner: "giris yapilmadi" iddiasinin kaniti budur.
func (b *fedTarayici) KodDegis(t *testing.T, istekID string) (string, error) {
	t.Helper()
	durum, jeton, err := b.KodDegisTam(t, istekID)
	if err != nil {
		return "", err
	}
	if durum != http.StatusOK {
		return "", fmt.Errorf("jeton ucu durumu %d: %v", durum, jeton)
	}
	erisim, _ := jeton["access_token"].(string)
	if erisim == "" {
		return "", errors.New("access_token yok")
	}
	return erisim, nil
}

// KodDegisTam, /authorize/callback bacagini bu tarayicida kosturur ve
// token ucunun TUM yanitini doner (mutlu yol testleri id_token ve
// refresh_token'i da olcer).
func (b *fedTarayici) KodDegisTam(t *testing.T, istekID string) (int, map[string]any, error) {
	t.Helper()
	yanit := b.istek(t, http.MethodGet,
		b.ortam.issuer+"/authorize/callback?"+url.Values{"id": {istekID}}.Encode(),
		nil, baglamAyniSite)
	kod := yanit.KonumKod()
	if kod == "" {
		return 0, nil, fmt.Errorf("kod uretilmedi (callback durumu %d)", yanit.Durum)
	}
	durum, govde := b.ortam.jetonAl(t, kod, b.verifier)
	return durum, govde, nil
}

var beklemeIDDeseni = regexp.MustCompile(`name="beklemeID" value="([^"]*)"`)

// BaglamaGonder, /baglama formunu VERILEN govdeden okuyup gonderir:
// beklemeID ve CSRF jetonu formun kendisinden gelir, testin elle
// kurdugu degerlerden degil.
func (b *fedTarayici) BaglamaGonder(t *testing.T, govde, sifre string) *yanitKaydi {
	t.Helper()
	beklemeEs := beklemeIDDeseni.FindStringSubmatch(govde)
	if beklemeEs == nil {
		t.Fatalf("baglama formunda beklemeID yok; govde: %s", govde)
	}
	csrfEs := akisCSRFDeseni.FindStringSubmatch(govde)
	if csrfEs == nil {
		t.Fatal("baglama formunda CSRF jetonu yok")
	}
	return b.istek(t, http.MethodPost, b.ortam.issuer+yolBaglama, url.Values{
		alanBeklemeID: {beklemeEs[1]},
		csrfAlan:      {csrfEs[1]},
		"sifre":       {sifre},
	}, baglamAyniSite)
}

// ---------------------------------------------------------------------
// Akis ortami: uretim kablolamasi + sahte yukari akislar
// ---------------------------------------------------------------------

// gevsekHizAyar, uctan uca akislarin hiz limitine TAKILMAMASI icin
// gereken ayardir: tek bir uctan uca akis ayni IP'den (127.0.0.1) bir
// dizi istek yapar ve limite carpan bir test 429'u olcer, akisi degil.
// Hiz limitinin KENDISI hizlimit_test.go ve
// TestMuxKurFederasyonKablolamasi'nda dar sinirlarla olculur.
func gevsekHizAyar() HizLimitAyar {
	return HizLimitAyar{
		GirisIPPerMin:      1000,
		GirisEpostaPerSaat: 1000,
		KayitIPPerSaat:     1000,
	}
}

// akisOrtamiKur, sahte yukari akislari (Google 302 donusu, Apple
// form_post donusu), GERCEK bir muxKur dinleyicisini ve Redis'i
// birlikte kurar.
//
// Postgres KULLANILMAZ: kullanici ve federe depolari bellek ici
// sahtelerdir. Gerekce, Faz 1'den beri surdurulen ayrim: depolarin
// Postgres ile BIREBIR ayni davrandigini sozlesme testleri olcer
// (TestUserStoreSozlesmesi, TestFedereDepoSozlesmesi -- ikisi de artik
// CI'da gercekten kosuyor), bu dosya ise akisin KENDISINI olcer.
func akisOrtamiKur(t *testing.T, ayar sahteOPAyar) *akisOrtami {
	t.Helper()
	return testSunucuKurAyarli(t, gevsekHizAyar(), true, ayar)
}

// federasyonKablola, iki sahte yukari akisi (google + apple) ve onlarin
// dogrulayicilarini kurar. testSunucuKurAyarli'nin federasyon dali
// buradan beslenir, yani uretim kablolamasi (muxKur) tek bir yerden
// kurulur.
func federasyonKablola(t *testing.T, ayar sahteOPAyar) (
	map[string]*YukariAkis,
	map[string]*auth.YukariAkisDogrulayici,
	map[string]*sahteOP,
) {
	t.Helper()
	google := sahteYukariAkisOP(t, saglayiciGoogle, ayar)
	// Apple'in donusu HER ZAMAN form_post: name/email kapsami istenince
	// gercek davranis budur (K2) ve uctan uca kanit tam oradan geliyor.
	appleAyar := ayar
	appleAyar.FormPost = true
	apple := sahteYukariAkisOP(t, saglayiciApple, appleAyar)
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
			// (K5), bu yuzden testte de GERCEK bir .p8 gerekir.
			AppleEkipID: "EKIP123", AppleAnahtarID: "ANAHTAR9", AppleAnahtarP8: appleP8,
		},
	}
	oplar := map[string]*sahteOP{saglayiciGoogle: google, saglayiciApple: apple}
	dogrulayicilar := map[string]*auth.YukariAkisDogrulayici{}
	for ad, s := range oplar {
		d, err := auth.NewYukariAkisDogrulayici(
			t.Context(), s.sunucu.URL+"/jwks", s.sunucu.URL, s.istemciID)
		if err != nil {
			t.Fatalf("%s dogrulayicisi kurulamadi: %v", ad, err)
		}
		t.Cleanup(func() { _ = d.Close() })
		dogrulayicilar[ad] = d
	}
	return akislar, dogrulayicilar, oplar
}

// YukariAkis, verilen saglayicinin sahte OP'sini doner: saldiri
// testleri yanitlarini oradan bozar.
func (o *akisOrtami) YukariAkis(t *testing.T, saglayici string) *sahteOP {
	t.Helper()
	s, varMi := o.yukari[saglayici]
	if !varMi {
		t.Fatalf("%q icin sahte yukari akis kurulmamis", saglayici)
	}
	return s
}

// KayitOl, GERCEK kayit akisindan gecerek sifreli bir hesap acar:
// /authorize -> GET /kayit -> POST /kayit. Depoya dogrudan yazmak
// yerine akistan gecmek sart: onkosulun uretim yolundan kuruldugunu
// kanitlar.
func (o *akisOrtami) KayitOl(t *testing.T, eposta, ad, sifre string) {
	t.Helper()
	b := o.YeniTarayici(t)
	id := b.AuthorizeBaslat(t)
	form := b.istek(t, http.MethodGet,
		o.issuer+yolKayit+"?"+url.Values{"authRequestID": {id}}.Encode(), nil, baglamAyniSite)
	if form.Durum != http.StatusOK {
		t.Fatalf("GET /kayit durumu = %d, beklenen 200", form.Durum)
	}
	csrfEs := akisCSRFDeseni.FindStringSubmatch(form.Govde())
	if csrfEs == nil {
		t.Fatal("kayit formunda CSRF jetonu yok")
	}
	yanit := b.istek(t, http.MethodPost, o.issuer+yolKayit, url.Values{
		"eposta": {eposta}, "ad": {ad}, "sifre": {sifre},
		"authRequestID": {id}, csrfAlan: {csrfEs[1]},
	}, baglamAyniSite)
	if yanit.Durum != http.StatusFound {
		t.Fatalf("POST /kayit durumu = %d, beklenen 302. govde: %s", yanit.Durum, yanit.Govde())
	}
}

// SifreyleGiris, sifre yolundan uctan uca giris yapar ve access
// token'i doner; akisin HERHANGI bir adimi tutmazsa bos string doner.
//
// NEDEN BOS STRING (hata degil): bu yardimci hem ONKOSUL kontrolu
// ("saldirganin hesabi gercekten acildi mi") hem de negatif iddia
// ("bu sifreyle girilemez") icin kullanilir.
func (o *akisOrtami) SifreyleGiris(t *testing.T, eposta, sifre string) string {
	t.Helper()
	b := o.YeniTarayici(t)
	id := b.AuthorizeBaslat(t)
	form := b.istek(t, http.MethodGet,
		o.issuer+yolGiris+"?"+url.Values{"authRequestID": {id}}.Encode(), nil, baglamAyniSite)
	if form.Durum != http.StatusOK {
		return ""
	}
	csrfEs := akisCSRFDeseni.FindStringSubmatch(form.Govde())
	if csrfEs == nil {
		return ""
	}
	yanit := b.istek(t, http.MethodPost, o.issuer+yolGiris, url.Values{
		"eposta": {eposta}, "sifre": {sifre},
		"authRequestID": {id}, csrfAlan: {csrfEs[1]},
	}, baglamAyniSite)
	if yanit.Durum != http.StatusFound {
		return ""
	}
	jeton, err := b.KodDegis(t, id)
	if err != nil {
		return ""
	}
	return jeton
}

// IstekTamamlandiMi, bekleyen auth isteginin bir kimlige baglandigini
// soyler. Istek hic yoksa false: baglanmamis demektir.
func (o *akisOrtami) IstekTamamlandiMi(t *testing.T, istekID string) bool {
	t.Helper()
	istek, err := o.istekler.IDileOku(t.Context(), istekID)
	if errors.Is(err, ErrIstekYok) {
		return false
	}
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	return istek.Done()
}

// FedereEslesmeSayisi, yazilmis (saglayici, sub) -> kullanici
// eslesmelerinin sayisini doner: "baglama YAPILMADI" iddiasinin kaniti.
func (o *akisOrtami) FedereEslesmeSayisi(t *testing.T) int {
	t.Helper()
	if o.federe == nil {
		t.Fatal("federe depo kurulmamis")
	}
	return o.federe.eslesmeSayisi()
}

// eslesmeSayisi, sahte depodaki eslesme sayisini doner. Yalnizca
// testler icin; uretim arayuzunde (FedereDepo) boyle bir islem yok ve
// olmasina gerek de yok.
func (d *SahteFedereDepo) eslesmeSayisi() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.eslesmeler)
}

// ---------------------------------------------------------------------
// Jeton iddialari
// ---------------------------------------------------------------------

// jwtIddialari, bir JWT'nin BASLIGINI ve iddialarini DOGRULAMADAN
// cozer. Imza burada dogrulanmaz; alg'in RS256 oldugu basliktan
// okunarak IDDIA EDILIR (bkz. TestUctanUcaGoogleGirisi). Jetonun
// imzasinin gecerliligini kutuphane kendi testlerinde olcuyor, burada
// olculen sey claim'lerin icerigidir.
func jwtIddialari(t *testing.T, jeton string) (map[string]any, map[string]any) {
	t.Helper()
	parcalar := strings.Split(jeton, ".")
	if len(parcalar) != 3 {
		t.Fatalf("jeton JWT degil (%d parca)", len(parcalar))
	}
	coz := func(parca string) map[string]any {
		ham, err := base64.RawURLEncoding.DecodeString(parca)
		if err != nil {
			t.Fatalf("jeton parcasi cozulemedi: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(ham, &m); err != nil {
			t.Fatalf("jeton parcasi JSON degil: %v", err)
		}
		return m
	}
	return coz(parcalar[0]), coz(parcalar[1])
}

// audIcerir, aud claim'inin (string ya da dizi) verilen degeri
// icerdigini soyler.
func audIcerir(aud any, beklenen string) bool {
	switch v := aud.(type) {
	case string:
		return v == beklenen
	case []any:
		for _, e := range v {
			if s, uygun := e.(string); uygun && s == beklenen {
				return true
			}
		}
	}
	return false
}

// amrDegerleri, amr claim'ini string dilimine cevirir.
func amrDegerleri(amr any) []string {
	ham, uygun := amr.([]any)
	if !uygun {
		return nil
	}
	degerler := make([]string, 0, len(ham))
	for _, e := range ham {
		if s, uygun := e.(string); uygun {
			degerler = append(degerler, s)
		}
	}
	return degerler
}

// yenile, refresh_token akisini kosturur.
func (o *akisOrtami) yenile(t *testing.T, refresh string) (int, map[string]any) {
	t.Helper()
	yanit, err := http.PostForm(o.issuer+"/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {"makesinger-mobil"},
	})
	if err != nil {
		t.Fatalf("POST /oauth/token (refresh): %v", err)
	}
	defer func() { _ = yanit.Body.Close() }()
	var govde map[string]any
	_ = json.NewDecoder(yanit.Body).Decode(&govde)
	return yanit.StatusCode, govde
}
