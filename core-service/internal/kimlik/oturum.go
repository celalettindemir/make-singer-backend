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

// cerezAdOturum, hosted giris/kayit formunu BASLATAN tarayiciya verilen
// cerezin adidir. authRequestID tek basina yetki girdisi olamaz: linki
// baskasina yollayip girisi ona yaptirmak, kodu ise kendi tarayicisinda
// toplamak mumkundur (hesap devralma). Cerez, auth istegini baslatan
// tarayiciyi istege baglar ve bu baglama UC noktada da zorunludur:
// POST /giris, POST /kayit ve /authorize/callback.
const cerezAdOturum = "kimlik_oturum"

// oturumBaytSayisi, oturum ve CSRF degerlerinin ham entropisi. 32 bayt
// (256 bit) tahmin edilemez olmak icin fazlasiyla yeterli.
const oturumBaytSayisi = 32

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

// OturumBaglama, bir tarayici cerezinin hangi auth istegine bagli
// oldugunu ve o tarayiciya verilen CSRF jetonunu tutar. CSRF jetonu
// cerezin yaninda SUNUCUDA saklanir; yani jeton dogrulamasi fiilen
// "bu jetonu bu cerezle birlikte biz verdik mi" sorusudur.
type OturumBaglama struct {
	AuthRequestID string
	CSRF          string
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
// ve bagli authRequestID'nin beklenen id ile BIREBIR ayni oldugunu
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
	// olmak zorunda.
	if baglama.AuthRequestID == "" || baglama.AuthRequestID != beklenenID {
		return "", ErrOturumEslesmedi
	}
	return baglama.CSRF, nil
}
