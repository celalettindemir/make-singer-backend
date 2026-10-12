package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// yolBaglama, hesap baglama sayfasinin yolu. Federasyon akisinin
// ORTASINDAKI bir adimdir: kullanici yukari akista dogrulandi ama
// e-postasi mevcut bir SIFRELI hesapla cakisiyor.
const yolBaglama = "/baglama"

// alanBeklemeID, bekleyen baglama kaydinin form/query alan adi.
const alanBeklemeID = "beklemeID"

// gizliAktarmaSoneki, Apple'in "Hide My Email" adreslerinin sonekidir.
//
// Bu adresler BAGLAMA icin HICBIR KOSULDA guvenilir degildir (spec
// yasagi): adres kullaniciya degil (uygulama, Apple hesabi) ciftine
// aittir, kullanici hesabini silip tekrar kaydolursa AYNI adres geri
// gelir ve adresin bir baska satirda bulunmasi ayni kisi oldugunu
// KANITLAMAZ.
const gizliAktarmaSoneki = "@privaterelay.appleid.com"

// baglamaBeklemeTTL, bekleyen baglama kaydinin omru. Kullanicinin
// sifresini hatirlayip girmesine yetecek kadar uzun, calinmis bir
// kaydin degerini yitirmesine yetecek kadar kisa.
const baglamaBeklemeTTL = 10 * time.Minute

// BaglamaBekleme, /baglama sayfasinin bir POST'u guvenle tamamlamak
// icin ihtiyac duydugu her seyi tutar. Redis'te kisa omurlu ve TEK
// KULLANIMLIK tutulur.
//
// Alanlar disa acik (buyuk harfli) olmak ZORUNDA: kayit JSON olarak
// yazilir ve encoding/json kucuk harfli alanlari sessizce atlar.
type BaglamaBekleme struct {
	// AuthRequestID, akisin tamamlanacagi bekleyen auth istegi.
	AuthRequestID string
	// UserID, sifresi sorulacak MEVCUT hesap. Formdan GELMEZ: aksi
	// halde kullanici baska bir hesabin sifresini denemeye
	// yonlendirilebilirdi.
	UserID string
	// Saglayici ve Sub, dogru sifre girilince baglanacak federe kimlik.
	Saglayici string `json:"Saglayici"`
	Sub       string
	// Eposta, sayfada GOSTERILEN (salt okunur) adres.
	Eposta string `json:"Eposta"`
	// CSRF, bu kayda ve bu tarayiciya ozel jeton.
	CSRF string
}

// ErrBeklemeYok, beklenen baglama kaydi yok (hic olusmamis, suresi
// dolmus veya ZATEN TUKETILMIS).
var ErrBeklemeYok = errors.New("baglama beklemesi bulunamadi")

// ErrYukariAkisEpostaYok, yukari akis dogrulanmis id_token'da e-posta
// GONDERMEDIGI icin yeni bir federe hesap acilamadiginda donulur
// (bkz. kullaniciCoz'daki gerekce: bos e-posta ile en fazla BIR hesap
// olusabilir ve ikincisi kullaniciyi KALICI olarak kilitler).
var ErrYukariAkisEpostaYok = errors.New("yukari akis e-posta gondermedi")

// BeklemeDepo, bekleyen baglama kaydinin yazilmasini ve okunmasini
// tanimlar. Uretimde *IstekDepo karsilar.
//
// IKI okuma yolu KASITLIDIR:
//   - BeklemeOku (GET): formu RENDER etmek icin. Render kaydi
//     TUKETMEMELI, aksi halde sayfayi gormek akisi bitirirdi.
//   - BeklemeAl (GETDEL): POST icin. Kayit ATOMIK olarak tuketilir,
//     yani bir kayitla yalnizca TEK bir baglama tamamlanabilir.
type BeklemeDepo interface {
	BeklemeYaz(ctx context.Context, id string, b BaglamaBekleme, ttl time.Duration) error
	BeklemeOku(ctx context.Context, id string) (BaglamaBekleme, error)
	BeklemeAl(ctx context.Context, id string) (BaglamaBekleme, error)
}

var _ BeklemeDepo = (*IstekDepo)(nil)

func beklemeAnahtar(id string) string { return "kimlik:baglama:" + id }

func (d *IstekDepo) BeklemeYaz(ctx context.Context, id string, b BaglamaBekleme, ttl time.Duration) error {
	// Sifir/negatif TTL, Redis'te SURESIZ bir anahtar demektir: tek
	// kullanimlik olmasi gereken bir kayit sonsuza kadar yasardi.
	if ttl <= 0 {
		return fmt.Errorf("baglama beklemesi ttl pozitif olmali")
	}
	veri, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("baglama beklemesi serilenemedi: %w", err)
	}
	if err := d.rdb.Set(ctx, beklemeAnahtar(id), veri, ttl).Err(); err != nil {
		return fmt.Errorf("baglama beklemesi yazilamadi: %w", err)
	}
	return nil
}

// BeklemeOku, kaydi TUKETMEDEN okur (form render'i icin).
func (d *IstekDepo) BeklemeOku(ctx context.Context, id string) (BaglamaBekleme, error) {
	return d.beklemeCoz(d.rdb.Get(ctx, beklemeAnahtar(id)).Bytes())
}

// BeklemeAl, kaydi okur ve ATOMIK olarak siler (GETDEL): TEK
// KULLANIMLIK. Aksi halde dogru sifre bir kez girildikten sonra ayni
// kayitla ikinci bir baglama (baska bir auth istegini tamamlamak
// dahil) yapilabilirdi. Kod degisimiyle ayni desen (bkz. KodlaOku).
func (d *IstekDepo) BeklemeAl(ctx context.Context, id string) (BaglamaBekleme, error) {
	return d.beklemeCoz(d.rdb.GetDel(ctx, beklemeAnahtar(id)).Bytes())
}

func (d *IstekDepo) beklemeCoz(veri []byte, err error) (BaglamaBekleme, error) {
	if errors.Is(err, redis.Nil) {
		return BaglamaBekleme{}, ErrBeklemeYok
	}
	if err != nil {
		return BaglamaBekleme{}, fmt.Errorf("baglama beklemesi okunamadi: %w", err)
	}
	var b BaglamaBekleme
	if err := json.Unmarshal(veri, &b); err != nil {
		return BaglamaBekleme{}, fmt.Errorf("baglama beklemesi cozumlenemedi: %w", err)
	}
	return b, nil
}

// Cakisma, federasyon akisinin GIRIS YAPMADAN durdugu durumu anlatir.
// Iki bicimi var:
//
//	SifreliHesap = true  -> /baglama sayfasi: mevcut hesabin sifresi BIR
//	                        KEZ sorulur ve dogruysa kimlik baglanir.
//	SifreliHesap = false -> 409 cakisma sayfasi (K1): sorulacak sifre
//	                        yok, baglama YOK, yeni hesap YOK.
type Cakisma struct {
	// BeklemeID, /baglama akisinin kayit anahtari. Yalnizca
	// SifreliHesap true oldugunda doludur.
	BeklemeID string
	// Eposta, cakisan adres (kullaniciya gosterilir).
	Eposta string
	// MevcutSaglayicilar, 409 metninin "hangi yontemle gir" kismini
	// besler. BOS olabilir; o zaman saglayici adi UYDURULMAZ.
	MevcutSaglayicilar []string
	SifreliHesap       bool

	// Asagidakiler DISA ACIK DEGIL: yalnizca cakismaYonlendir'in
	// bekleme kaydini yazmasi icin tasinirlar. Formdan GELMEZLER.
	userID    string
	saglayici string
	sub       string
	csrf      string
}

// kullaniciCoz, dogrulanmis bir yukari akis id_token'indan bizim
// kullanicimizi bulur.
//
// SIRA GUVENLIGIN KENDISIDIR:
//
//  1. (saglayici, sub) -- bagli hesap her zaman once. Kullanicinin
//     saglayicidaki e-postasi degisebilir; e-postaya gore aramak onu
//     BASKA bir hesaba (ya da hic hesaba) goturebilirdi.
//  2. Gizli aktarma adresi -> cakisma kontrolu YOK (spec yasagi).
//  3. E-posta ile hesap yok -> yeni hesap.
//  4. Hesap var ve SIFRELI -> baglama sayfasi (sifre bir kez).
//  5. Hesap var ve SIFRESIZ -> 409 (K1).
//
// 4 ve 5'te OTOMATIK BAGLAMA YAPILMAZ, email_verified true olsa bile.
// Spec'teki saldiri: e-posta dogrulamasini BIZ yapmadigimiz icin
// saldirgan kurbanin adresiyle sifreli bir hesap acabilir; kurban
// Google ile girip otomatik baglanirsa SIFRESI SALDIRGANDA olan bir
// hesaba duser.
func (f *FedereSayfalar) kullaniciCoz(
	ctx context.Context, saglayici string, iddia *auth.Claims,
) (*User, *Cakisma, error) {
	// 1) Bagli kimlik her zaman ONCE.
	ku, err := f.federe.KimlikleKullanici(ctx, saglayici, iddia.UserID)
	if err == nil {
		return ku, nil, nil
	}
	if !errors.Is(err, ErrKimlikYok) {
		return nil, nil, fmt.Errorf("federe kimlik okunamadi: %w", err)
	}

	eposta := normalizeEmail(iddia.Email)
	// 2) Gizli aktarma adresinde cakisma kontrolu HIC yapilmaz.
	gizliAktarma := strings.HasSuffix(strings.ToLower(eposta), gizliAktarmaSoneki)

	// E-posta BOS ise de arama yapilmaz: bos adresle bir hesap aramak
	// anlamsizdir ve tekillik kisiti olusturmayi zaten engeller.
	if !gizliAktarma && eposta != "" {
		mevcut, err := f.kullanici.ByEmail(ctx, eposta)
		switch {
		// 4) Hesap var ve SIFRELI -> baglama sayfasi.
		case err == nil && mevcut.PasswordHash != "":
			c, err := f.cakismaKur(mevcut, eposta, saglayici, iddia)
			if err != nil {
				return nil, nil, err
			}
			return nil, c, nil
		// 5) Hesap var ve SIFRESIZ -> 409 (K1). Baglama YOK, yeni hesap
		//    YOK: sorulacak bir sifre yoktur ve hesabin ayni kisiye ait
		//    oldugunu kanitlayamayiz.
		case err == nil:
			return nil, &Cakisma{
				Eposta:             eposta,
				MevcutSaglayicilar: f.saglayicilar(ctx, mevcut.ID),
				SifreliHesap:       false,
			}, nil
		case !errors.Is(err, ErrUserNotFound):
			return nil, nil, fmt.Errorf("kullanici okunamadi: %w", err)
		}
	}

	// 3) Hesap yok (veya gizli aktarma adresi) -> yeni federe hesap.
	//
	// AMA e-posta BOS ise hesap ACILMAZ. users tablosunda "email NOT
	// NULL" ve "UNIQUE (lower(email))" var (migrations/001_kullanici.sql),
	// yani e-postasiz EN FAZLA BIR federe hesap olusabilir. Yukari akis
	// bir kez e-posta gondermezse ikinci boyle kullanici 23505 ->
	// ErrEpostaKullanimda ile duser ve BOS e-posta gosteren bir 409
	// cakisma sayfasina cakilir; o kullanici bir daha HIC kaydolamaz,
	// yani KALICI olarak kilitlenir. Olasilik dusuk, acilan kapi agir.
	//
	// Cozum semayi degistirmek DEGIL (email NULL'lanabilir yapmak
	// calisir bir uretim tablosunu etkiler ve bu turun kapsami disi),
	// sessizce bos e-postayla hesap acmak yerine ACIKCA reddetmek ve
	// kullaniciya ne yapacagini soylemek.
	if eposta == "" {
		return nil, nil, ErrYukariAkisEpostaYok
	}
	yeni, err := f.federe.FedereKullaniciOlustur(
		ctx, saglayici, iddia.UserID, eposta, iddia.Name, bool(iddia.EmailVerified))
	if errors.Is(err, ErrEmailInUse) {
		// Gizli aktarma adresi zaten kayitli ya da arada bir yaris
		// olustu. Her iki halde de BAGLAMA YOK: o satirin ayni kisiye
		// ait oldugunu kanitlayamayiz.
		return nil, &Cakisma{Eposta: eposta, SifreliHesap: false}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("federe kullanici olusturulamadi: %w", err)
	}
	return yeni, nil, nil
}

// saglayicilar, 409 metnini beslemek icin kullanicinin bagli oldugu
// saglayici adlarini doner. Hata YUTULMAZ ama AKISI DURDURMAZ: liste
// yalnizca gosterim icindir ve bos liste "baska bir yontem" metnine
// duser (bkz. saglayiciMetni).
func (f *FedereSayfalar) saglayicilar(ctx context.Context, userID string) []string {
	sag, err := f.federe.SaglayicilarByUserID(ctx, userID)
	if err != nil {
		log.Printf("federasyon: saglayici listesi okunamadi: %v", err)
		return nil
	}
	return sag
}

// cakismaKur, SIFRELI bir hesapla cakismada /baglama akisinin
// kimliklerini uretir. Kaydi Redis'e YAZMAZ: authRequestID'yi
// cakismaYonlendir tasir, boylece tek bir yazma noktasi olur.
func (f *FedereSayfalar) cakismaKur(
	mevcut *User, eposta, saglayici string, iddia *auth.Claims,
) (*Cakisma, error) {
	beklemeID, err := rasgeleJeton()
	if err != nil {
		return nil, fmt.Errorf("bekleme id uretilemedi: %w", err)
	}
	csrf, err := rasgeleJeton()
	if err != nil {
		return nil, fmt.Errorf("csrf uretilemedi: %w", err)
	}
	return &Cakisma{
		BeklemeID:    beklemeID,
		Eposta:       eposta,
		SifreliHesap: true,
		userID:       mevcut.ID,
		saglayici:    saglayici,
		sub:          iddia.UserID,
		csrf:         csrf,
	}, nil
}

// cakismaYonlendir, cakismanin iki bicimini yanita cevirir: sifreli
// hesapta /baglama sayfasina yonlendirir, sifresiz hesapta 409 cakisma
// sayfasini yazar. HICBIR dalda giris YAPILMAZ.
func (f *FedereSayfalar) cakismaYonlendir(
	w http.ResponseWriter, r *http.Request, authRequestID string, c *Cakisma,
) {
	if !c.SifreliHesap {
		f.cakismaRender(w, authRequestID, c.Eposta, c.MevcutSaglayicilar)
		return
	}
	bekleme := BaglamaBekleme{
		AuthRequestID: authRequestID,
		UserID:        c.userID,
		Saglayici:     c.saglayici,
		Sub:           c.sub,
		Eposta:        c.Eposta,
		CSRF:          c.csrf,
	}
	if err := f.durum.BeklemeYaz(r.Context(), c.BeklemeID, bekleme, baglamaBeklemeTTL); err != nil {
		// beklemeID, csrf ve e-posta LOGLANMAZ.
		log.Printf("federasyon: baglama beklemesi yazilamadi: %v", err)
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	// URL Go tarafinda url.Values ile kurulur, sablonda string
	// birlestirmeyle DEGIL.
	hedef := yolBaglama + "?" + url.Values{alanBeklemeID: {c.BeklemeID}}.Encode()
	http.Redirect(w, r, hedef, http.StatusFound)
}

// linkData, baglama.html sablonuna verilen veridir.
type linkData struct {
	Email     string
	PendingID string
	CSRF      string
	Error     string
	LinkPath  string
}

// conflictData, cakisma.html'in bekledigi alanlardir. Cakisma yapisi
// DOGRUDAN gecilmez: sablon, kullaniciya GOSTERILECEK metni ister,
// saglayici listesini degil.
type conflictData struct {
	Email        string
	ProviderText string
	// LoginURL, Go tarafinda url.Values ile kurulur; sablonda string
	// birlestirme YAPILMAZ.
	LoginURL string
}

// saglayiciEtiket, saglayici adini kullaniciya gosterilecek etikete
// cevirir. Bilinmeyen bir ad UYDURULMAZ, oldugu gibi gosterilir.
func saglayiciEtiket(ad string) string {
	switch ad {
	case saglayiciGoogle:
		return "Google"
	case saglayiciApple:
		return "Apple"
	default:
		return ad
	}
}

// saglayiciMetni, listeyi kullaniciya gosterilecek tek bir ifadeye
// cevirir. Liste BOS olabilir (ornegin gizli aktarma adresi zaten
// kayitliysa); o zaman saglayici adi UYDURULMAZ.
func saglayiciMetni(sag []string) string {
	switch len(sag) {
	case 0:
		return "baska bir yontem"
	case 1:
		return saglayiciEtiket(sag[0])
	default:
		etiketler := make([]string, 0, len(sag))
		for _, s := range sag {
			etiketler = append(etiketler, saglayiciEtiket(s))
		}
		return strings.Join(etiketler, " veya ")
	}
}

// cakismaRender, K1'in 409 sayfasini yazar: baglama YOK, yeni hesap
// YOK, yalnizca "hangi yontemle gir" bilgisi.
func (f *FedereSayfalar) cakismaRender(w http.ResponseWriter, authRequestID, eposta string, sag []string) {
	veri := conflictData{
		Email:        eposta,
		ProviderText: saglayiciMetni(sag),
		LoginURL:     yolGiris + "?" + url.Values{"authRequestID": {authRequestID}}.Encode(),
	}
	w.WriteHeader(http.StatusConflict)
	if err := sablonlar.ExecuteTemplate(w, "cakisma.html", veri); err != nil {
		log.Printf("cakisma sablonu yazilamadi: %v", err)
	}
}

func (f *FedereSayfalar) baglamaRender(w http.ResponseWriter, kod int, b BaglamaBekleme, beklemeID, hata string) {
	veri := linkData{
		Email:     b.Eposta,
		PendingID: beklemeID,
		CSRF:      b.CSRF,
		Error:     hata,
		LinkPath:  yolBaglama,
	}
	w.WriteHeader(kod)
	if err := sablonlar.ExecuteTemplate(w, "baglama.html", veri); err != nil {
		log.Printf("baglama sablonu yazilamadi: %v", err)
	}
}

// BaglamaBagla, /baglama sayfasini mux'a baglar.
//
// POST issuer aracisiyla SARILIR (geriCagirma issuer'i baglamdan
// okur). Hiz limiti GIRIS ucundan AYRI bir sayac kullanir: /baglama,
// bu sistemdeki IKINCI sifre deneme yuzeyidir ve formu acmak ile sifre
// denemek ayni butceyi tuketmemelidir.
//
// hiz nil GECILEBILIR (yalnizca birim testler icin).
func (f *FedereSayfalar) BaglamaBagla(mux *http.ServeMux, araci *op.IssuerInterceptor, hiz *HizLimit) {
	// hiz, HESAP BASINA sayac icin handler'da da gerekir: o sayacin
	// anahtari ancak bekleme kaydi okunduktan sonra bilinir (bkz.
	// HizLimit.HesapSayaci).
	f.hiz = hiz
	var get http.Handler = http.HandlerFunc(f.baglamaGet)
	post := araci.Handler(http.HandlerFunc(f.baglamaPost))
	if hiz != nil {
		get = hiz.SayfaGetSar("baglama", get)
		post = hiz.BaglamaSar(post)
	}
	mux.Handle("GET "+yolBaglama, get)
	mux.Handle("POST "+yolBaglama, post)
}

// baglamaBaglamaDogrula, /baglama istegi icin IKI kosulu birlikte
// dogrular: (1) gecerli bir bekleme kaydi var, (2) kaydin bagli oldugu
// auth istegi BU tarayiciya bagli (Faz 1'in oturum cerezi; federasyon
// cerezi Path=/federasyon oldugu icin /baglama'ya HIC gonderilmez).
//
// KAYIT BURADA TUKETILMEZ. GETDEL'i dogrulamalardan ONCE yapmak, bir
// beklemeID'yi ogrenen ucuncu bir tarafin CEREZSIZ tek bir POST ile
// kurbanin bekleyen baglamasini YOK ETMESINE izin verirdi (kurban tum
// federasyon turunu bastan yapmak zorunda kalirdi). Tuketme, sifre
// dogrulandiktan SONRA baglamaPost'ta ACIKCA yapilir.
func (f *FedereSayfalar) baglamaBaglamaDogrula(
	r *http.Request, beklemeID string,
) (BaglamaBekleme, error) {
	if beklemeID == "" {
		return BaglamaBekleme{}, ErrBeklemeYok
	}
	b, err := f.durum.BeklemeOku(r.Context(), beklemeID)
	if err != nil {
		return BaglamaBekleme{}, err
	}
	// Oturum baglamasi: akisi BASLATAN tarayici ile baglamayi
	// TAMAMLAYAN tarayici ayni olmak zorunda.
	if _, err := oturumBaglamaDogrula(r.Context(), f.oturumlar, r, b.AuthRequestID); err != nil {
		return BaglamaBekleme{}, err
	}
	return b, nil
}

// baglamaGet, sifre formunu YALNIZCA gecerli bir bekleme baglamasi
// varsa gosterir.
//
// Faz 1'in C1 dersi: baglamasiz bir istege form RENDER ETMEK, zafiyetin
// KENDISIDIR. Bu yuzden form burada MINTLENMEZ, var olan bir bekleme
// TALEP edilir.
func (f *FedereSayfalar) baglamaGet(w http.ResponseWriter, r *http.Request) {
	beklemeID := r.URL.Query().Get(alanBeklemeID)
	b, err := f.baglamaBaglamaDogrula(r, beklemeID)
	if err != nil {
		oturumRed(w, "baglama-get", err)
		return
	}
	f.baglamaRender(w, http.StatusOK, b, beklemeID, "")
}

// baglamaPost, mevcut hesabin sifresini BIR KEZ dogrular ve dogruysa
// federe kimligi o hesaba baglar.
func (f *FedereSayfalar) baglamaPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form okunamadi", http.StatusBadRequest)
		return
	}
	beklemeID := r.FormValue(alanBeklemeID)
	// Kayit TUKETILMEDEN okunur; tuketme asagida, sifre dogrulandiktan
	// SONRA yapilir (bkz. baglamaBaglamaDogrula).
	b, err := f.baglamaBaglamaDogrula(r, beklemeID)
	if err != nil {
		oturumRed(w, "baglama", err)
		return
	}
	// CSRF jetonu kayitta SUNUCUDA saklanir: dogrulama fiilen "bu
	// jetonu bu bekleme icin biz verdik mi" sorusudur.
	if !csrfEsit(b.CSRF, r.FormValue(csrfAlan)) {
		oturumRed(w, "baglama", ErrCSRF)
		return
	}

	mevcut, err := f.kullanici.ByID(r.Context(), b.UserID)
	if err != nil {
		// E-posta ve kullanici id'si LOGLANMAZ.
		log.Printf("federasyon: baglanacak hesap okunamadi: %v", err)
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	// Sifresiz hesap bu sayfaya HIC gelmemeli (kullaniciCoz K1 dalinda
	// 409 doner); gelirse derinlemesine savunma olarak burada da 409.
	// SifreDogru bos hash'te zaten false doner, ama o yol kullaniciya
	// "sifreni yanlis girdin" derdi ve kullanici ASLA giremeyecegi bir
	// formda sonsuza kadar denerdi.
	if mevcut.PasswordHash == "" {
		log.Print("federasyon: sifresiz hesap icin baglama denendi")
		f.cakismaRender(w, b.AuthRequestID, b.Eposta, f.saglayicilar(r.Context(), mevcut.ID))
		return
	}
	// HESAP BASINA hiz limiti: sifre dogrulamasindan ONCE. IP sayaci
	// dagitik bir denemeyi (her istek farkli IP) durdurmaz; bu sayac
	// TEK bir hesaba yonelen denemeleri, kaynaktan ve kullanilan
	// bekleme kaydindan BAGIMSIZ olarak sinirlar. Yeni bir bekleme
	// kaydi federasyon turunu tekrarlayarak istenildigi kadar
	// alinabildigi icin tek basina IP sayaci yeterli DEGILDIR.
	// hiz nil GECILEBILIR (yalnizca birim testler); uretimde Start her
	// zaman bir limit kurar ve kurulamazsa HIC BASLAMAZ.
	if f.hiz != nil && !f.hiz.HesapSayaci(w, r, b.UserID) {
		return
	}
	if !mevcut.PasswordMatches(r.FormValue("sifre")) {
		// Kayit TUKETILMEZ: bir yazim hatasi tum federasyon turunu
		// bastan yapmayi gerektirmesin. Denemeleri sinirlayan sey hiz
		// limitleridir (IP basina + yukaridaki hesap basina sayac).
		//
		// Sifre LOGLANMAZ ve hata metni hesabin varligini sizdirmaz:
		// hataGirisBasarisiz, giris sayfasiyla AYNI tek mesajdir.
		f.baglamaRender(w, http.StatusUnauthorized, b, beklemeID, hataGirisBasarisiz)
		return
	}

	// Sifre DOGRU: kayit ancak simdi ATOMIK olarak tuketilir (GETDEL).
	// TEK KULLANIMLIK olmasi, dogru sifre bir kez girildikten sonra ayni
	// kayitla ikinci bir baglamanin (baska bir auth istegini tamamlamak
	// dahil) yapilmasini engeller.
	if _, err := f.durum.BeklemeAl(r.Context(), beklemeID); err != nil {
		// Yaris: kayit arada tuketilmis. Baglamayi YAPMADAN durulur.
		oturumRed(w, "baglama", err)
		return
	}

	if err := f.federe.Bagla(r.Context(), b.Saglayici, b.Sub, b.UserID); err != nil {
		log.Printf("federasyon: kimlik baglanamadi: %v", err)
		if errors.Is(err, ErrKimlikKullanimda) {
			// Kimlik arada baska bir hesaba baglanmis ya da bu hesap bu
			// saglayiciya zaten bagli: sessizce ezmek yerine acikca
			// durulur.
			f.cakismaRender(w, b.AuthRequestID, b.Eposta, f.saglayicilar(r.Context(), b.UserID))
			return
		}
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	if err := f.istekler.TamamlandiIsaretle(r.Context(), b.AuthRequestID, b.UserID, []string{amrFederasyon}); err != nil {
		// authRequestID LOGLANMAZ.
		log.Printf("federasyon: istek tamamlanamadi: %v", err)
		// Geri bacagiyla AYNI siniflandirma: 409 / 400 (TTL ile dustu) /
		// 500. baglamaBeklemeTTL ile istekTTL ust uste bindigi icin bu
		// bacak da auth istegini GECEBILIR.
		f.istekTamamlamaHatasi(w, err)
		return
	}
	http.Redirect(w, r, f.geriCagirma(r.Context(), b.AuthRequestID), http.StatusFound)
}
