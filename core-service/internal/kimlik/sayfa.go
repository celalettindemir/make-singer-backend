package kimlik

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"

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
// GUVENLIK: authRequestID tek basina yetki girdisi DEGILDIR. Her GET
// bir oturum cerezi uretir ve cerez -> (authRequestID, CSRF)
// baglamasini oturumlar deposuna yazar; POST'lar ve /authorize/callback
// bu baglamayi ZORUNLU kilar. Boylece auth istegini baslatan, girisi
// tamamlayan ve kodu toplayan tarayici AYNI olmak zorundadir.
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
// hiz, POST uclarina uygulanan hiz limitidir (bkz. hizlimit.go). SADECE
// POST'lar sarilir: GET'ler yalnizca formu uretir, bcrypt kosmaz ve
// kullanici/jeton deposuna dokunmaz. nil GECILEBILIR ve o zaman hiz
// limiti UYGULANMAZ; bu yalnizca birim testler icindir, uretimde Start
// her zaman bir limit kurar ve limit kurulamazsa HIC BASLAMAZ.
func (s *Sayfalar) Bagla(mux *http.ServeMux, araci *op.IssuerInterceptor, hiz *HizLimit) {
	girisPost := araci.Handler(http.HandlerFunc(s.Giris))
	kayitPost := araci.Handler(http.HandlerFunc(s.Kayit))
	if hiz != nil {
		// Hiz limiti aracinin ICINDE degil DISINDA: reddedilen bir istek
		// issuer cozumlemesi dahil hicbir ek is yapmadan donsun.
		girisPost = hiz.GirisSar(girisPost)
		kayitPost = hiz.KayitSar(kayitPost)
	}
	mux.HandleFunc("GET "+yolGiris, s.Giris)
	mux.Handle("POST "+yolGiris, girisPost)
	mux.HandleFunc("GET "+yolKayit, s.Kayit)
	mux.Handle("POST "+yolKayit, kayitPost)
}

// oturumBasla, yeni bir oturum degeri ve CSRF jetonu uretir, baglamayi
// depoya yazar ve cerezi yanita koyar. Donen deger forma gomulecek CSRF
// jetonudur. Oturum degeri, CSRF jetonu ve authRequestID LOGLANMAZ.
func (s *Sayfalar) oturumBasla(w http.ResponseWriter, r *http.Request, id string) (string, error) {
	oturum, err := rasgeleJeton()
	if err != nil {
		return "", err
	}
	csrf, err := rasgeleJeton()
	if err != nil {
		return "", err
	}
	baglama := OturumBaglama{AuthRequestID: id, CSRF: csrf}
	if err := s.oturumlar.OturumYaz(r.Context(), oturum, baglama, istekTTL); err != nil {
		return "", err
	}
	http.SetCookie(w, oturumCerezi(oturum, s.cerezGuvenli))
	return csrf, nil
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

func (s *Sayfalar) girisGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	csrf, err := s.oturumBasla(w, r, id)
	if err != nil {
		log.Printf("giris: oturum baslatilamadi: %v", err)
		http.Error(w, hataSunucu, http.StatusInternalServerError)
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

func (s *Sayfalar) kayitGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("authRequestID")
	if id == "" {
		http.Error(w, "authRequestID eksik", http.StatusBadRequest)
		return
	}
	csrf, err := s.oturumBasla(w, r, id)
	if err != nil {
		log.Printf("kayit: oturum baslatilamadi: %v", err)
		http.Error(w, hataSunucu, http.StatusInternalServerError)
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
