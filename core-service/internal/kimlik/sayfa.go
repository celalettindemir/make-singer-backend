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

// girisVeri, giris.html sablonuna verilen veridir.
type girisVeri struct {
	AuthRequestID string
	Eposta        string
	Hata          string
	GirisYolu     string
	KayitYolu     string
}

// kayitVeri, kayit.html sablonuna verilen veridir.
type kayitVeri struct {
	AuthRequestID string
	Eposta        string
	Ad            string
	Hata          string
	GirisYolu     string
	KayitYolu     string
}

// Sayfalar, hosting edilen giris/kayit HTML formlarini ve bunlarin
// OIDC akisina geri donusunu tasir.
type Sayfalar struct {
	kullanici   UserStore
	istekler    *IstekDepo
	geriCagirma func(context.Context, string) string
}

// NewSayfalar, gecerli bir UserStore, IstekDepo ve op.AuthCallbackURL
// tarafindan uretilen geri cagirma ile Sayfalar kurar.
func NewSayfalar(
	kullanici UserStore,
	istekler *IstekDepo,
	geriCagirma func(context.Context, string) string,
) *Sayfalar {
	return &Sayfalar{kullanici: kullanici, istekler: istekler, geriCagirma: geriCagirma}
}

// Bagla, sayfalari mux'a baglar. POST handler'lari issuer aracisiyla
// SARILIR: araci issuer'i istek baglamina koyar ve geriCagirma onu
// oradan okur. Sarmadan baglamak akisi yonlendirme asamasinda kirar.
func (s *Sayfalar) Bagla(mux *http.ServeMux, araci *op.IssuerInterceptor) {
	mux.HandleFunc("GET "+yolGiris, s.Giris)
	mux.HandleFunc("POST "+yolGiris, araci.HandlerFunc(s.Giris))
	mux.HandleFunc("GET "+yolKayit, s.Kayit)
	mux.HandleFunc("POST "+yolKayit, araci.HandlerFunc(s.Kayit))
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
	s.girisRender(w, http.StatusOK, id, "", "")
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
	eposta := r.FormValue("eposta")
	sifre := r.FormValue("sifre")

	kullanici, err := s.kullanici.ByEmail(r.Context(), eposta)
	if err != nil && !errors.Is(err, ErrKullaniciYok) {
		log.Printf("giris: kullanici okunamadi eposta=%s hata=%v", eposta, err)
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
		s.girisRender(w, http.StatusUnauthorized, id, eposta, hataGirisBasarisiz)
		return
	}

	if err := s.istekler.TamamlandiIsaretle(r.Context(), id, kullanici.ID); err != nil {
		// authRequestID loglanmaz: TamamlandiIsaretle ile birlesince
		// bekleyen bir istegi belirli bir kullaniciya baglayan, 10
		// dakika gecerli bir yetki jetonu gibi davranir.
		log.Printf("giris: istek tamamlanamadi: %v", err)
		s.girisRender(w, http.StatusInternalServerError, id, eposta, hataSunucu)
		return
	}
	http.Redirect(w, r, s.geriCagirma(r.Context(), id), http.StatusFound)
}

func (s *Sayfalar) girisRender(w http.ResponseWriter, kod int, id, eposta, hata string) {
	veri := girisVeri{
		AuthRequestID: id,
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
	s.kayitRender(w, http.StatusOK, id, "", "", "")
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
	eposta := r.FormValue("eposta")
	ad := r.FormValue("ad")
	sifre := r.FormValue("sifre")

	kullanici, err := s.kullanici.Create(r.Context(), eposta, ad, sifre)
	if err != nil {
		switch {
		case errors.Is(err, ErrEpostaKullanimda):
			s.kayitRender(w, http.StatusConflict, id, eposta, ad, hataEpostaKullanimda)
		case errors.Is(err, ErrSifreKisa):
			s.kayitRender(w, http.StatusBadRequest, id, eposta, ad, ErrSifreKisa.Error())
		default:
			log.Printf("kayit: kullanici olusturulamadi eposta=%s hata=%v", eposta, err)
			s.kayitRender(w, http.StatusInternalServerError, id, eposta, ad, hataSunucu)
		}
		return
	}

	if err := s.istekler.TamamlandiIsaretle(r.Context(), id, kullanici.ID); err != nil {
		// authRequestID loglanmaz (bkz. girisPost'taki ayni not).
		log.Printf("kayit: istek tamamlanamadi: %v", err)
		s.kayitRender(w, http.StatusInternalServerError, id, eposta, ad, hataSunucu)
		return
	}
	http.Redirect(w, r, s.geriCagirma(r.Context(), id), http.StatusFound)
}

func (s *Sayfalar) kayitRender(w http.ResponseWriter, kod int, id, eposta, ad, hata string) {
	veri := kayitVeri{
		AuthRequestID: id,
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
