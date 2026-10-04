package kimlik

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// cerezAdOturum, auth istegini BASLATAN tarayiciya verilen cerezin
// adidir. authRequestID tek basina yetki girdisi olamaz: linki
// baskasina yollayip girisi ona yaptirmak, kodu ise kendi tarayicisinda
// toplamak mumkundur (hesap devralma). Cerez, auth istegini baslatan
// tarayiciyi istege baglar.
//
// BAGLAMA YALNIZCA /authorize'DA KURULUR (bkz. Sayfalar.AuthorizeSar).
// Eskiden GET /giris ve GET /kayit da baglama MINTLERDI ve bu, korumayi
// fiilen kaldiriyordu: saldirgan kendi tarayicisinda
// GET /giris?authRequestID=<X> cagirip X'e kendi cerezini baglayabilir,
// sonra ayni linki kurbana yollayip girisi ONA yaptirabilir ve kodu
// kendi tarayicisinda toplayabilirdi. Baglama kontrolu "cerezime bagli
// id, istedigim id'ye esit mi" sorusu oldugu icin saldirganin
// kendi mintledigi cerezle GECIYORDU. Simdi baglama yalnizca istegi
// GERCEKTEN baslatan tarayiciya verilir; GET formlari var olan bir
// baglama TALEP eder, mintlemez.
//
// Baglama DORT noktada da zorunludur: GET /giris, GET /kayit,
// POST /giris + POST /kayit (ayrica CSRF) ve /authorize/callback.
const cerezAdOturum = "kimlik_oturum"

// oturumBaytSayisi, oturum ve CSRF degerlerinin ham entropisi. 32 bayt
// (256 bit) tahmin edilemez olmak icin fazlasiyla yeterli.
const oturumBaytSayisi = 32

// oturumBaglamaUstSinir, TEK bir cerezin ayni anda bagli kalabilecegi en
// fazla authRequestID sayisidir.
//
// NEDEN BIR KUME (cok sayida id) GEREKLI: tek cerez adi var ve Path=/,
// yani bir tarayicida tek cerez tutulur. Baglama /authorize'da
// kuruldugundan, kullanici iki sekmede paralel akis baslatirsa ikinci
// /authorize cerezi EZERDI ve birinci sekmenin POST'u 403 olurdu.
// Baglamayi "oturum -> {authRequestID}" kumesi olarak tutmak paralel
// akisi korur; her bacak hala BIREBIR esitlik dogrular ("bu cerez bu
// id'ye bagli mi"), yani bir cerezin BASKA bir tarayicinin istegine
// erisimi yok.
//
// NEDEN "ILK BAGLAYAN KAZANIR" DEGIL: o yamada saldirgan on-baglama ile
// ilk baglayan olur ve kurbanin mesru girisini 403'e dusurur; devralma
// yerine DoS cikar.
//
// NEDEN 10: sinirsiz buyume bir sisirme yuzeyi olur (ayni cerezle
// /authorize'i tekrar tekrar cagirip tek bir Redis degerini sure siz
// buyutmek). Insan kullanimda paralel giris sekmesi sayisi tek
// hanededir; 10 bunun rahat ustunde ve kaydin boyutunu kucuk tutar
// (10 * (~36 bayt uuid + 43 karakter CSRF) < 1 KB). Sinir dolduysa EN
// ESKI kayit duser (FIFO): sadece o cerezin kendi en eski sekmesi
// etkilenir, baska tarayicilar etkilenmez.
const oturumBaglamaUstSinir = 10

// Oturum baglamasi hatalari. Hepsi kullaniciya AYNI genel mesajla
// doner; ayirmak saldirgana hangi kosulun tutmadigini soyler.
var (
	// ErrOturumYok, cerez degeri icin kayitli bir baglama yok (hic
	// olusmamis, suresi dolmus veya uydurma bir cerez).
	ErrOturumYok = errors.New("oturum baglamasi bulunamadi")

	// ErrOturumEslesmedi, cereze bagli authRequestID ile istekte gelen
	// authRequestID birebir ayni degil.
	ErrOturumEslesmedi = errors.New("oturum baglamasi istekle eslesmiyor")

	// ErrOturumCerezYok, istekte hic oturum cerezi yok.
	ErrOturumCerezYok = errors.New("oturum cerezi yok")

	// ErrCSRF, form/istek CSRF jetonu cereze bagli jetonla eslesmiyor.
	ErrCSRF = errors.New("csrf jetonu gecersiz")
)

// OturumKayit, tek bir auth istegi icin baglamayi tutar: istegin id'si
// ve o istek icin o tarayiciya verilen CSRF jetonu. CSRF jetonu cerezin
// yaninda SUNUCUDA saklanir; yani jeton dogrulamasi fiilen "bu jetonu
// bu cerez ve bu istek icin biz verdik mi" sorusudur.
type OturumKayit struct {
	AuthRequestID string
	CSRF          string
}

// OturumBaglama, bir tarayici cerezinin hangi auth isteklerine bagli
// oldugunu tutar. Kume (bire-cok) olmasinin nedeni paralel akistir;
// gerekce ve ust sinir icin bkz. oturumBaglamaUstSinir.
//
// Alan disa acik (buyuk harfli) olmak ZORUNDA: kayit Redis'e JSON
// olarak yazilir ve encoding/json kucuk harfli alanlari sessizce atlar.
type OturumBaglama struct {
	Kayitlar []OturumKayit
}

// CSRFBul, verilen authRequestID bu cereze BAGLI ise o istege ait CSRF
// jetonunu doner. Karsilastirma birebir esitliktir.
func (b OturumBaglama) CSRFBul(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	for _, k := range b.Kayitlar {
		if k.AuthRequestID == id {
			return k.CSRF, true
		}
	}
	return "", false
}

// Ekle, bir authRequestID'yi (ve ona ait CSRF jetonunu) baglamaya
// ekler. Ayni id zaten varsa CSRF jetonu YENILENIR (kayit cogaltilmaz).
// Kume oturumBaglamaUstSinir'i asarsa EN ESKI kayit duser.
func (b *OturumBaglama) Ekle(id, csrf string) {
	for i := range b.Kayitlar {
		if b.Kayitlar[i].AuthRequestID == id {
			b.Kayitlar[i].CSRF = csrf
			return
		}
	}
	b.Kayitlar = append(b.Kayitlar, OturumKayit{AuthRequestID: id, CSRF: csrf})
	if fazla := len(b.Kayitlar) - oturumBaglamaUstSinir; fazla > 0 {
		b.Kayitlar = b.Kayitlar[fazla:]
	}
}

// OturumDepo, cerez -> OturumBaglama eslemesini tutar. Uretimde
// IstekDepo (Redis) karsilar; testler SahteOturumDepo ile agsiz koshar.
type OturumDepo interface {
	OturumYaz(ctx context.Context, oturum string, baglama OturumBaglama, ttl time.Duration) error
	OturumOku(ctx context.Context, oturum string) (OturumBaglama, error)
}

// rasgeleJeton, crypto/rand ile oturumBaytSayisi baytlik bir deger
// uretir ve URL'de guvenli bicimde doner. Hata YUTULMAZ: zayif/bos bir
// oturum degeri ile devam etmek baglamayi fiilen kaldirir.
func rasgeleJeton() (string, error) {
	b := make([]byte, oturumBaytSayisi)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rasgele deger uretilemedi: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// csrfEsit, CSRF karsilastirmasini sabit zamanda yapar.
func csrfEsit(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// oturumCerezi, oturum degerini tarayiciya yazar.
//
// guvenli (Secure bayragi) ISSUER'A gore belirlenir: https issuer'da
// ACIK, http:// yerel gelistirmede KAPALI. http'de Secure acilirsa
// tarayici cerezi hic saklamaz ve yerel akis kirilir.
//
// SameSite=Lax: akis her zaman ayni siteden bir form POST'u ile
// surdugu icin Lax yeterlidir ve /authorize'dan gelen GET
// yonlendirmesinde cerez gonderilmeye devam eder.
func oturumCerezi(oturum string, guvenli bool) *http.Cookie {
	return &http.Cookie{
		Name:     cerezAdOturum,
		Value:    oturum,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   guvenli,
		MaxAge:   int(istekTTL.Seconds()),
	}
}

// oturumBaglamaDogrula, istekteki cerezi okur, depodaki baglamayi alir
// ve beklenen authRequestID'nin bu cereze BIREBIR bagli oldugunu
// dogrular. Donen CSRF jetonu, cagiranin form/istek jetonuyla
// karsilastirmasi icindir.
func oturumBaglamaDogrula(
	ctx context.Context,
	depo OturumDepo,
	r *http.Request,
	beklenenID string,
) (string, error) {
	cerez, err := r.Cookie(cerezAdOturum)
	if err != nil || cerez.Value == "" {
		return "", ErrOturumCerezYok
	}
	baglama, err := depo.OturumOku(ctx, cerez.Value)
	if err != nil {
		return "", err
	}
	// Birebir esitlik: baslatan tarayici ile tamamlayan tarayici ayni
	// olmak zorunda. Kume birden fazla id tutabilir (paralel sekmeler)
	// ama her id yalnizca onu BASLATAN cerezde bulunur.
	csrf, bagliMi := baglama.CSRFBul(beklenenID)
	if !bagliMi {
		return "", ErrOturumEslesmedi
	}
	return csrf, nil
}
