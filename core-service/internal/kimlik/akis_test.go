package kimlik

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"
)

// Bu dosya, OP'nin TAMAMINI gercek bir TCP dinleyicisi uzerinde ucan uca
// olcer: /authorize -> /giris -> /authorize/callback -> /oauth/token.
// Yalnizca Redis ister (kullanici ve jeton depolari sahte), bu yuzden
// Postgres olmadan da kosar; Redis yoksa testRedis (istek_test.go) ile
// AYNI kalipta atlanir.

// testCryptoAnahtari, op.Config.CryptoKey icin 32 baytlik TEST-YEREL
// degerdir. Uretim sirri DEGILDIR ve uretimde AUTH_CRYPTO_KEY'den
// gelir; kaynak koda uretim sirri yazilmaz.
const testCryptoAnahtari = "kimlik-test-yerel-crypto-32byte!"

const testRedirectURI = "com.makesinger.app:/oauth2redirect"

type akisOrtami struct {
	issuer    string
	kullanici UserStore
	istekler  *IstekDepo
}

// testSunucu, muxKur ile uretimdekiyle AYNI kablolamayi kurar ve gercek
// bir dinleyicide yayinlar. Issuer, dinleyicinin adresinden turetilir:
// discovery ve op.AuthCallbackURL dogru adresi uretsin.
func testSunucu(t *testing.T) *akisOrtami {
	t.Helper()
	// Uretim kablolamasi HER akis testinde kosar (muxKur gercek bir
	// *HizLimit alir), ama sinirlar bu testleri etkilemeyecek kadar
	// yuksek: hiz limitinin KENDISI hizlimit_test.go'da dar sinirlarla
	// olculur.
	return testSunucuAyarli(t, HizLimitAyar{
		GirisIPPerMin:      1000,
		GirisEpostaPerSaat: 1000,
		KayitIPPerSaat:     1000,
		GuvenilenProxy:     0,
	})
}

func testSunucuAyarli(t *testing.T, ayar HizLimitAyar) *akisOrtami {
	t.Helper()
	rdb := testRedis(t) // Redis yoksa burada atlanir

	anahtar, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	cryptoAnahtar, err := CryptoAnahtar(testCryptoAnahtari)
	if err != nil {
		t.Fatalf("CryptoAnahtar: %v", err)
	}

	dinleyici, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("dinleyici acilamadi: %v", err)
	}
	issuer := "http://" + dinleyici.Addr().String()

	cfg := testAuthCfg()
	cfg.Issuer = issuer
	cfg.RedirectURIs = []string{testRedirectURI}

	kullanici := NewSahteUserStore()
	istekler := NewIstekDepo(rdb)
	depo := NewDepo(cfg, kullanici, NewSahteTokenStore(), istekler, anahtar)

	opCfg := &op.Config{
		CryptoKey:             cryptoAnahtar,
		CodeMethodS256:        true,
		AuthMethodPost:        false,
		GrantTypeRefreshToken: true,
		SupportedUILocales:    []language.Tag{language.Turkish, language.English},
		SupportedScopes: []string{
			oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess,
		},
	}
	saglayici, err := op.NewProvider(opCfg, depo, op.StaticIssuer(issuer), op.WithAllowInsecure())
	if err != nil {
		t.Fatalf("OpenIDProvider: %v", err)
	}

	hizLimit, err := NewHizLimit(rdb, ayar, []byte(testCryptoAnahtari))
	if err != nil {
		t.Fatalf("NewHizLimit: %v", err)
	}

	// cerezGuvenli=false: test dinleyicisi http, Secure cerez
	// tasinmazdi.
	srv := &http.Server{
		Handler:           muxKur(saglayici, kullanici, istekler, false, hizLimit),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(dinleyici) }()
	t.Cleanup(func() {
		ctx, iptal := context.WithTimeout(context.Background(), 3*time.Second)
		defer iptal()
		_ = srv.Shutdown(ctx)
	})

	return &akisOrtami{issuer: issuer, kullanici: kullanici, istekler: istekler}
}

// tarayici, kendi cerez kabi olan ve yonlendirmeleri OTOMATIK TAKIP
// ETMEYEN bir istemci doner: her bacagi tek tek olcebilmek icin.
func tarayici(t *testing.T) *http.Client {
	t.Helper()
	kap, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar:     kap,
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// pkceS256, (verifier, challenge) ciftini uretir.
func pkceS256(t *testing.T) (string, string) {
	t.Helper()
	verifier, err := rasgeleJeton()
	if err != nil {
		t.Fatalf("verifier uretilemedi: %v", err)
	}
	return verifier, oidc.NewSHACodeChallenge(verifier)
}

// authorizeURL, verilen PKCE parametreleriyle bir /authorize adresi
// kurar. challenge veya method bos gecilirse o parametre HIC
// EKLENMEZ (eksik parametre ile bos parametreyi ayirmak icin).
func (o *akisOrtami) authorizeURL(challenge, method string) string {
	q := url.Values{
		"client_id":     {"makesinger-mobil"},
		"redirect_uri":  {testRedirectURI},
		"response_type": {"code"},
		"scope":         {"openid offline_access"},
		"state":         {"durum-1"},
		"nonce":         {"nonce-1"},
	}
	if challenge != "" {
		q.Set("code_challenge", challenge)
	}
	if method != "" {
		q.Set("code_challenge_method", method)
	}
	return o.issuer + "/authorize?" + q.Encode()
}

func al(t *testing.T, c *http.Client, adres string) *http.Response {
	t.Helper()
	yanit, err := c.Get(adres)
	if err != nil {
		t.Fatalf("GET basarisiz: %v", err)
	}
	t.Cleanup(func() { _ = yanit.Body.Close() })
	return yanit
}

func govde(t *testing.T, yanit *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(yanit.Body)
	if err != nil {
		t.Fatalf("govde okunamadi: %v", err)
	}
	return string(b)
}

// authRequestIDCikar, /authorize'in giris sayfasina yonlendirmesinden
// authRequestID'yi alir.
func authRequestIDCikar(t *testing.T, yanit *http.Response) string {
	t.Helper()
	if yanit.StatusCode != http.StatusFound {
		t.Fatalf("/authorize durumu = %d, beklenen 302", yanit.StatusCode)
	}
	konum, err := url.Parse(yanit.Header.Get("Location"))
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

var akisCSRFDeseni = regexp.MustCompile(`name="csrf" value="([^"]*)"`)

// girisDene, verilen tarayicida giris formunu ACMAYA calisir ve form
// gosterildiyse gonderir. Doner: (POST yaniti veya form yaniti, GET
// formunun durum kodu).
//
// NEDEN "DENE": baglama artik YALNIZCA /authorize'da kuruldugu icin,
// akisi baslatmayan bir tarayicida GET /giris 403 doner ve POST'a hic
// gelinmez. Saldiri testleri iki durumu da olcebilmeli: duzeltilmis
// kodda form hic gosterilmez, mutasyonlu kodda (GET yeniden mintlerse)
// gosterilir ve akis tamamlanir.
func (o *akisOrtami) girisDene(t *testing.T, c *http.Client, id, eposta, sifre string) (*http.Response, int) {
	t.Helper()
	formYanit := al(t, c, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(id))
	if formYanit.StatusCode != http.StatusOK {
		return formYanit, formYanit.StatusCode
	}
	esler := akisCSRFDeseni.FindStringSubmatch(govde(t, formYanit))
	if esler == nil {
		t.Fatal("giris formunda CSRF jetonu yok")
	}
	form := url.Values{
		"eposta": {eposta}, "sifre": {sifre},
		"authRequestID": {id}, csrfAlan: {esler[1]},
	}
	yanit, err := c.PostForm(o.issuer+yolGiris, form)
	if err != nil {
		t.Fatalf("POST /giris: %v", err)
	}
	t.Cleanup(func() { _ = yanit.Body.Close() })
	return yanit, http.StatusOK
}

// girisFormuAc, giris formunu acar ve formdaki CSRF jetonunu doner.
// Form reddedilirse test durur.
func (o *akisOrtami) girisFormuAc(t *testing.T, c *http.Client, id string) string {
	t.Helper()
	formYanit := al(t, c, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(id))
	if formYanit.StatusCode != http.StatusOK {
		t.Fatalf("giris formu durumu = %d", formYanit.StatusCode)
	}
	esler := akisCSRFDeseni.FindStringSubmatch(govde(t, formYanit))
	if esler == nil {
		t.Fatal("giris formunda CSRF jetonu yok")
	}
	return esler[1]
}

// girisGonder, ACIK bir formdan POST /giris yapar. Formu yeniden
// ACMAZ: gercek tarayici da basarisiz denemede formu yeniden
// yuklemez, govdeyle birlikte donen sayfayi gosterir.
func (o *akisOrtami) girisGonder(t *testing.T, c *http.Client, id, csrf, eposta, sifre string) *http.Response {
	t.Helper()
	yanit, err := c.PostForm(o.issuer+yolGiris, url.Values{
		"eposta": {eposta}, "sifre": {sifre},
		"authRequestID": {id}, csrfAlan: {csrf},
	})
	if err != nil {
		t.Fatalf("POST /giris: %v", err)
	}
	t.Cleanup(func() { _ = yanit.Body.Close() })
	return yanit
}

// girisYap, girisDene'nin "form GOSTERILMEK ZORUNDA" surumudur: mutlu
// yol testleri icin.
func (o *akisOrtami) girisYap(t *testing.T, c *http.Client, id, eposta, sifre string) *http.Response {
	t.Helper()
	yanit, formDurum := o.girisDene(t, c, id, eposta, sifre)
	if formDurum != http.StatusOK {
		t.Fatalf("giris formu durumu = %d", formDurum)
	}
	return yanit
}

// kodCikar, callback yanitindan authorization code'u cikarir (yoksa bos
// string).
func kodCikar(t *testing.T, yanit *http.Response) string {
	t.Helper()
	if yanit.StatusCode != http.StatusFound {
		return ""
	}
	konum, err := url.Parse(yanit.Header.Get("Location"))
	if err != nil {
		return ""
	}
	return konum.Query().Get("code")
}

// jetonAl, authorization code'u jetona cevirir.
func (o *akisOrtami) jetonAl(t *testing.T, kod, verifier string) (int, map[string]any) {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {kod},
		"redirect_uri":  {testRedirectURI},
		"client_id":     {"makesinger-mobil"},
		"code_verifier": {verifier},
	}
	yanit, err := http.PostForm(o.issuer+"/oauth/token", form)
	if err != nil {
		t.Fatalf("POST /oauth/token: %v", err)
	}
	defer func() { _ = yanit.Body.Close() }()
	var govdeJSON map[string]any
	_ = json.NewDecoder(yanit.Body).Decode(&govdeJSON)
	return yanit.StatusCode, govdeJSON
}

// testKullanici, sahte depoda bir hesap acar.
func (o *akisOrtami) testKullanici(t *testing.T, eposta, sifre string) *User {
	t.Helper()
	k, err := o.kullanici.Create(context.Background(), eposta, "Test", sifre)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return k
}

// ---- Mutlu yol ----

// Ucan uca S256 PKCE akisi: /authorize -> /giris -> callback -> jeton.
// Bu test, C1 ve C2 duzeltmelerinin akisi KIRMADIGININ kilidi.
func TestAkisS256IleUctanUcaCalisir(t *testing.T) {
	o := testSunucu(t)
	kullanici := o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	verifier, challenge := pkceS256(t)
	c := tarayici(t)

	id := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge, "S256")))

	girisYanit := o.girisYap(t, c, id, "kurban@ornek.com", "dogruSifre12")
	if girisYanit.StatusCode != http.StatusFound {
		t.Fatalf("POST /giris durumu = %d, beklenen 302. govde: %s",
			girisYanit.StatusCode, govde(t, girisYanit))
	}
	callbackURL := girisYanit.Header.Get("Location")
	if !strings.Contains(callbackURL, "/authorize/callback") {
		t.Fatalf("giris sonrasi Location beklenmedik: %q", callbackURL)
	}

	cbYanit := al(t, c, callbackURL)
	if cbYanit.StatusCode != http.StatusFound {
		t.Fatalf("callback durumu = %d, beklenen 302. govde: %s",
			cbYanit.StatusCode, govde(t, cbYanit))
	}
	son, err := url.Parse(cbYanit.Header.Get("Location"))
	if err != nil {
		t.Fatalf("Location cozulemedi: %v", err)
	}
	kod := son.Query().Get("code")
	if kod == "" {
		t.Fatalf("kod uretilmedi, Location: %q", son.String())
	}
	if son.Query().Get("state") != "durum-1" {
		t.Errorf("state = %q", son.Query().Get("state"))
	}

	durum, jeton := o.jetonAl(t, kod, verifier)
	if durum != http.StatusOK {
		t.Fatalf("token durumu = %d, govde: %v", durum, jeton)
	}
	if jeton["access_token"] == nil || jeton["access_token"] == "" {
		t.Error("access_token yok")
	}
	if jeton["refresh_token"] == nil || jeton["refresh_token"] == "" {
		t.Error("offline_access istendi ama refresh_token yok")
	}
	_ = kullanici
}

// ---- C1: hesap devralma saldirisinin kendisi ----
//
// Re-review, onceki tek saldiri testinin (saldirgan giris formunu HIC
// acmiyordu) somurunun yalnizca BIR varyantini modelledigini gosterdi.
// Asil somuru, saldirganin GET /giris ile KENDINE baglama mintlemesiydi;
// iki zamanlama varyantinin ikisi de canli jeton aldi. Asagidaki iki
// test o iki varyanti AYRI AYRI olcer.
//
// devralmaDogrula, her iki varyantta da ayni sonucu dogrular: saldirgan
// kod ALMAZ ve auth istegi kurbanin kimligine HIC baglanmaz.
//
// NEDEN "403" DEGIL "KOD YOK": saldirgan kendi istegine bagli oldugu
// icin /authorize/callback'te cerez kontrolunu GECER; kutuphane
// (pkg/op/auth_request.go AuthorizeCallback) istegi Done() olmadigi
// icin reddeder ve istemcinin redirect_uri'sine error=... ile 302
// doner. Olculmesi gereken sey durum kodu degil, KOD URETILMEDIGI ve
// istegin baglanmadigidir.
func devralmaDogrula(t *testing.T, o *akisOrtami, id string, cbYanit *http.Response) {
	t.Helper()
	if kod := kodCikar(t, cbYanit); kod != "" {
		t.Fatalf("HESAP DEVRALMA: saldirgan kod aldi (durum %d)", cbYanit.StatusCode)
	}
	istek, err := o.istekler.IDileOku(context.Background(), id)
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if istek.Done() {
		t.Fatal("HESAP DEVRALMA YOLU ACIK: saldirganin auth istegi bir kimlige baglandi")
	}
}

// SOMURU 1 (on-baglama). Saldirgan /authorize'i kendi S256 ciftiyle
// cagirir, SONRA kendi tarayicisinda GET /giris acar (giris YAPMAZ,
// yalnizca baglama almaya calisir), linki kurbana yollar; kurban AYRI
// bir cerez kabinda giris yapmaya calisir; saldirgan callback'i cagirir.
//
// Beklenti: kurbanin GET'i 403 (baglamasi yok, form gosterilmez),
// saldirgan kod ALMAZ. GET /giris yeniden baglama mintlerse bu test
// FAIL eder (mutasyon kaniti raporda).
func TestSomuru1OnBaglamaIleDevralinamaz(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)

	saldirgan := tarayici(t)
	kurban := tarayici(t)

	// 1) Saldirgan akisi baslatir (challenge/state SALDIRGANIN).
	id := authRequestIDCikar(t, al(t, saldirgan, o.authorizeURL(challenge, "S256")))

	// 2) ON-BAGLAMA: saldirgan formu KENDI tarayicisinda acar. Bu 200
	//    doner (akisi o baslatti, baglamasi var) ama giris YAPMAZ.
	saldirganForm := al(t, saldirgan, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(id))
	if saldirganForm.StatusCode != http.StatusOK {
		t.Fatalf("saldirganin kendi formu durumu = %d; senaryo kurulamadi", saldirganForm.StatusCode)
	}

	// 3) Kurban AYRI tarayicida ayni linki acar ve girisi dener.
	kurbanYanit, formDurum := o.girisDene(t, kurban, id, "kurban@ornek.com", "dogruSifre12")
	if formDurum != http.StatusForbidden {
		t.Errorf("kurbanin GET /giris durumu = %d, beklenen 403 (baglamasi yok, form gosterilmemeli)", formDurum)
	}
	if kurbanYanit.StatusCode == http.StatusFound {
		t.Error("kurbanin girisi saldirganin istegini tamamladi")
	}

	// 4) Saldirgan kodu KENDI tarayicisinda toplamaya calisir.
	devralmaDogrula(t, o, id, al(t, saldirgan, o.issuer+"/authorize/callback?id="+url.QueryEscape(id)))
}

// SOMURU 2 (giris sonrasi baglama). Ayni somuru, ama saldirgan
// baglamayi kurbanin girisi TAMAMLANDIKTAN SONRA almaya calisir:
// zamanlama sarti bile yok. Sira SOMURU 1'den FARKLI oldugu icin ayri
// bir testtir.
func TestSomuru2GirisSonrasiBaglamaIleDevralinamaz(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)

	saldirgan := tarayici(t)
	kurban := tarayici(t)

	// 1) Saldirgan akisi baslatir.
	id := authRequestIDCikar(t, al(t, saldirgan, o.authorizeURL(challenge, "S256")))

	// 2) Kurban ONCE giris yapmayi dener (saldirgan henuz formu
	//    acmamistir).
	kurbanYanit, formDurum := o.girisDene(t, kurban, id, "kurban@ornek.com", "dogruSifre12")
	if formDurum != http.StatusForbidden {
		t.Errorf("kurbanin GET /giris durumu = %d, beklenen 403", formDurum)
	}
	if kurbanYanit.StatusCode == http.StatusFound {
		t.Error("kurbanin girisi saldirganin istegini tamamladi")
	}

	// 3) Saldirgan SONRA formu acar (baglama almaya calisir) ve kodu
	//    toplar.
	al(t, saldirgan, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(id))
	devralmaDogrula(t, o, id, al(t, saldirgan, o.issuer+"/authorize/callback?id="+url.QueryEscape(id)))
}

// Akisi BASLATMAYAN bir tarayicida GET /giris form BILE gostermez.
// (Birim karsiligi sayfa_test.go TestFormGetBaglamasizReddedilir;
// buradaki uretim mux'u uzerinden kosar.)
func TestGetGirisBaglamasizUretimMuxundaReddedilir(t *testing.T) {
	o := testSunucu(t)
	_, challenge := pkceS256(t)
	baslatan := tarayici(t)
	yabanci := tarayici(t)

	id := authRequestIDCikar(t, al(t, baslatan, o.authorizeURL(challenge, "S256")))

	yanit := al(t, yabanci, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(id))
	if yanit.StatusCode != http.StatusForbidden {
		t.Fatalf("yabanci tarayici GET /giris durumu = %d, beklenen 403", yanit.StatusCode)
	}
	if strings.Contains(govde(t, yanit), `name="csrf"`) {
		t.Error("reddedilen GET formu gosterdi")
	}

	// NEGATIF KONTROL: akisi baslatan tarayicida AYNI link 200 doner,
	// yani 403 baglama yuzunden (uc tumuyle kapanmis degil).
	if kendi := al(t, baslatan, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(id)); kendi.StatusCode != http.StatusOK {
		t.Errorf("baslatan tarayicida GET /giris durumu = %d, beklenen 200", kendi.StatusCode)
	}
}

// PARALEL AKIS: ayni tarayicida (ayni cerez kabi) iki /authorize
// baslatilir; IKISININ de POST'u ve callback'i calismali.
//
// Bu testin yakaladigi sey: baglama /authorize'a tasinirken cerez ->
// TEK id modeli korunsa ikinci /authorize birincinin baglamasini EZER
// ve birinci sekme 403 olurdu (devralma yerine DoS). Bkz.
// oturumBaglamaUstSinir.
func TestParalelIkiAkisAyniTarayicidaCalisir(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	c := tarayici(t)

	verifier1, challenge1 := pkceS256(t)
	verifier2, challenge2 := pkceS256(t)

	// Iki sekme: ikisi de giris yapilmadan ONCE baslatilir.
	id1 := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge1, "S256")))
	id2 := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge2, "S256")))
	if id1 == id2 {
		t.Fatal("iki /authorize ayni authRequestID uretti")
	}

	// Sekme 1 ONCE tamamlanir: ikinci /authorize onun baglamasini
	// ezmemis olmali.
	for _, d := range []struct {
		ad       string
		id       string
		verifier string
	}{{"sekme-1", id1, verifier1}, {"sekme-2", id2, verifier2}} {
		t.Run(d.ad, func(t *testing.T) {
			girisYanit := o.girisYap(t, c, d.id, "kurban@ornek.com", "dogruSifre12")
			if girisYanit.StatusCode != http.StatusFound {
				t.Fatalf("POST /giris durumu = %d, beklenen 302. govde: %s",
					girisYanit.StatusCode, govde(t, girisYanit))
			}
			cbYanit := al(t, c, girisYanit.Header.Get("Location"))
			kod := kodCikar(t, cbYanit)
			if kod == "" {
				t.Fatalf("kod uretilmedi (callback durumu %d)", cbYanit.StatusCode)
			}
			durum, jeton := o.jetonAl(t, kod, d.verifier)
			if durum != http.StatusOK {
				t.Fatalf("token durumu = %d, govde: %v", durum, jeton)
			}
			if jeton["access_token"] == nil || jeton["access_token"] == "" {
				t.Error("access_token yok")
			}
		})
	}
}

// Kurbanin KENDI tarayicisi akisi tamamlayabilmeli: baglama yalnizca
// yabancilari keser, mesru akisi kesmez.
func TestKendiTarayicisiAkisiTamamlar(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)
	c := tarayici(t)

	id := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge, "S256")))
	girisYanit := o.girisYap(t, c, id, "kurban@ornek.com", "dogruSifre12")
	if girisYanit.StatusCode != http.StatusFound {
		t.Fatalf("giris durumu = %d", girisYanit.StatusCode)
	}
	cbYanit := al(t, c, girisYanit.Header.Get("Location"))
	if cbYanit.StatusCode != http.StatusFound {
		t.Fatalf("callback durumu = %d, beklenen 302", cbYanit.StatusCode)
	}
	konum, _ := url.Parse(cbYanit.Header.Get("Location"))
	if konum.Query().Get("code") == "" {
		t.Error("kendi tarayicisinda kod uretilmedi; mesru akis kirildi")
	}
}

// ---- C2: PKCE S256 zorunlu ----

// pkceRed, /authorize'in istegi reddettigini dogrular: ya 400 doner ya
// da istemcinin redirect_uri'sine error=invalid_request ile 302 doner.
// HER IKI durumda da giris sayfasina GIDILMEMELI (kullanici sifresini
// bos yere girmemeli).
func pkceRed(t *testing.T, yanit *http.Response) {
	t.Helper()
	switch yanit.StatusCode {
	case http.StatusFound:
		konum := yanit.Header.Get("Location")
		if strings.Contains(konum, yolGiris) {
			t.Fatalf("istek giris sayfasina yonlendirildi: %q", konum)
		}
		if !strings.Contains(konum, "error=invalid_request") {
			t.Fatalf("Location'da error=invalid_request yok: %q", konum)
		}
	case http.StatusBadRequest:
		// Kabul: dogrudan hata.
	default:
		t.Fatalf("durum = %d, red bekleniyordu", yanit.StatusCode)
	}
}

// C2: code_challenge HIC yoksa /authorize REDDEDILIR. Ayni zamanda
// M1: eskiden 302 ile giris sayfasina gidiliyor, kullanici sifresini
// giriyor ve hata ancak token ucunda ortaya cikiyordu.
func TestAuthorizeCodeChallengesizReddedilir(t *testing.T) {
	o := testSunucu(t)
	c := tarayici(t)
	pkceRed(t, al(t, c, o.authorizeURL("", "")))
}

// C2: code_challenge var ama method=plain -> REDDEDILIR. plain'de
// challenge == verifier'dir; PKCE fiilen devre disidir.
func TestAuthorizePlainReddedilir(t *testing.T) {
	o := testSunucu(t)
	c := tarayici(t)
	verifier, _ := pkceS256(t)
	pkceRed(t, al(t, c, o.authorizeURL(verifier, "plain")))
}

// C2: method HIC verilmemis (bos string) -> REDDEDILIR. Kutuphane bos
// method'u duz karsilastirma olarak isler.
func TestAuthorizeMethodsuzReddedilir(t *testing.T) {
	o := testSunucu(t)
	c := tarayici(t)
	verifier, _ := pkceS256(t)
	pkceRed(t, al(t, c, o.authorizeURL(verifier, "")))
}

// C2: bilinmeyen bir method da REDDEDILIR.
func TestAuthorizeBilinmeyenMethodReddedilir(t *testing.T) {
	o := testSunucu(t)
	c := tarayici(t)
	_, challenge := pkceS256(t)
	pkceRed(t, al(t, c, o.authorizeURL(challenge, "S512")))
}

// C2: method=S256 + gecerli challenge CALISIR (giris sayfasina gider).
func TestAuthorizeS256Calisir(t *testing.T) {
	o := testSunucu(t)
	c := tarayici(t)
	_, challenge := pkceS256(t)
	if id := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge, "S256"))); id == "" {
		t.Error("authRequestID bos")
	}
}

// C2 regresyon kilidi: S256 akisinda YANLIS code_verifier ile jeton
// alinamaz.
func TestYanlisVerifierReddedilir(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)
	c := tarayici(t)

	id := authRequestIDCikar(t, al(t, c, o.authorizeURL(challenge, "S256")))
	girisYanit := o.girisYap(t, c, id, "kurban@ornek.com", "dogruSifre12")
	cbYanit := al(t, c, girisYanit.Header.Get("Location"))
	konum, _ := url.Parse(cbYanit.Header.Get("Location"))
	kod := konum.Query().Get("code")
	if kod == "" {
		t.Fatal("kod uretilmedi")
	}

	yanlisVerifier, _ := pkceS256(t)
	durum, jetonGovde := o.jetonAl(t, kod, yanlisVerifier)
	if durum == http.StatusOK {
		t.Fatalf("yanlis code_verifier ile jeton verildi: %v", jetonGovde)
	}
}

// C2 IKINCI KATMAN: /authorize kontrolu atlansa bile token ucu
// korumali. GetCodeChallenge fail-closed oldugu icin, S256 OLMAYAN bir
// challenge ile DOGRUDAN depoya yazilmis (yani /authorize kontrolunden
// gecmemis) bir istegin kodu jetona cevrilemez. Fail-closed kaldirilirsa
// bu test FAIL eder.
func TestPlainChallengeTokenUcundaReddedilir(t *testing.T) {
	o := testSunucu(t)
	ctx := context.Background()
	kullanici := o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")

	// /authorize'i BYPASS ederek plain challenge'li bir istek yaz.
	verifier, _ := pkceS256(t)
	istek, err := o.istekler.Olustur(ctx, &oidc.AuthRequest{
		ClientID:            "makesinger-mobil",
		RedirectURI:         testRedirectURI,
		Scopes:              oidc.SpaceDelimitedArray{oidc.ScopeOpenID},
		ResponseType:        oidc.ResponseTypeCode,
		CodeChallenge:       verifier, // plain'de challenge == verifier
		CodeChallengeMethod: oidc.CodeChallengeMethodPlain,
		State:               "durum-1",
	}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := o.istekler.TamamlandiIsaretle(ctx, istek.GetID(), kullanici.ID); err != nil {
		t.Fatalf("TamamlandiIsaretle: %v", err)
	}
	const kod = "test-kod-plain"
	if err := o.istekler.KodKaydet(ctx, istek.GetID(), kod); err != nil {
		t.Fatalf("KodKaydet: %v", err)
	}

	durum, jetonGovde := o.jetonAl(t, kod, verifier)
	if durum == http.StatusOK {
		t.Fatalf("plain challenge ile jeton verildi: %v", jetonGovde)
	}
}

// Discovery, yalnizca S256 ilan etmeli ve ilan ettigi sey fiilen
// zorlanan sey olmali (bkz. pkce.go).
func TestDiscoveryYalnizcaS256IlanEder(t *testing.T) {
	o := testSunucu(t)
	yanit := al(t, tarayici(t), o.issuer+"/.well-known/openid-configuration")
	if yanit.StatusCode != http.StatusOK {
		t.Fatalf("discovery durumu = %d", yanit.StatusCode)
	}
	var belge struct {
		Methods []string `json:"code_challenge_methods_supported"`
	}
	if err := json.Unmarshal([]byte(govde(t, yanit)), &belge); err != nil {
		t.Fatalf("discovery cozulemedi: %v", err)
	}
	if len(belge.Methods) != 1 || belge.Methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, beklenen [S256]", belge.Methods)
	}
}

// M3: YAYINLANAN discovery belgesi desteklenmeyen akislari ilan
// ETMEMELI. Inceleyici bunlarin hepsini canli denedi ve hepsi dogru
// reddediliyor (guvenlik sorunu yok), ama metadata'ya guvenip implicit
// deneyen uyumlu bir RP gereksiz yere hata alirdi.
//
// Bu test, discoveryDuzelt'in gercekten YAYIN YOLUNDA oldugunu olcer:
// belge kutuphanenin kendi op.CreateDiscoveryConfig'inden gecip bizim
// mux'umuz uzerinden doner.
func TestDiscoveryDesteklenmeyenAkislariIlanEtmez(t *testing.T) {
	o := testSunucu(t)
	yanit := al(t, tarayici(t), o.issuer+"/.well-known/openid-configuration")
	if yanit.StatusCode != http.StatusOK {
		t.Fatalf("discovery durumu = %d", yanit.StatusCode)
	}
	var belge struct {
		ResponseTypes []string `json:"response_types_supported"`
		GrantTypes    []string `json:"grant_types_supported"`
		DeviceUcu     string   `json:"device_authorization_endpoint"`
		Issuer        string   `json:"issuer"`
		TokenUcu      string   `json:"token_endpoint"`
	}
	ham := govde(t, yanit)
	if err := json.Unmarshal([]byte(ham), &belge); err != nil {
		t.Fatalf("discovery cozulemedi: %v", err)
	}

	if len(belge.ResponseTypes) != 1 || belge.ResponseTypes[0] != "code" {
		t.Errorf("response_types_supported = %v, beklenen [code]", belge.ResponseTypes)
	}
	for _, yasak := range []string{"implicit", "urn:ietf:params:oauth:grant-type:jwt-bearer", "urn:ietf:params:oauth:grant-type:device_code"} {
		for _, g := range belge.GrantTypes {
			if g == yasak {
				t.Errorf("grant_types_supported desteklenmeyen %q iceriyor: %v", yasak, belge.GrantTypes)
			}
		}
	}
	if belge.DeviceUcu != "" {
		t.Errorf("device_authorization_endpoint ilan edildi: %q", belge.DeviceUcu)
	}

	// Belgenin GERI KALANI bozulmamis olmali: issuer ve token ucu
	// kutuphanenin urettigi degerler, istek baglamindan cozulen issuer
	// ile birlikte dogru gelmeli (araci.Handler sarmasi calisiyor mu).
	if belge.Issuer != o.issuer {
		t.Errorf("issuer = %q, beklenen %q", belge.Issuer, o.issuer)
	}
	if belge.TokenUcu != o.issuer+"/oauth/token" {
		t.Errorf("token_endpoint = %q, beklenen %q", belge.TokenUcu, o.issuer+"/oauth/token")
	}
}

// ---- LOGIN-CSRF: /authorize'a capraz-siteden PKCE enjeksiyonu ----

// alBaslikli, al'in fetch-metadata basliklari eklenen surumudur.
// fetchSite bos gecilirse baslik HIC EKLENMEZ.
func alBaslikli(t *testing.T, c *http.Client, adres, fetchSite string) *http.Response {
	t.Helper()
	istek, err := http.NewRequestWithContext(t.Context(), http.MethodGet, adres, nil)
	if err != nil {
		t.Fatalf("istek kurulamadi: %v", err)
	}
	if fetchSite != "" {
		istek.Header.Set(baslikFetchSite, fetchSite)
		istek.Header.Set(baslikFetchMode, "navigate")
	}
	yanit, err := c.Do(istek)
	if err != nil {
		t.Fatalf("GET basarisiz: %v", err)
	}
	t.Cleanup(func() { _ = yanit.Body.Close() })
	return yanit
}

// LOGIN-CSRF SOMURUSU (inceleyicinin senaryosu, birebir): saldirgan
// kurbani kendi hazirladigi /authorize adresine yonlendirir. Oturum
// cerezi SameSite=Lax oldugu icin ust-duzey navigasyonda TASINIR, yani
// kurbanin MEVCUT cerezi istege gider. Duzeltme oncesinde saldirganin
// code_challenge'i ile dogan authRequestID kurbanin baglama kumesine
// eklenirdi; kurban formu acip giris yapar, uretilen kodun
// code_verifier'i ise SALDIRGANDA olurdu (PKCE'nin korudugu TEK
// senaryo, RFC 8252 bolum 8.1, cokerdi).
//
// Simdi: capraz-site navigasyonda yeni baglama EKLENMEZ, kurban o id
// icin 403 alir ve istek TAMAMLANAMAZ — saldirganin verifier'ini
// bilmesi ise yaramaz.
func TestLoginCSRFCaprazSiteEnjeksiyonuTamamlanamaz(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	kurban := tarayici(t)

	// 1) Kurbanin MESRU akisi: native uygulama sistem tarayicisini acar
	// (Sec-Fetch-Site: none). Cerez ve baglama burada dogar.
	kurbanVerifier, kurbanChallenge := pkceS256(t)
	mesruID := authRequestIDCikar(t, alBaslikli(t, kurban, o.authorizeURL(kurbanChallenge, "S256"), fetchSiteYok))

	// 2) Saldirganin hazirladigi adres, KURBANIN tarayicisinda
	// capraz-site ust-duzey navigasyonla acilir. code_challenge
	// SALDIRGANIN; verifier'i yalnizca o biliyor.
	// Verifier'i KASITLI olarak tutmuyoruz: kanit, saldirganin onu
	// bilmesinin ise yaramamasi — asagida HIC kod uretilmedigi icin
	// degistirecek bir sey YOK (adim 4).
	_, saldirganChallenge := pkceS256(t)
	enjekteID := authRequestIDCikar(t, alBaslikli(t, kurban, o.authorizeURL(saldirganChallenge, "S256"), fetchSiteCapraz))
	if enjekteID == mesruID {
		t.Fatal("iki /authorize ayni authRequestID uretti")
	}

	// 3) Kurban enjekte id ile formu ACAMAZ: baglama kurulmadi -> 403.
	_, formDurum := o.girisDene(t, kurban, enjekteID, "kurban@ornek.com", "dogruSifre12")
	if formDurum != http.StatusForbidden {
		t.Fatalf("LOGIN-CSRF ACIK: enjekte id icin GET /giris durumu = %d, beklenen 403", formDurum)
	}

	// 4) Dolayisiyla saldirganin istegi icin KOD URETILMEZ ve istek
	// hicbir kimlige BAGLANMAZ: saldirganin verifier'i ise yaramaz.
	devralmaDogrula(t, o, enjekteID,
		al(t, kurban, o.issuer+"/authorize/callback?id="+url.QueryEscape(enjekteID)))

	// 5) KURBANIN MESRU AKISI BOZULMADI (kontrol DoS'a cevrilmedi):
	// ayni cerez kabiyla ucan uca tamamlanir ve jeton alir.
	csrf := o.girisFormuAc(t, kurban, mesruID)
	girisYanit := o.girisGonder(t, kurban, mesruID, csrf, "kurban@ornek.com", "dogruSifre12")
	if girisYanit.StatusCode != http.StatusFound {
		t.Fatalf("kurbanin mesru POST /giris durumu = %d, beklenen 302", girisYanit.StatusCode)
	}
	kod := kodCikar(t, al(t, kurban, girisYanit.Header.Get("Location")))
	if kod == "" {
		t.Fatal("kurbanin mesru akisi kod uretmedi: kontrol mesru akisi kirdi")
	}
	durum, jeton := o.jetonAl(t, kod, kurbanVerifier)
	if durum != http.StatusOK {
		t.Fatalf("kurbanin jeton degisimi durumu = %d, govde: %v", durum, jeton)
	}
	if jeton["access_token"] == nil || jeton["access_token"] == "" {
		t.Error("kurbanin mesru akisinda access_token yok")
	}
}

// Capraz-site istek, kurbanin MEVCUT baglamalarini ve cerezini
// BOZMAZ: kontrol yalnizca "yeni baglama eklememek"tir, cerez
// yenilenmez veya kume temizlenmez. Iki paralel mesru akis, aralarina
// giren capraz-site istekten SONRA da tamamlanabilmeli.
func TestCaprazSiteIstekMevcutBaglamalariBozmaz(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	kurban := tarayici(t)

	_, challenge1 := pkceS256(t)
	_, challenge2 := pkceS256(t)
	id1 := authRequestIDCikar(t, alBaslikli(t, kurban, o.authorizeURL(challenge1, "S256"), fetchSiteYok))
	id2 := authRequestIDCikar(t, alBaslikli(t, kurban, o.authorizeURL(challenge2, "S256"), fetchSiteYok))

	// Araya capraz-site bir istek girer.
	_, saldirganChallenge := pkceS256(t)
	enjekteID := authRequestIDCikar(t, alBaslikli(t, kurban, o.authorizeURL(saldirganChallenge, "S256"), fetchSiteCapraz))
	if g := al(t, kurban, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(enjekteID)); g.StatusCode != http.StatusForbidden {
		t.Fatalf("enjekte id icin GET /giris durumu = %d, beklenen 403", g.StatusCode)
	}

	// Iki mesru sekme de hala formunu acip girisi tamamlayabilmeli.
	for _, id := range []string{id1, id2} {
		csrf := o.girisFormuAc(t, kurban, id)
		yanit := o.girisGonder(t, kurban, id, csrf, "kurban@ornek.com", "dogruSifre12")
		if yanit.StatusCode != http.StatusFound {
			t.Fatalf("mesru sekme POST /giris durumu = %d, beklenen 302", yanit.StatusCode)
		}
		if kod := kodCikar(t, al(t, kurban, yanit.Header.Get("Location"))); kod == "" {
			t.Fatal("mesru sekme kod uretmedi: capraz-site istek baglamayi bozdu")
		}
	}
}
