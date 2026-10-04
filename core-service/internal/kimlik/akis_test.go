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
	saglayici, err := op.NewOpenIDProvider(issuer, opCfg, depo, op.WithAllowInsecure())
	if err != nil {
		t.Fatalf("OpenIDProvider: %v", err)
	}

	// cerezGuvenli=false: test dinleyicisi http, Secure cerez
	// tasinmazdi.
	srv := &http.Server{
		Handler:           muxKur(saglayici, kullanici, istekler, false),
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

// girisYap, verilen tarayicida giris formunu ACAR (cerez burada
// kurulur) ve formu gonderir. Donen yanit POST /giris yanitidir.
func (o *akisOrtami) girisYap(t *testing.T, c *http.Client, id, eposta, sifre string) *http.Response {
	t.Helper()
	formYanit := al(t, c, o.issuer+yolGiris+"?authRequestID="+url.QueryEscape(id))
	if formYanit.StatusCode != http.StatusOK {
		t.Fatalf("giris formu durumu = %d", formYanit.StatusCode)
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
	return yanit
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

// SALDIRI TESTI (C1). A istemcisi (saldirgan) kendi PKCE ciftiyle
// /authorize baslatir ve /giris linkini "kurbana" yollar. B istemcisi
// (kurban, TAMAMEN AYRI bir cerez kabi) kendi e-postasi ve sifresiyle
// giris yapar. Sonra A, /authorize/callback'i KENDI tarayicisinda
// cagirir.
//
// Beklenti: A kod ALMAZ. Cerez baglamasi kaldirilirsa bu test FAIL
// eder (mutasyon kaniti raporda).
func TestSaldirganKurbaninGirisindenKodAlamaz(t *testing.T) {
	o := testSunucu(t)
	o.testKullanici(t, "kurban@ornek.com", "dogruSifre12")
	_, challenge := pkceS256(t)

	saldirgan := tarayici(t)
	kurban := tarayici(t)

	// 1) Saldirgan akisi baslatir (challenge/state SALDIRGANIN).
	id := authRequestIDCikar(t, al(t, saldirgan, o.authorizeURL(challenge, "S256")))

	// 2) Kurban, AYRI bir istemcide girisi tamamlar. Giris basarili
	//    olabilir (kendi cerezini alir) ama bu saldirgana yaramamali.
	kurbanYanit := o.girisYap(t, kurban, id, "kurban@ornek.com", "dogruSifre12")
	if kurbanYanit.StatusCode != http.StatusFound {
		t.Fatalf("kurbanin girisi 302 donmedi (durum %d); saldiri senaryosu kurulamadi",
			kurbanYanit.StatusCode)
	}

	// 3) Saldirgan kodu KENDI tarayicisinda toplamaya calisir.
	cbYanit := al(t, saldirgan, o.issuer+"/authorize/callback?id="+url.QueryEscape(id))

	if cbYanit.StatusCode == http.StatusFound {
		konum, err := url.Parse(cbYanit.Header.Get("Location"))
		if err == nil && konum.Query().Get("code") != "" {
			t.Fatalf("HESAP DEVRALMA: saldirgan kurbanin girisinden kod aldi (durum %d)",
				cbYanit.StatusCode)
		}
		t.Fatalf("saldirgana 302 donuldu (kod yok ama red bekleniyordu): %q",
			cbYanit.Header.Get("Location"))
	}
	if cbYanit.StatusCode != http.StatusForbidden {
		t.Errorf("callback durumu = %d, beklenen 403", cbYanit.StatusCode)
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
