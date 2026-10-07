package kimlik

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// Federasyon uclarinin yollari. {saglayici} yol degeri YALNIZCA akisi
// BASLATIRKEN kullanilir; geri donuste saglayici state KAYDINDAN
// okunur (bkz. Geri).
const (
	yolFederasyonBaslat = "/federasyon/{saglayici}/baslat"
	yolFederasyonGeri   = "/federasyon/{saglayici}/geri"
)

// saglayiciYerTutucu, yukaridaki desenlerde saglayici adinin yerini
// tutan ServeMux yol degeridir.
const saglayiciYerTutucu = "{saglayici}"

// federasyonBaslatYolu, bir saglayicinin baslatma ucunun GERCEK yolunu
// uretir.
//
// NEDEN BU YARDIMCI VAR: giris sayfasindaki buton yolu (bkz.
// Sayfalar.federasyonButonlari) ile mux'a baglanan uc yolu AYNI olmak
// zorunda. Ikisi ayri ayri elle kurulurken uc yolunun yeniden
// adlandirilmasi butonlari sessizce eski yola, yani 404'e birakiyordu.
// Buton yolu artik ucun KENDI sabitinden turetiliyor: tek dogruluk
// kaynagi yolFederasyonBaslat'tir. Yardimci tercih edildi (sabiti
// cagiran yerde elle Replace etmek yerine) cunku yer tutucunun bicimi
// de boylece tek yerde kaliyor.
func federasyonBaslatYolu(saglayici string) string {
	return strings.Replace(yolFederasyonBaslat, saglayiciYerTutucu, saglayici, 1)
}

// cerezAdFederasyon, federasyon akisini BASLATAN tarayiciya verilen
// cerezin adidir (K2).
//
// NEDEN AYRI BIR CEREZ: Apple name/email kapsami istenince yanitini
// response_mode=form_post ile, yani Apple'in origin'inden BIZE
// CAPRAZ-SITE bir POST olarak doner. Faz 1'in oturum cerezi
// (cerezAdOturum) SameSite=Lax'tir ve tarayici onu o POST'ta
// GONDERMEZ: Faz 1'in baglama kontrolu federasyon donusunde fiilen HIC
// KOSMAZ. Bu yuzden federasyon akisi SameSite=None olan kendi cerezini
// kullanir. Oturum cerezi DEGISTIRILMEZ: onu None yapmak Faz 1'in
// login-CSRF korumasini zayiflatirdi.
const cerezAdFederasyon = "kimlik_fed"

// hataIstekGecersiz, federasyon akisi var olmayan (veya suresi dolmus)
// bir auth istegi icin baslatilmak istendiginde gosterilir.
const hataIstekGecersiz = "Giris istegi gecersiz, lutfen uygulamadan tekrar deneyin."

// hataIstekSuresiDoldu, bekleyen auth isteginin TTL ile Redis'ten
// dusmesi durumunda gosterilir.
//
// NEDEN AYRI BIR SINIF: istekTTL (10dk) sayaci EN BASTA, /authorize'da
// baslar ve hicbir yolda uzatilmaz; federasyonDurumTTL ve
// baglamaBeklemeTTL de 10dk'dir ama SIRALI tuketilir. Yani federasyon
// zinciri, isaret ettigi auth istegini GECEBILIR: T=9dk50sn'de Baslat
// gecer (istek VAR), kullanici yukari akista 30 saniye gecirir, T=10dk20sn'de
// Geri'de TamamlandiIsaretle ErrIstekYok doner. Bu SIRADAN bir
// suresi-dolmadir; eskiden hataSunucu'ya dusup 500 ureterek gercek
// sunucu hatalariyla AYNI alarmi caliyordu ve kullaniciya ne oldugunu
// soylemiyordu. TTL degerleri BILEREK degistirilmedi: istekTTL'i
// buyutmek bir UX sorunu icin bir guvenlik penceresini genisletmek
// olurdu.
const hataIstekSuresiDoldu = "Giris isteginin suresi doldu. Lutfen uygulamaya donup girisi yeniden baslatin."

// hataEpostaYok, yukari akis e-posta gondermedigi icin yeni hesap
// acilamadiginda gosterilir (bkz. ErrYukariAkisEpostaYok). Mesaj
// kullaniciya NE YAPACAGINI soyler; "bir hata olustu" demek kullaniciyi
// sonsuza kadar ayni butona basmaya birakirdi.
const hataEpostaYok = "Saglayici e-posta adresinizi bize vermedigi icin yeni bir hesap olusturulamadi. Lutfen e-posta ve sifre ile giris yapin ya da diger giris yontemini kullanin."

// federasyonZamanAsimi, yukari akisa yapilan HTTP cagrilarinin ust
// siniri. JetonDegis kendi ic zaman asimini da uygular; bu, istemci
// seviyesindeki ikinci kemerdir.
const federasyonZamanAsimi = 15 * time.Second

// FedereIstekDepo, federasyon akisinin bekleyen auth istegine ihtiyac
// duydugu IKI islemi tanimlar: Baslat istegin GERCEKTEN var oldugunu
// dogrulamak icin okur, Geri ise akis bitince onu kullaniciya baglar.
// Uretimde *IstekDepo karsilar; testler bellek ici bir karsilikla
// agsiz koshar.
type FedereIstekDepo interface {
	IstekTamamlayici

	// IDileOku, bekleyen auth istegini doner; yoksa ErrIstekYok.
	IDileOku(ctx context.Context, id string) (*AuthIstek, error)
}

// FederasyonDepo, federasyon akisinin Redis yuzeyinin TAMAMIDIR:
// Gorev 3'un tek kullanimlik state kaydi (DurumDepo) ve bekleyen
// baglama kaydi (BeklemeDepo, bkz. baglama_sayfa.go). Ikisi de ayni
// akisin kisa omurlu durumudur ve uretimde ayni *IstekDepo tarafindan
// karsilanir; ayri ayri kablolamak iki yerde ayri bozulabilen bir
// kurulum demek olurdu.
type FederasyonDepo interface {
	DurumDepo
	BeklemeDepo
}

// FedereSayfalar, bizim OP'umuzun bir RP olarak Apple/Google'a gidip
// donen bacaklarini tasir.
//
// GUVENLIK UCGENI (Faz 1'in C1'ini federasyonda yeniden acmamak icin):
//
//	state  -> TEK KULLANIMLIK (Redis GETDEL, bkz. DurumAl); yakalanan
//	          bir callback ikinci kez oynatilamaz.
//	cerez  -> akisi BASLATAN tarayici ile TAMAMLAYAN tarayicinin ayni
//	          olmasini zorunlu kilar (K2).
//	nonce  -> saglayicinin donen id_token'inin BU akis icin verildigini
//	          kanitlar (bkz. auth.DogrulaNonce).
//
// Ucunden biri dusunce akis durur; hicbiri digerinin yerini tutmaz.
type FedereSayfalar struct {
	akislar        map[string]*YukariAkis
	dogrulayicilar map[string]*auth.YukariAkisDogrulayici
	durum          FederasyonDepo
	federe         FedereDepo
	kullanici      UserStore
	istekler       FedereIstekDepo
	oturumlar      OturumDepo
	geriCagirma    func(context.Context, string) string
	issuer         string
	cerezGuvenli   bool

	// istemci, yukari akis token ucuna yapilan cagrilarda kullanilir.
	// Alan olarak tutulur ki testler httptest istemcisini gecirebilsin.
	istemci *http.Client

	// hiz, HESAP BASINA sifre deneme sayaci icin /baglama handler'inda
	// kullanilir; BaglamaBagla tarafindan kurulur ve nil OLABILIR
	// (yalnizca birim testler).
	hiz *HizLimit
}

// NewFedereSayfalar, federasyon uclarini kurar.
//
// dogrulayicilar, saglayici adindan Gorev 2'nin yukari akis
// dogrulayicisina esler: id_token'in imzasi, iss'i, aud'u ve nonce'u
// ORADA dogrulanir, burada yeniden dogrulanmaz.
//
// oturumlar, YALNIZCA /baglama sayfasinin Faz 1 ile ayni oturum
// baglamasini zorunlu kilmasi icindir (bkz. baglama_sayfa.go);
// federasyon donusu kendi cerezini kullanir.
//
// cerezGuvenli, Faz 1 ile ayni kuraldir: issuer https ise true,
// http:// yerel gelistirmede false (aksi halde tarayici Secure cerezi
// hic saklamaz ve yerel akis kirilir).
func NewFedereSayfalar(
	akislar map[string]*YukariAkis,
	dogrulayicilar map[string]*auth.YukariAkisDogrulayici,
	durum FederasyonDepo,
	federe FedereDepo,
	kullanici UserStore,
	istekler FedereIstekDepo,
	oturumlar OturumDepo,
	geriCagirma func(context.Context, string) string,
	issuer string,
	cerezGuvenli bool,
) *FedereSayfalar {
	return &FedereSayfalar{
		akislar:        akislar,
		dogrulayicilar: dogrulayicilar,
		durum:          durum,
		federe:         federe,
		kullanici:      kullanici,
		istekler:       istekler,
		oturumlar:      oturumlar,
		geriCagirma:    geriCagirma,
		issuer:         issuer,
		cerezGuvenli:   cerezGuvenli,
		istemci:        &http.Client{Timeout: federasyonZamanAsimi},
	}
}

// Bagla, federasyon uclarini mux'a baglar.
//
// Geri, issuer aracisiyla SARILIR: geriCagirma (op.AuthCallbackURL)
// issuer'i istek baglamindan okur, sarmadan baglamak akisi yonlendirme
// asamasinda kirar. Baslat SARILMAZ: geriCagirma cagirmaz.
//
// POST /federasyon/{saglayici}/geri ZORUNLU: Apple name/email kapsami
// istenince yanitini form_post ile doner (K2).
//
// hiz nil GECILEBILIR ve o zaman hiz limiti UYGULANMAZ; bu yalnizca
// birim testler icindir (Faz 1'deki Sayfalar.Bagla ile ayni kural).
func (f *FedereSayfalar) Bagla(mux *http.ServeMux, araci *op.IssuerInterceptor, hiz *HizLimit) {
	var baslat http.Handler = http.HandlerFunc(f.Baslat)
	geri := araci.Handler(http.HandlerFunc(f.Geri))
	if hiz != nil {
		// Baslat, kimlik dogrulamasiz bir uctur ve her kabul edilen
		// istek Redis'e bir state kaydi yazar; kendi sayacini hak
		// ediyor (bkz. HizLimit.FederasyonBaslatSar).
		baslat = hiz.FederasyonBaslatSar(baslat)
		geri = hiz.FederasyonGeriSar(geri)
	}
	mux.Handle("GET "+yolFederasyonBaslat, baslat)
	mux.Handle("GET "+yolFederasyonGeri, geri)
	mux.Handle("POST "+yolFederasyonGeri, geri)
}

// geriURL, saglayiciya verilecek redirect_uri'yi uretir. Saglayici
// tarafinda ayniyla kayitli olmak zorundadir, bu yuzden issuer'dan
// deterministik olarak kurulur ve elle string birlestirme yerine
// url.JoinPath kullanilir.
func (f *FedereSayfalar) geriURL(saglayici string) string {
	taban := strings.TrimSuffix(f.issuer, "/")
	return taban + "/federasyon/" + url.PathEscape(saglayici) + "/geri"
}

// federasyonCerezi: K2. SameSite=None ZORUNLU cunku Apple form_post
// ile CAPRAZ-SITE POST yapar ve Lax cerez o POST'ta gonderilmez.
// HttpOnly ve Secure'dan vazgecilmez; Path federasyon altina SINIRLANIR
// ki oturum cerezinin yuzeyini genisletmesin.
//
// YEREL GELISTIRME TUZAGI: cerezGuvenli, http:// issuer'da false'tur
// (bkz. NewFedereSayfalar) ve SameSite=None + Secure OLMAYAN bir cerezi
// MODERN TARAYICILAR REDDEDER. Yani yerelde bu cerez hic saklanmaz ve
// geri donus "cerez baglamasi tutmadi" ile 403 verir. Pratikte
// uretimi etkilemez: Apple ve Google redirect_uri'nin https olmasini
// zorunlu kilar, yani federasyon akisi yerelde ZATEN elle denenemez.
// Kod BILEREK degistirilmedi -- Secure'u http'de true yapmak cerezi
// baska bir sekilde (bu kez her ortamda) kirar; yerelde denemek isteyen
// kisinin bilmesi gereken sey, bu akisin https bir issuer gerektirdigidir.
func (f *FedereSayfalar) federasyonCerezi(deger string) *http.Cookie {
	return &http.Cookie{
		Name:     cerezAdFederasyon,
		Value:    deger,
		Path:     "/federasyon",
		HttpOnly: true,
		Secure:   f.cerezGuvenli,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   int(federasyonDurumTTL.Seconds()),
	}
}

// federasyonCereziSil, kimlik_fed cerezini SILEN cerezi uretir.
//
// Ozniteliklerin yazma anindakiyle birebir ayni olmasi ZORUNLU
// (ozellikle Path ve SameSite/Secure): tarayici cerezi ad+Path+Domain
// ucluyle esler ve uyusmayan bir silme cerezi ESKISINI SILMEZ, yanina
// IKINCI bir cerez yazar. Bu yuzden deger federasyonCerezi'nden
// turetilir, elle yeniden kurulmaz.
func (f *FedereSayfalar) federasyonCereziSil() *http.Cookie {
	c := f.federasyonCerezi("")
	// MaxAge < 0 "simdi sil" demektir (net/http bunu Max-Age=0 olarak
	// yazar); MaxAge = 0 ise "oznitelik yok" anlamina gelir ve cerez
	// oturum cerezi olarak YASAMAYA DEVAM EDER.
	c.MaxAge = -1
	return c
}

// hataSayfasi, federasyon akisindaki hatalari tek bicimli yazar. Metin
// SINIF bilgisi tasir, deger tasimaz: hangi kontrolun (state / cerez /
// nonce / imza) tutmadigini ayirmak saldirgana yol gosterir.
func (f *FedereSayfalar) hataSayfasi(w http.ResponseWriter, kod int, metin string) {
	http.Error(w, metin, kod)
}

// istekTamamlamaHatasi, TamamlandiIsaretle'nin hatasini UC SINIFA ayirir
// ve tek bicimli yaniti yazar. Iki bacakta da (Geri ve baglamaPost) ayni
// siniflandirma kullanilir; eskiden yalnizca ErrIstekZatenTamamlandi
// ayriliyordu ve geri kalan HER SEY, siradan bir suresi-dolma dahil,
// 500 uretiyordu.
//
//	ErrIstekZatenTamamlandi -> 409: istek baska bir kimlige bagli,
//	                           Subject EZILMEZ.
//	ErrIstekYok             -> 400: kayit TTL ile dustu. Kullaniciya ne
//	                           oldugu ve ne yapacagi soylenir.
//	digerleri               -> 500: GERCEK sunucu hatasi. Suresi-dolmayi
//	                           buradan cikarmak, gercek olaylarin
//	                           gurultuye gommesini de onler.
//
// Bu ayrim GUVENLIK REDLERINI KAPSAMAZ: capraz-site reddi, cerez
// eslesmemesi, oturum baglamasi ve CSRF 403 + GENEL mesajda KALIR
// (saldirgana hangi kontrolun caldigini soylemek istemiyoruz). Burada
// 400'e giden tek sinif "kayit TTL ile dustu"dur.
func (f *FedereSayfalar) istekTamamlamaHatasi(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrIstekZatenTamamlandi):
		f.hataSayfasi(w, http.StatusConflict, hataIstekBagli)
	case errors.Is(err, ErrIstekYok):
		f.hataSayfasi(w, http.StatusBadRequest, hataIstekSuresiDoldu)
	default:
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
	}
}

// Baslat, federasyon akisini baslatir: state/nonce/PKCE dogrulayicisi
// uretir, akisi BASLATAN tarayiciya cerez verir ve kullaniciyi yukari
// akisin authorize adresine yonlendirir.
//
// BU BACAK K3'TEN MUAF DEGILDIR. Muafiyet YALNIZCA geri donus ucuna
// aittir (bkz. Geri): orada donus TANIM GEREGI capraz-sitedir. Baslat
// ise /giris sayfasindaki butondan gelen AYNI SITE ust-duzey bir
// GET'tir, yani Faz 1'in iki korumasi burada oldugu gibi gecerlidir ve
// ikisi de ZORUNLUDUR:
//
//  1. OTURUM BAGLAMASI (oturumBaglamaDogrula): authRequestID tek basina
//     yetki girdisi DEGILDIR. Baglama olmadan, canli bir
//     authRequestID'yi ogrenen biri federasyon bacagini KENDI
//     tarayicisinda kosturup Geri'de istegi KENDI hesabina
//     tamamlayabilir; kurbanin uygulamasi saldirganin hesabina ait
//     jetonlari alir. kimlik_fed cerezi baslat ile geri'yi birbirine
//     baglar ama hicbirini /authorize'i BASLATAN tarayiciya baglamaz;
//     bunu yapan tek sey Faz 1'in kimlik_oturum baglamasidir.
//
//  2. CAPRAZ-SITE REDDI (caprazSiteIstek): AuthorizeSar, /authorize'a
//     CAPRAZ-SITEDEN gelindiginde baglamayi BILEREK KURMAZ ve guvendigi
//     degismez sudur: "auth istegi olusur ama hicbir tarayici ona bagli
//     olmadigi icin GET /giris 403 doner ve istek TAMAMLANAMAZ".
//     Baslat'ta baglama aranmasi bu degismezi zaten korur; capraz-site
//     reddi ikinci kemerdir ve niyeti okunur kilar.
//
// Mesru akis etkilenmez: kimlik_oturum SameSite=Lax'tir ve /giris'ten
// gelen ayni-site ust-duzey GET'te TASINIR.
func (f *FedereSayfalar) Baslat(w http.ResponseWriter, r *http.Request) {
	akis, ok := f.akislar[r.PathValue("saglayici")]
	if !ok {
		// Yapilandirilmamis bir saglayicinin butonu hic gorunmez, yani
		// buraya normalde gelinmez; gelinirse 404, "500" degil.
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		f.hataSayfasi(w, http.StatusBadRequest, hataIstekGecersiz)
		return
	}
	if caprazSiteIstek(r) {
		// authRequestID ve cerez degeri LOGLANMAZ.
		log.Print("federasyon: capraz-site baslatma reddedildi")
		f.hataSayfasi(w, http.StatusForbidden, hataOturum)
		return
	}
	// Baglama TALEP edilir, MINTLENMEZ (Faz 1'in girisGet kurali): yeni
	// bir baglama kurmak, saldirganin kendi tarayicisinda baglama alip
	// linki kurbana yollamasina izin verirdi.
	if _, err := oturumBaglamaDogrula(r.Context(), f.oturumlar, r, id); err != nil {
		oturumRed(w, "federasyon-baslat", err)
		return
	}
	// Istek GERCEKTEN var olmali: var olmayan bir id ile yukari akisa
	// gitmek, donuste tamamlanacak hicbir sey olmadigi icin kullaniciyi
	// anlamsiz bir hataya goturur.
	if _, err := f.istekler.IDileOku(r.Context(), id); err != nil {
		// authRequestID LOGLANMAZ (Faz 1'in kurali): TamamlandiIsaretle
		// ile birlesince bekleyen bir istegi belirli bir kullaniciya
		// baglayan kisa omurlu bir yetki jetonu gibi davranir.
		log.Printf("federasyon: baslatma istegi gecersiz: %v", err)
		f.hataSayfasi(w, http.StatusBadRequest, hataIstekGecersiz)
		return
	}

	state, err := rasgeleJeton()
	nonce, err2 := rasgeleJeton()
	cerezDeger, err3 := rasgeleJeton()
	dogrulayici, _, err4 := kodDogrulayiciUret()
	// HATALAR YUTULMAZ: zayif/bos bir state, nonce veya cerez degeri
	// korumayi fiilen kaldirir.
	if err != nil || err2 != nil || err3 != nil || err4 != nil {
		log.Print("federasyon: rasgele deger uretilemedi")
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}

	if err := f.durum.DurumYaz(r.Context(), state, FedereDurum{
		AuthRequestID:  id,
		Nonce:          nonce,
		Saglayici:      akis.Ad,
		Cerez:          cerezDeger,
		KodDogrulayici: dogrulayici,
	}); err != nil {
		// state, nonce, cerez ve dogrulayici LOGLANMAZ.
		log.Printf("federasyon: durum kaydi yazilamadi: %v", err)
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	http.SetCookie(w, f.federasyonCerezi(cerezDeger))
	http.Redirect(w, r, akis.AuthorizeURL(state, nonce, dogrulayici, f.geriURL(akis.Ad)), http.StatusFound)
}

// stateVeKod, geri donusteki state ve code degerlerini okur: GET'te
// query'den, POST'ta (Apple form_post) govdeden.
func (f *FedereSayfalar) stateVeKod(r *http.Request) (string, string) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			return "", ""
		}
		return r.PostFormValue("state"), r.PostFormValue("code")
	}
	q := r.URL.Query()
	return q.Get("state"), q.Get("code")
}

// Geri, yukari akistan donen kullaniciyi karsilar.
//
// K3: caprazSiteIstek guard'i BURAYA UYGULANMAZ. Apple'in form_post
// donusu TANIM GEREGI capraz-sitedir (Sec-Fetch-Site: cross-site) ve
// guard'i burada kosturmak Apple girisini tumden kirar. Guard'in yerini
// state (tek kullanimlik) + cerez (K2) + nonce ucgeni alir; UCU DE
// zorunludur. Bu muafiyet YALNIZCA bu uca aittir: /authorize ve
// /giris bacaklarinda guard yerinde kalir.
func (f *FedereSayfalar) Geri(w http.ResponseWriter, r *http.Request) {
	state, kod := f.stateVeKod(r)
	// DurumAl GETDEL'dir: kayit TEK KULLANIMLIK. Yakalanan bir callback
	// (adres cubugu, log, proxy) ikinci kez oynatilamaz.
	d, err := f.durum.DurumAl(r.Context(), state)
	if err != nil {
		// state LOGLANMAZ; yalnizca hata sinifi.
		log.Printf("federasyon: durum dogrulanamadi: %v", err)
		f.hataSayfasi(w, http.StatusForbidden, hataOturum)
		return
	}

	cerez, cerezHata := r.Cookie(cerezAdFederasyon)
	if cerezHata != nil || subtle.ConstantTimeCompare([]byte(cerez.Value), []byte(d.Cerez)) != 1 {
		// Bu dal, BASKA bir tarayicida baslatilmis bir akisin bu
		// tarayicida tamamlanmasini engeller (hesap devralma):
		// saldirgan kendi yetkilendirmesinden aldigi gecerli bir
		// state+code ciftini kurbanin tarayicisinda acsa bile cerez
		// kurbanin tarayicisinda YOKTUR.
		log.Print("federasyon: cerez baglamasi tutmadi")
		f.hataSayfasi(w, http.StatusForbidden, hataOturum)
		return
	}

	// Saglayici YOLDAN DEGIL KAYITTAN okunur: aksi halde saldirgan
	// Google'da baslatilmis bir akisi /federasyon/apple/geri'ye getirip
	// token ucu, PKCE dogrulayicisi ve id_token dogrulayicisi secimini
	// karistirabilirdi.
	akis, akisVar := f.akislar[d.Saglayici]
	dogrulayici, dogrulayiciVar := f.dogrulayicilar[d.Saglayici]
	if !akisVar || !dogrulayiciVar {
		// Kayit yazildiktan sonra saglayici yapilandirmadan cikarilmis
		// olabilir (yeniden dagitim). Saglayici adi SABIT bir kume
		// oldugu icin loglanabilir.
		log.Printf("federasyon: kayitli saglayici artik yapilandirilmamis (%s)", d.Saglayici)
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	if kod == "" {
		// Kullanici saglayicida vazgecmis ya da saglayici hata
		// dondurmus olabilir; degisilecek bir kod yok.
		log.Print("federasyon: donuste yetkilendirme kodu yok")
		f.hataSayfasi(w, http.StatusBadRequest, hataIstekGecersiz)
		return
	}

	idToken, err := akis.JetonDegis(r.Context(), f.istemci, kod, d.KodDogrulayici, f.geriURL(akis.Ad))
	if err != nil {
		// Hata metninde kod, client_secret veya yanit govdesi GECMEZ
		// (bkz. JetonDegis).
		log.Printf("federasyon: kod degisimi basarisiz: %v", err)
		f.hataSayfasi(w, http.StatusForbidden, hataOturum)
		return
	}
	// Imza (RS256), iss, aud, expiry, sub ve NONCE burada dogrulanir;
	// bu dosya hicbirini yeniden dogrulamaz.
	iddia, err := dogrulayici.DogrulaNonce(idToken, d.Nonce)
	if err != nil {
		// err icinde id_token GECMEZ (bkz. auth.DogrulaNonce).
		log.Printf("federasyon: id_token dogrulanamadi: %v", err)
		f.hataSayfasi(w, http.StatusForbidden, hataOturum)
		return
	}

	// kullaniciCoz UC deger doner: kullanici, cakisma, hata. Cakisma nil
	// DEGILSE giris YAPILMAZ -- baglama ya da 409 sayfasina gidilir.
	ku, cakisma, err := f.kullaniciCoz(r.Context(), akis.Ad, iddia)
	if err != nil {
		// E-posta ve sub LOGLANMAZ.
		log.Printf("federasyon: kullanici cozulemedi: %v", err)
		// Yukari akis e-posta gondermemesi bir SUNUCU hatasi degildir ve
		// bos e-posta gosteren 409 cakisma sayfasina da gitmez: kullanici
		// ne oldugunu ve ne yapacagini okumali (bkz.
		// ErrYukariAkisEpostaYok).
		if errors.Is(err, ErrYukariAkisEpostaYok) {
			f.hataSayfasi(w, http.StatusBadRequest, hataEpostaYok)
			return
		}
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	if cakisma != nil {
		f.cakismaYonlendir(w, r, d.AuthRequestID, cakisma)
		return
	}

	if err := f.istekler.TamamlandiIsaretle(r.Context(), d.AuthRequestID, ku.ID, []string{amrFederasyon}); err != nil {
		// authRequestID LOGLANMAZ.
		log.Printf("federasyon: istek tamamlanamadi: %v", err)
		// Siniflandirma istekTamamlamaHatasi'nda: 409 (baska kimlige
		// bagli, Subject EZILMEZ), 400 (TTL ile dustu) ve 500 (gercek
		// sunucu hatasi) birbirinden AYRI.
		f.istekTamamlamaHatasi(w, err)
		return
	}
	// HIJYEN: akis bitti, kimlik_fed cerezinin tarayicida kalmasi icin
	// bir neden yok. Bu bir GUVENLIK duzeltmesi DEGIL -- guvenlik
	// dayanagi state kaydinin GETDEL ile tek kullanimlik olmasidir
	// (bkz. DurumAl), yani geride kalan cerez tek basina hicbir akisi
	// yeniden oynatamaz. Silme cerezinin Name/Path/Secure/SameSite
	// ozniteliklerinin yazma anindakiyle AYNI olmasi ZORUNLU: aksi
	// halde tarayici silmeyi hic uygulamaz, bu yuzden ayni yapici
	// (federasyonCerezi) kullanilir.
	http.SetCookie(w, f.federasyonCereziSil())
	http.Redirect(w, r, f.geriCagirma(r.Context(), d.AuthRequestID), http.StatusFound)
}
