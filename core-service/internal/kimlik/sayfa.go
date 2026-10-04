package kimlik

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/zitadel/oidc/v3/pkg/op"
)

// Giris ve kayit sayfalarinin yollari. Hem istemcinin LoginURL'i hem
// sayfa handler'lari buradan okur.
const (
	yolGiris = "/giris"
	yolKayit = "/kayit"
)

// csrfAlan, formdaki CSRF jetonunun alan adi.
const csrfAlan = "csrf"

// IstekTamamlayici, giris/kayit basarili olunca bekleyen auth istegini
// kullaniciya baglar. Arayuz olmasinin nedeni testlerin mutlu yolu AG
// OLMADAN olcebilmesi; uretimde *IstekDepo karsilar.
type IstekTamamlayici interface {
	TamamlandiIsaretle(ctx context.Context, id string, userID string) error
}

//go:embed sablonlar/*.html
var sablonFS embed.FS

var sablonlar = template.Must(template.ParseFS(sablonFS, "sablonlar/*.html"))

// hataGirisBasarisiz, hem "boyle bir hesap yok" hem "sifre yanlis"
// durumunda gosterilir. Ikisini ayirmak formu e-posta sayim aracina
// cevirir (kullanici numaralandirma).
const hataGirisBasarisiz = "E-posta veya sifre hatali."

// hataSunucu, beklenmeyen (5xx) durumlarda gosterilir; ic detay
// icermez.
const hataSunucu = "Bir hata olustu, lutfen tekrar deneyin."

// hataEpostaKullanimda, kayitta e-posta zaten varsa gosterilir. Bu
// bilgiyi kayit akisinda vermek kullanici numaralandirma sayilmaz:
// giris akisindan farkli olarak burada kullanici zaten kendi verdigi
// e-postayla yeni hesap acmaya calisiyor.
const hataEpostaKullanimda = "Bu e-posta zaten kullaniliyor."

// hataOturum, cerez baglamasi veya CSRF dogrulamasi tutmadiginda
// donulur. TEK bir genel mesaj: hangi kosulun tutmadigini (cerez yok /
// baglama yok / id eslesmedi / csrf yanlis) ayirmak saldirgana yol
// gosterir.
const hataOturum = "Oturum dogrulanamadi, lutfen giris sayfasini yeniden acin."

// hataIstekBagli, bekleyen auth istegi ZATEN baska bir kimlige
// baglandiginda donulur. Subject ezilmez (bkz.
// IstekDepo.TamamlandiIsaretle); kullanici akisi bastan baslatmali.
const hataIstekBagli = "Bu giris istegi zaten kullanildi, lutfen uygulamadan tekrar deneyin."

// girisVeri, giris.html sablonuna verilen veridir.
type girisVeri struct {
	AuthRequestID string
	CSRF          string
	Eposta        string
	Hata          string
	GirisYolu     string
	KayitYolu     string
}

// kayitVeri, kayit.html sablonuna verilen veridir.
type kayitVeri struct {
	AuthRequestID string
	CSRF          string
	Eposta        string
	Ad            string
	Hata          string
	GirisYolu     string
	KayitYolu     string
}

// Sayfalar, hosting edilen giris/kayit HTML formlarini ve bunlarin
// OIDC akisina geri donusunu tasir.
//
// GUVENLIK: authRequestID tek basina yetki girdisi DEGILDIR. Cerez ->
// {authRequestID, CSRF} baglamasi YALNIZCA /authorize'da, istegi
// BASLATAN tarayici icin kurulur (bkz. AuthorizeSar). GET /giris,
// GET /kayit, POST'lar ve /authorize/callback bu baglamayi ZORUNLU
// kilar ve HICBIRI yeni baglama mintlemez. Boylece auth istegini
// baslatan, formu goren, girisi tamamlayan ve kodu toplayan tarayici
// AYNI olmak zorundadir. Ayrica /authorize CAPRAZ-SITEDEN cagrildiginda
// hic baglama kurulmaz (login-CSRF; bkz. AuthorizeSar ve
// caprazSiteIstek).
type Sayfalar struct {
	kullanici    UserStore
	istekler     IstekTamamlayici
	oturumlar    OturumDepo
	geriCagirma  func(context.Context, string) string
	cerezGuvenli bool

	// callbackDelege, cerez baglamasi dogrulandiktan sonra cagrilan
	// KUTUPHANE handler'idir (op.AuthorizeCallbackHandler / saglayici).
	// Kutuphane kodu degistirilmez veya kopyalanmaz; yalnizca onune
	// gecilir.
	callbackDelege http.Handler
}

// NewSayfalar, gecerli bir UserStore, IstekDepo, OturumDepo ve
// op.AuthCallbackURL tarafindan uretilen geri cagirma ile Sayfalar
// kurar. cerezGuvenli, cereze Secure bayragi konup konmayacagidir:
// issuer https ise true, http:// yerel gelistirmede false (aksi halde
// tarayici cerezi hic saklamaz ve yerel akis kirilir).
func NewSayfalar(
	kullanici UserStore,
	istekler IstekTamamlayici,
	oturumlar OturumDepo,
	geriCagirma func(context.Context, string) string,
	cerezGuvenli bool,
) *Sayfalar {
	return &Sayfalar{
		kullanici:    kullanici,
		istekler:     istekler,
		oturumlar:    oturumlar,
		geriCagirma:  geriCagirma,
		cerezGuvenli: cerezGuvenli,
	}
}

// CallbackDelege, cerez baglamasi dogrulandiktan sonra cagrilacak
// kutuphane handler'ini baglar. Ayri bir setter: handler kutuphane
// saglayicisindan gelir ve saglayici NewSayfalar'dan sonra kurulur.
func (s *Sayfalar) CallbackDelege(h http.Handler) { s.callbackDelege = h }

// Bagla, sayfalari mux'a baglar. POST handler'lari issuer aracisiyla
// SARILIR: araci issuer'i istek baglamina koyar ve geriCagirma onu
// oradan okur. Sarmadan baglamak akisi yonlendirme asamasinda kirar.
//
// hiz, uclara uygulanan hiz limitidir (bkz. hizlimit.go). GET'ler de
// SARILIR: baglama /authorize'a tasindiktan sonra GET artik Redis'e
// yazmiyor ama uc hala kimlik dogrulamasiz ve /authorize'dan (gecerli
// client_id + redirect_uri + S256 ister) daha ucuz bir yuzey; sablon
// uretimi de bedava degil. GET'ler POST'lardan AYRI anahtarlarda
// sayilir: aksi halde formu acmak sifre deneme butcesini tuketir ve
// mesru kullanici kendi akisini kilitler.
//
// nil GECILEBILIR ve o zaman hiz limiti UYGULANMAZ; bu yalnizca birim
// testler icindir, uretimde Start her zaman bir limit kurar ve limit
// kurulamazsa HIC BASLAMAZ.
func (s *Sayfalar) Bagla(mux *http.ServeMux, araci *op.IssuerInterceptor, hiz *HizLimit) {
	girisPost := araci.Handler(http.HandlerFunc(s.Giris))
	kayitPost := araci.Handler(http.HandlerFunc(s.Kayit))
	// GET'ler issuer aracisiyla SARILMAZ: form uretimi geriCagirma
	// cagirmaz, yani istek baglaminda issuer'a ihtiyac duymaz.
	var girisGet http.Handler = http.HandlerFunc(s.Giris)
	var kayitGet http.Handler = http.HandlerFunc(s.Kayit)
	if hiz != nil {
		// Hiz limiti aracinin ICINDE degil DISINDA: reddedilen bir istek
		// issuer cozumlemesi dahil hicbir ek is yapmadan donsun.
		girisPost = hiz.GirisSar(girisPost)
		kayitPost = hiz.KayitSar(kayitPost)
		girisGet = hiz.SayfaGetSar("giris", girisGet)
		kayitGet = hiz.SayfaGetSar("kayit", kayitGet)
	}
	mux.Handle("GET "+yolGiris, girisGet)
	mux.Handle("POST "+yolGiris, girisPost)
	mux.Handle("GET "+yolKayit, kayitGet)
	mux.Handle("POST "+yolKayit, kayitPost)
}

// BaglamaKur, verilen authRequestID'yi CAGIRAN tarayiciya baglar: varsa
// mevcut oturum cerezini yeniden kullanir (paralel akis icin, bkz.
// oturumBaglamaUstSinir), yoksa yeni bir oturum degeri uretir; istege
// ozel bir CSRF jetonu uretip baglamayi depoya yazar ve cerezi yanita
// koyar.
//
// YALNIZCA /authorize bacagindan cagrilir (AuthorizeSar). GET /giris ve
// GET /kayit bunu CAGIRMAZ: cagirsalardi baglama herkese, herhangi bir
// id icin mintlenebilir olurdu (bkz. oturum.go cerezAdOturum notu).
//
// Oturum degeri, CSRF jetonu ve authRequestID LOGLANMAZ.
func (s *Sayfalar) BaglamaKur(w http.ResponseWriter, r *http.Request, id string) error {
	if id == "" {
		return ErrOturumEslesmedi
	}
	var (
		oturum  string
		baglama OturumBaglama
	)
	// Mevcut cerez YALNIZCA depoda gecerli bir baglamasi varsa yeniden
	// kullanilir; uydurma veya suresi dolmus bir cerez degerinin uzerine
	// yazmak, saldirganin secebildigi bir oturum degerini canlandirmak
	// olurdu.
	if cerez, err := r.Cookie(cerezAdOturum); err == nil && cerez.Value != "" {
		if mevcut, err := s.oturumlar.OturumOku(r.Context(), cerez.Value); err == nil {
			oturum = cerez.Value
			baglama = mevcut
		}
	}
	if oturum == "" {
		yeni, err := rasgeleJeton()
		if err != nil {
			return err
		}
		oturum = yeni
	}
	csrf, err := rasgeleJeton()
	if err != nil {
		return err
	}
	baglama.Ekle(id, csrf)
	if err := s.oturumlar.OturumYaz(r.Context(), oturum, baglama, istekTTL); err != nil {
		return err
	}
	// Cerez her yolda yeniden yazilir: yeni oturumda zorunlu, mevcut
	// oturumda MaxAge'i istek TTL'i boyunca tazeler.
	http.SetCookie(w, oturumCerezi(oturum, s.cerezGuvenli))
	return nil
}

// AuthorizeSar, /authorize handler'ini sarar ve istek dogrulamalari
// GECTIKTEN sonra (yani kutuphane kullaniciyi giris sayfasina
// yonlendirdiginde) cerez baglamasini ISTEGI BASLATAN tarayici icin
// kurar.
//
// NEDEN BURADA: /authorize, akisin tarayici tarafindan BASLATILDIGI tek
// noktadir ve gecerli bir client_id + redirect_uri + S256 PKCE
// gerektirir (bkz. pkceZorlayici). Baglamayi burada kurmak, "istegi
// baslatan tarayici" ile "cerezi tasiyan tarayici" esitligini
// kurulabilir tek yer yapar. Kutuphane kodu DEGISTIRILMEZ: yanit
// yazilmadan HEMEN once araya giriyoruz.
//
// Yonlendirme giris sayfasina DEGILSE (hata 302'si, dogrudan yanit)
// baglama KURULMAZ.
//
// FAIL-CLOSED: baglama kurulamazsa (orn. Redis yok) kutuphanenin
// yonlendirmesi YAZILMAZ, 500 donulur. Aksi halde kullanici, POST'u
// kesin 403 olacak bir forma gonderilirdi.
//
// CAPRAZ-SITE NAVIGASYON (login-CSRF): oturum cerezi SameSite=Lax
// oldugu icin ust-duzey navigasyonda TASINIR. Saldirgan kurbani kendi
// hazirladigi /authorize?...&code_challenge=<SALDIRGANIN> adresine
// yonlendirirse, kurbanin MEVCUT cerezi yeniden kullanilir ve
// saldirganin parametreleriyle dogan authRequestID kurbanin baglama
// kumesine girer: kurban formu acabilir, girisi tamamlar, uretilen
// kodun code_verifier'i ise SALDIRGANDA olur. Kod kurbanin cihazindan
// sizarsa (ayni custom scheme'i claim eden kotu amacli bir uygulama,
// RFC 8252 bolum 8.1) PKCE'nin korudugu TEK senaryo cokar. Bu yuzden
// capraz-siteden gelen istekte baglama KURULMAZ; bkz.
// caprazSiteIstek.
func (s *Sayfalar) AuthorizeSar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		yakalayici := &baglamaYakalayici{ResponseWriter: w}
		yakalayici.kur = func(kod int) error {
			if kod != http.StatusFound && kod != http.StatusSeeOther {
				return nil
			}
			id := girisYonlendirmeID(w.Header().Get("Location"))
			if id == "" {
				return nil
			}
			if caprazSiteIstek(r) {
				// Yalnizca YENI BAGLAMA EKLENMEZ. Kurbanin cerezi, mevcut
				// baglamalari ve paralel akislari DOKUNULMADAN kalir
				// (cerez yenilenmez, kume temizlenmez): aksi halde bu
				// kontrol kurbanin acik sekmelerini dusuren bir DoS'a
				// donerdi. Kutuphanenin yonlendirmesi YAZILIR; auth
				// istegi olusur ama hicbir tarayici ona bagli olmadigi
				// icin GET /giris 403 doner ve istek TAMAMLANAMAZ.
				//
				// authRequestID, cerez degeri ve CSRF jetonu LOGLANMAZ.
				log.Print("authorize: capraz-site navigasyon, oturum baglamasi kurulmadi")
				return nil
			}
			return s.BaglamaKur(w, r, id)
		}
		next.ServeHTTP(yakalayici, r)
	})
}

// Fetch-metadata baslik adlari (tarayici tarafindan uretilir, istemci
// JavaScript'i tarafindan DEGISTIRILEMEZ; "Sec-" on eki yasak baslik
// adi oldugu icin fetch/XHR ile ezilemez).
const (
	baslikFetchSite = "Sec-Fetch-Site"
	baslikFetchMode = "Sec-Fetch-Mode"
)

// Sec-Fetch-Site degerleri. DORDUNU de ayirmak sart:
//
//   - "none"        -> kullanicinin DOGRUDAN baslattigi navigasyon:
//     adres cubugu, yer imi VE native uygulamanin sistem tarayicisini
//     acmasi. BIZIM MESRU AKISIMIZ budur (bugun tek istemcimiz
//     MobilIstemci / ApplicationTypeNative), asla reddedilmez.
//   - "same-origin" -> kendi sayfalarimizdan donen navigasyon
//     (giris <-> kayit capraz linkleri, form sonrasi geri donusler).
//   - "same-site"   -> ayni kayitli alan adi, farkli origin.
//   - "cross-site"  -> BASKA bir sitenin baslattigi istek: tek
//     hedefimiz bu.
const (
	fetchSiteYok        = "none"
	fetchSiteAyniOrigin = "same-origin"
	fetchSiteAyniSite   = "same-site"
	fetchSiteCapraz     = "cross-site"
)

// caprazSiteIstek, istegin BASKA bir sitenin baslattigi bir istek
// oldugunu soyler.
//
// BASLIK YOKSA FAIL-OPEN (false doner, yani baglama KURULUR). Gerekce:
// bu kontrol yalnizca "cerezi tasiyan tarayici" senaryosunda is gorur
// ve SameSite=Lax cerezi tasiyan her tarayici fetch-metadata'yi da
// gonderir (Chrome 76+, Firefox 90+, Safari 16.4+; SameSite'i
// uygulamayan daha eski bir tarayicida zaten Lax korumasi da yoktur,
// yani fail-closed yapmak somuruyu kapatmaz). Buna karsilik
// fail-closed, basligi hic gondermeyen TARAYICI OLMAYAN istemcilerde
// (curl, yerel gelistirme araclari, saglik kontrolleri) girisi tumden
// kirardi — ve o istemcilerde saldirgan bir "kurban cerezi" de yoktur.
// Yani eksik baslik, kazandirdigindan cok sey kiriyor.
//
// Mod (Sec-Fetch-Mode) kontrolu KASITLI olarak YOK: capraz-site bir
// alt-kaynak istegi (mod "no-cors"/"cors") Lax cerezi ZATEN tasimaz,
// dolayisiyla modu ayirmadan TUM capraz-site isteklerde baglama
// kurmamak hem daha basit hem daha dar. Baslik yine de okunabilir
// olsun diye adi baslikFetchMode'da tutuluyor.
//
// GELECEGE NOT — WEB RP'si: ileride tarayici tabanli bir istemci (web
// RP) eklenirse, o istemcinin kullaniciyi OP'ye yonlendirmesi MESRU
// olarak Sec-Fetch-Site: cross-site gelir ve bu kontrol onu reddeder.
// O gun bu fonksiyon gozden gecirilmeli: ornegin baglamayi kurup
// login-CSRF'i ayri bir mekanizmayla (OP tarafinda tutulan, istemciye
// ozel bir baslatma jetonu) kapatmak gerekir. SameSite=Strict'e gecmek
// COZUM DEGILDIR: tek basina bu somuruyu kapatmaz ve kurbanin mesru
// akislarini dusurur.
func caprazSiteIstek(r *http.Request) bool {
	return r.Header.Get(baslikFetchSite) == fetchSiteCapraz
}

// girisYonlendirmeID, kutuphanenin urettigi yonlendirme adresi BIZIM
// giris sayfamiza gidiyorsa authRequestID'yi doner, aksi halde bos
// string. Yol kontrolu zorunlu: istemcinin redirect_uri'sine donen hata
// yonlendirmelerinde baglama kurulmamali.
func girisYonlendirmeID(konum string) string {
	if konum == "" {
		return ""
	}
	u, err := url.Parse(konum)
	if err != nil || u.Path != yolGiris {
		return ""
	}
	return u.Query().Get("authRequestID")
}

// baglamaYakalayici, sarilan handler'in yanitini YAZMADAN once kur'u
// cagirir. Cerez ve durum kodu sirasi onemli: Set-Cookie basliklari
// WriteHeader'dan ONCE yazilmak zorunda.
type baglamaYakalayici struct {
	http.ResponseWriter

	// kur, yanitin durum kodunu alir ve baglamayi kurar. Hata donerse
	// sarilan handler'in yaniti BASTIRILIR ve 500 yazilir.
	kur func(kod int) error

	yazildi    bool
	bastirildi bool
}

func (y *baglamaYakalayici) WriteHeader(kod int) {
	if y.yazildi {
		return
	}
	y.yazildi = true
	if err := y.kur(kod); err != nil {
		// authRequestID, cerez degeri ve CSRF jetonu LOGLANMAZ.
		log.Printf("authorize: oturum baglamasi kurulamadi: %v", err)
		y.bastirildi = true
		y.ResponseWriter.Header().Del("Location")
		http.Error(y.ResponseWriter, hataSunucu, http.StatusInternalServerError)
		return
	}
	y.ResponseWriter.WriteHeader(kod)
}

func (y *baglamaYakalayici) Write(b []byte) (int, error) {
	if !y.yazildi {
		y.WriteHeader(http.StatusOK)
	}
	if y.bastirildi {
		// Govde yutulur ama yazar hata gormez: 500 ZATEN yazildi.
		return len(b), nil
	}
	return y.ResponseWriter.Write(b)
}

// oturumZorla, bir POST'un GERCEKTEN bu tarayicida gosterilen forma ait
// oldugunu dogrular: (1) oturum cerezi var, (2) cereze bagli bir kayit
// var, (3) bagli authRequestID form degeriyle birebir ayni, (4) form
// CSRF jetonu cereze bagli jetonla ayni. Dordu de SIFRE DOGRULAMASINDAN
// ONCE kosar; biri tutmazsa giris denemesi hic yapilmaz.
func (s *Sayfalar) oturumZorla(r *http.Request, id string) error {
	csrf, err := oturumBaglamaDogrula(r.Context(), s.oturumlar, r, id)
	if err != nil {
		return err
	}
	if !csrfEsit(csrf, r.FormValue(csrfAlan)) {
		return ErrCSRF
	}
	return nil
}

// oturumRed, baglama/CSRF dogrulamasi tutmadiginda tek bicimli yaniti
// yazar. Reddin SEBEBI istemciye sizdirilmaz; sunucu tarafinda da cerez
// degeri, CSRF jetonu ve authRequestID loglanmaz, yalnizca hata turu
// yazilir.
func oturumRed(w http.ResponseWriter, nerede string, err error) {
	log.Printf("%s: oturum baglamasi dogrulanamadi: %v", nerede, err)
	http.Error(w, hataOturum, http.StatusForbidden)
}

// Giris, GET'te formu gosterir, POST'ta kimlik dogrular ve basarili
// olursa OIDC akisina geri doner.
func (s *Sayfalar) Giris(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.girisPost(w, r)
		return
	}
	s.girisGet(w, r)
}

// girisGet, formu YALNIZCA bu tarayici o auth istegini baslatmissa
// gosterir. Yeni baglama MINTLEMEZ: var olan baglamayi TALEP eder.
// Baglama yoksa form BILE gosterilmez — aksi halde saldirgan kendi
// tarayicisinda baglama alip linki kurbana yollayabilirdi.
func (s *Sayfalar) girisGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	csrf, err := oturumBaglamaDogrula(r.Context(), s.oturumlar, r, id)
	if err != nil {
		oturumRed(w, "giris-get", err)
		return
	}
	s.girisRender(w, http.StatusOK, id, csrf, "", "")
}

func (s *Sayfalar) girisPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	id := r.FormValue("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	// Cerez baglamasi + CSRF: sifre dogrulamasindan ONCE. Burada
	// reddedilen bir istek kullanici deposuna HIC dokunmaz.
	if err := s.oturumZorla(r, id); err != nil {
		oturumRed(w, "giris", err)
		return
	}
	csrf := r.FormValue(csrfAlan)
	// epostaNormalize: kayit ile giris AYNI yoldan gecmek zorunda.
	// Eskiden kayit tarafi trim ediyor, giris tarafi ham form degerini
	// geciyordu; bosluklu e-postayla kayit olan kullanici AYNI girdiyle
	// giris yapamiyordu (bkz. kullanici_depo.go epostaNormalize notu).
	eposta := epostaNormalize(r.FormValue("eposta"))
	sifre := r.FormValue("sifre")

	kullanici, err := s.kullanici.ByEmail(r.Context(), eposta)
	if err != nil && !errors.Is(err, ErrKullaniciYok) {
		// E-posta PII'dir ve burada loglanmaz; hatanin kendisi zaten
		// teshis icin yeterli.
		log.Printf("giris: kullanici okunamadi: %v", err)
	}
	if !kullanici.SifreDogru(sifre) {
		// Kasitli olarak hesabin var olup olmadigini AYIRT ETMIYORUZ:
		// AYNI e-posta icin "hesap yok" ve "sifre yanlis" durumlari
		// birebir ayni govdeyi/durum kodunu doner (bkz.
		// TestYanlisGirisKullaniciVarligiSizdirmaz). Girilen e-postayi
		// forma geri yazmak numaralandirma sayilmaz: saldirgan zaten
		// kendi yazdigi degeri goruyor, farkli bir bilgi sizmiyor.
		// html/template kacislama yapar, bkz. TestGirisEpostaEchoKacar. Sifre
		// burada loglanmaz.
		s.girisRender(w, http.StatusUnauthorized, id, csrf, eposta, hataGirisBasarisiz)
		return
	}

	if err := s.istekler.TamamlandiIsaretle(r.Context(), id, kullanici.ID); err != nil {
		// authRequestID loglanmaz: TamamlandiIsaretle ile birlesince
		// bekleyen bir istegi belirli bir kullaniciya baglayan, 10
		// dakika gecerli bir yetki jetonu gibi davranir.
		log.Printf("giris: istek tamamlanamadi: %v", err)
		if errors.Is(err, ErrIstekZatenTamamlandi) {
			// Bu istek baska bir kimlige bagli; ezmek yerine reddet.
			http.Error(w, hataIstekBagli, http.StatusConflict)
			return
		}
		s.girisRender(w, http.StatusInternalServerError, id, csrf, eposta, hataSunucu)
		return
	}
	http.Redirect(w, r, s.geriCagirma(r.Context(), id), http.StatusFound)
}

func (s *Sayfalar) girisRender(w http.ResponseWriter, kod int, id, csrf, eposta, hata string) {
	veri := girisVeri{
		AuthRequestID: id,
		CSRF:          csrf,
		Eposta:        eposta,
		Hata:          hata,
		GirisYolu:     yolGiris,
		KayitYolu:     yolKayit,
	}
	w.WriteHeader(kod)
	if err := sablonlar.ExecuteTemplate(w, "giris.html", veri); err != nil {
		log.Printf("giris sablonu yazilamadi: %v", err)
	}
}

// Kayit, GET'te formu gosterir, POST'ta yeni kullanici olusturur ve
// basarili olursa OIDC akisina geri doner.
func (s *Sayfalar) Kayit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.kayitPost(w, r)
		return
	}
	s.kayitGet(w, r)
}

// kayitGet, girisGet ile AYNI kurala tabidir: baglama TALEP eder,
// mintlemez (bkz. girisGet).
func (s *Sayfalar) kayitGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	csrf, err := oturumBaglamaDogrula(r.Context(), s.oturumlar, r, id)
	if err != nil {
		oturumRed(w, "kayit-get", err)
		return
	}
	s.kayitRender(w, http.StatusOK, id, csrf, "", "", "")
}

func (s *Sayfalar) kayitPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	id := r.FormValue("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	// Cerez baglamasi + CSRF: kullanici OLUSTURMADAN once.
	if err := s.oturumZorla(r, id); err != nil {
		oturumRed(w, "kayit", err)
		return
	}
	csrf := r.FormValue(csrfAlan)
	// Giris ile AYNI normalizasyon (bkz. girisPost'taki not).
	eposta := epostaNormalize(r.FormValue("eposta"))
	ad := r.FormValue("ad")
	sifre := r.FormValue("sifre")

	kullanici, err := s.kullanici.Create(r.Context(), eposta, ad, sifre)
	if err != nil {
		switch {
		case errors.Is(err, ErrEpostaKullanimda):
			s.kayitRender(w, http.StatusConflict, id, csrf, eposta, ad, hataEpostaKullanimda)
		case errors.Is(err, ErrSifreKisa):
			s.kayitRender(w, http.StatusBadRequest, id, csrf, eposta, ad, ErrSifreKisa.Error())
		default:
			// E-posta PII'dir ve burada loglanmaz; hatanin kendisi zaten
			// teshis icin yeterli.
			log.Printf("kayit: kullanici olusturulamadi: %v", err)
			s.kayitRender(w, http.StatusInternalServerError, id, csrf, eposta, ad, hataSunucu)
		}
		return
	}

	if err := s.istekler.TamamlandiIsaretle(r.Context(), id, kullanici.ID); err != nil {
		// authRequestID loglanmaz (bkz. girisPost'taki ayni not).
		log.Printf("kayit: istek tamamlanamadi: %v", err)
		if errors.Is(err, ErrIstekZatenTamamlandi) {
			// Bu istek zaten baska bir kimlige bagli; Subject EZILMEZ.
			http.Error(w, hataIstekBagli, http.StatusConflict)
			return
		}
		s.kayitRender(w, http.StatusInternalServerError, id, csrf, eposta, ad, hataSunucu)
		return
	}
	http.Redirect(w, r, s.geriCagirma(r.Context(), id), http.StatusFound)
}

func (s *Sayfalar) kayitRender(w http.ResponseWriter, kod int, id, csrf, eposta, ad, hata string) {
	veri := kayitVeri{
		AuthRequestID: id,
		CSRF:          csrf,
		Eposta:        eposta,
		Ad:            ad,
		Hata:          hata,
		GirisYolu:     yolGiris,
		KayitYolu:     yolKayit,
	}
	w.WriteHeader(kod)
	if err := sablonlar.ExecuteTemplate(w, "kayit.html", veri); err != nil {
		log.Printf("kayit sablonu yazilamadi: %v", err)
	}
}

// Callback, /authorize/callback bacagini cerez baglamasiyla korur.
//
// Bu bacak acik kalirsa yalnizca forma CSRF koymak YETMEZ: kutuphane
// callback'te sadece AuthRequestByID + Done() bakar
// (pkg/op/auth_request.go, AuthorizeCallback), yani kodu TOPLAYAN
// tarayici girisi YAPAN tarayicidan bagimsiz olabilir. Burada ayni
// cerez baglamasini dogrulariz ve ancak "id" parametresi cereze bagli
// authRequestID ile birebir eslesirse kutuphanenin kendi handler'ina
// delege ederiz.
//
// CSRF jetonu burada ARANMAZ: bu bacak bir form POST'u degil,
// /giris POST'unun 302 ile yonlendirdigi bir GET'tir; jeton URL'e
// konsa Referer/tarayici gecmisi uzerinden sizardi. Koruyan sey cerez
// baglamasidir ve bu bacakta cerez baglamasi tek basina yeterlidir:
// CSRF, "kurbanin tarayicisinda istemedigi bir POST'u tetiklemek"
// tehdidine karsidir, burada ise kodu saldirganin tarayicisinda
// toplama tehdidi var ve onu cerez kapatiyor.
func (s *Sayfalar) Callback(w http.ResponseWriter, r *http.Request) {
	if s.callbackDelege == nil {
		log.Print("callback: delege baglanmamis")
		http.Error(w, hataSunucu, http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	id := r.Form.Get("id")
	if id == "" {
		http.Error(w, "id eksik", http.StatusBadRequest)
		return
	}
	if _, err := oturumBaglamaDogrula(r.Context(), s.oturumlar, r, id); err != nil {
		oturumRed(w, "callback", err)
		return
	}
	s.callbackDelege.ServeHTTP(w, r)
}
