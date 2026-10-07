package kimlik

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// YukariAkis, tek bir yukari akis saglayicisinin (Apple veya Google)
// /authorize adresini kurmak icin gereken her seyi tasir. JWKSURL ve
// Issuer, Gorev 2'nin auth.NewYukariAkisDogrulayici'sine verilir; bu
// struct kendisi dogrulama yapmaz, sadece yapilandirmayi tasir.
type YukariAkis struct {
	Ad, IstemciID, IstemciSir string

	AuthURL, TokenURL, JWKSURL, Issuer string

	Kapsamlar []string

	// PKCEDestekli: Google PKCE'yi destekler ve biz onu HER ZAMAN
	// kullaniriz (public client, secret yok). Apple'in PKCE destegi
	// DOKUMANTE DEGIL; bilinmeyen bir parametre gondermek akisi kirma
	// riski tasir, bu yuzden Apple icin bu alan BILEREK false birakilir
	// (bkz. TestAuthorizeURLApplePKCEGondermez). Bu bir unutma degil,
	// kayitli bir karardir: ileride biri "tutarlilik" adina PKCE'yi
	// Apple'a da eklemeye kalkisirsa, bu yorum onu durdurmali.
	PKCEDestekli bool

	// FormPost: Apple, name/email scope'u istenince yanitini GET
	// yerine POST (response_mode=form_post) ile doner. Geri donus
	// handler'i bu yuzden POST'u da kabul etmek zorunda (bkz. K2).
	FormPost bool

	// Apple'a ozgu (K5). Google icin bos kalir. JetonDegis (Gorev 4),
	// client_secret'i HER token degisiminde bu uc alandan ES256 ile
	// uretir; Apple'in secret'i sabit degil, kisa omurlu bir JWT'dir.
	AppleEkipID, AppleAnahtarID, AppleAnahtarP8 string
}

// AuthorizeURL, yukari akis saglayicisinin authorize adresini kurar.
// redirect_uri BIZIM geri donus adresimizdir ve saglayici tarafinda
// ayniyla kayitli olmak zorundadir.
//
// kodDogrulayici asla URL'e duz metin olarak girmez: PKCE destekliyse
// yalnizca onun SHA-256 ozetinin base64url'i (code_challenge) gonderilir.
// Dogrulayicinin kendisi yalnizca bizim tarafimizda (DurumYaz ile) saklanir
// ve token degisiminde saglayiciya dogrudan gonderilir.
func (y *YukariAkis) AuthorizeURL(state, nonce, kodDogrulayici, geriURL string) string {
	q := url.Values{}
	q.Set("client_id", y.IstemciID)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(y.Kapsamlar, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("redirect_uri", geriURL)
	if y.FormPost {
		q.Set("response_mode", "form_post")
	}
	if y.PKCEDestekli {
		ozet := sha256.Sum256([]byte(kodDogrulayici))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(ozet[:]))
		q.Set("code_challenge_method", "S256")
	}
	return y.AuthURL + "?" + q.Encode()
}

// kodDogrulayiciUretBaytSayisi: 32 ham bayt, base64url ile kodlandiginda
// 43 karaktere cikar (PKCE RFC 7636'nin alt siniri). Daha az bayt
// standardi ihlal eder, daha fazlasi fayda getirmez.
const kodDogrulayiciUretBaytSayisi = 32

// kodDogrulayiciUret, PKCE icin rasgele bir "code_verifier" ve onun S256
// "code_challenge"ini uretir. Hata YUTULMAZ: rasgele uretim basarisiz
// olursa (crypto/rand.Read hata donerse) bos/zayif bir dogrulayici ile
// devam etmek PKCE korumasini fiilen kaldirir (bkz. oturum.go,
// rasgeleJeton ile ayni disiplin).
func kodDogrulayiciUret() (dogrulayici, meydanOkuma string, err error) {
	b := make([]byte, kodDogrulayiciUretBaytSayisi)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("kod dogrulayicisi uretilemedi: %w", err)
	}
	dogrulayici = base64.RawURLEncoding.EncodeToString(b)
	ozet := sha256.Sum256([]byte(dogrulayici))
	meydanOkuma = base64.RawURLEncoding.EncodeToString(ozet[:])
	return dogrulayici, meydanOkuma, nil
}

// FedereDurum, /federasyon/{saglayici}/baslat ile /federasyon/{saglayici}/geri
// arasinda "state" parametresi uzerinden tasinan tum baglami tutar.
//
// Karsilastirilabilir (==) olmasi KASITLI: dilim/harita alani YOK. Bu,
// testin okunan != istenen ile dogrudan karsilastirma yapabilmesini
// saglar ve ayni zamanda Redis'teki kaydin minimal ve duz oldugunu
// garanti eder.
type FedereDurum struct {
	// AuthRequestID: bizim OP'imizdeki asil auth istegi (mobil istemcinin
	// baslattigi). Federasyon akisi bittiginde bu istege Subject yazilir.
	AuthRequestID string
	// Nonce: id_token'in nonce iddiasiyla karsilastirilir (replay'e
	// karsi, bkz. auth.DogrulaNonce).
	Nonce string
	// Saglayici: "google" | "apple". Geri donus handler'i callback'in
	// beklenen saglayiciya ait oldugunu dogrulamak icin kullanir.
	Saglayici string
	// Cerez: bu akisi baslatan tarayicinin cerez degeri (oturum.go'daki
	// desenle ayni gerekce: authRequestID tek basina yetki girdisi
	// degildir, baslatan tarayici ile tamamlayan tarayici ayni olmali).
	Cerez string
	// KodDogrulayici: PKCE code_verifier. Token degisiminde saglayiciya
	// gonderilir; LOGLANMAMALI (sir niteliginde).
	KodDogrulayici string
}

// ErrDurumYok, state icin kayitli bir FedereDurum yok (hic olusmamis,
// suresi dolmus veya zaten tuketilmis).
var ErrDurumYok = errors.New("federasyon durumu bulunamadi")

// DurumDepo, federasyon "state" kaydinin yazilip tek seferlik okunmasini
// tanimlar. OturumDepo gibi, IstekDepo tarafindan karsilanir.
type DurumDepo interface {
	DurumYaz(ctx context.Context, state string, d FedereDurum) error
	DurumAl(ctx context.Context, state string) (FedereDurum, error)
}

var _ DurumDepo = (*IstekDepo)(nil)

// federasyonDurumTTL, kullanicinin yukari akis saglayicisinda gecirdigi
// sureye yetecek kadar uzun, calinmis bir state'in ise degerini hizla
// yitirmesine yetecek kadar kisa olmali. 10 dakika: Apple'in iki
// adimli onayi dahil bol bol yeterli.
const federasyonDurumTTL = 10 * time.Minute

func durumAnahtar(state string) string { return "kimlik:fed:" + state }

// DurumYaz, federasyon akisinin baslangicindaki baglami state anahtari
// altinda Redis'e yazar.
func (d *IstekDepo) DurumYaz(ctx context.Context, state string, durum FedereDurum) error {
	veri, err := json.Marshal(durum)
	if err != nil {
		return fmt.Errorf("federasyon durumu serilenemedi: %w", err)
	}
	if err := d.rdb.Set(ctx, durumAnahtar(state), veri, federasyonDurumTTL).Err(); err != nil {
		return fmt.Errorf("federasyon durumu yazilamadi: %w", err)
	}
	return nil
}

// DurumAl, state kaydini okur ve ATOMIK olarak siler (GETDEL): kayit TEK
// KULLANIMLIKTIR, aksi halde saldirgan yakaladigi bir callback URL'ini
// (adres cubugu, log, proxy) tekrar oynatip ayni federasyon akisini
// ikinci kez tamamlayabilir. Kod degisimiyle ayni desen (bkz.
// IstekDepo.KodlaOku).
func (d *IstekDepo) DurumAl(ctx context.Context, state string) (FedereDurum, error) {
	veri, err := d.rdb.GetDel(ctx, durumAnahtar(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return FedereDurum{}, ErrDurumYok
	}
	if err != nil {
		return FedereDurum{}, fmt.Errorf("federasyon durumu okunamadi: %w", err)
	}
	var durum FedereDurum
	if err := json.Unmarshal(veri, &durum); err != nil {
		return FedereDurum{}, fmt.Errorf("federasyon durumu cozumlenemedi: %w", err)
	}
	return durum, nil
}
