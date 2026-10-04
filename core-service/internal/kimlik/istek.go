package kimlik

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

const (
	istekTTL = 10 * time.Minute
	kodTTL   = 60 * time.Second
)

// ErrIstekYok, istenen auth istegi (veya kod) Redis'te bulunamadiginda
// donulur. Suresi dolmus, hic olusmamis ya da (kod icin) zaten tuketilmis
// olabilir.
var ErrIstekYok = errors.New("auth istegi bulunamadi")

// ErrIstekZatenTamamlandi, zaten tamamlanmis bir auth istegi BASKA bir
// kullaniciya yeniden baglanmak istendiginde donulur (bkz.
// TamamlandiIsaretle).
var ErrIstekZatenTamamlandi = errors.New("auth istegi zaten tamamlandi")

// AuthIstek, op.AuthRequest arayuzunu karsilayan, Redis'te JSON olarak
// saklanan authorization request kaydidir. Tum alanlar disa acik
// (buyuk harfli) olmali; aksi halde encoding/json sessizce atlar ve
// kayit Redis'e yazilirken veri kaybolur.
type AuthIstek struct {
	ID                  string
	ClientID            string
	RedirectURI         string
	Scopes              []string
	ResponseType        oidc.ResponseType
	ResponseMode        oidc.ResponseMode
	State               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod oidc.CodeChallengeMethod

	// IpucuSubject, id_token_hint ile gelen "sub" degeridir (kutuphane
	// CreateAuthRequest'in userID parametresinde verir). SADECE ipucu
	// olarak tutulur: ne istek ID'si olarak kullanilir ne de Done()'u
	// etkiler. Istek ID'si olarak kullanilmasi es zamanli iki akisin
	// ayni Redis anahtarini ezmesine yol acardi.
	IpucuSubject string

	// Giris tamamlaninca doldurulur.
	Subject  string
	AuthTime time.Time
}

var _ op.AuthRequest = (*AuthIstek)(nil)

func (a *AuthIstek) GetID() string { return a.ID }

// GetACR: makesinger tek bir kimlik dogrulama sinifi kullaniyor, bu
// yuzden sabit/bos donuyoruz. Coklu ACR gerekirse burada genisletilir.
func (a *AuthIstek) GetACR() string { return "" }

// GetAMR: su an tek giris yontemi eposta+sifre oldugu icin sabit "pwd"
// donuyoruz.
func (a *AuthIstek) GetAMR() []string { return []string{"pwd"} }

// GetAudience: en az client ID'yi icermeli (op kutuphanesi bunu jetonun
// "aud" alaninda kullanir).
func (a *AuthIstek) GetAudience() []string { return []string{a.ClientID} }

func (a *AuthIstek) GetAuthTime() time.Time { return a.AuthTime }

func (a *AuthIstek) GetClientID() string { return a.ClientID }

// GetCodeChallenge: Challenge bos ise PKCE kullanilmiyor demektir, nil
// donmek zorunlu (op kutuphanesi nil'i "PKCE yok" olarak yorumlar).
//
// FAIL-CLOSED: method S256 DEGILSE (plain veya bos) challenge'i
// AKTARMIYORUZ. Kutuphane bos/plain method'da duz string
// karsilastirmasi yapar (pkg/oidc/code_challenge.go VerifyCodeChallenge)
// ve bu, PKCE'yi fiilen etkisiz kilar. nil donmek, public client'ta
// token ucunun istegi "PKCE required" ile reddetmesini saglar
// (pkg/op/token_code.go, AuthMethodNone dali). Birinci katman
// /authorize'da reddetmektir (bkz. pkceZorlayici, sunucu.go); bu ikinci
// katman o kontrol atlanirsa bile kod degisimini kapatir.
func (a *AuthIstek) GetCodeChallenge() *oidc.CodeChallenge {
	if a.CodeChallenge == "" || a.CodeChallengeMethod != oidc.CodeChallengeMethodS256 {
		return nil
	}
	return &oidc.CodeChallenge{
		Challenge: a.CodeChallenge,
		Method:    a.CodeChallengeMethod,
	}
}

func (a *AuthIstek) GetNonce() string { return a.Nonce }

func (a *AuthIstek) GetRedirectURI() string { return a.RedirectURI }

func (a *AuthIstek) GetResponseType() oidc.ResponseType { return a.ResponseType }

func (a *AuthIstek) GetResponseMode() oidc.ResponseMode { return a.ResponseMode }

func (a *AuthIstek) GetScopes() []string { return a.Scopes }

func (a *AuthIstek) GetState() string { return a.State }

func (a *AuthIstek) GetSubject() string { return a.Subject }

// Done: yalnizca Subject dolu VE AuthTime sifir degilse true doner.
// Guvenlik acisindan kritik: giris tamamlanmadan Done() true donerse
// authorization code, kullanici hic dogrulanmadan verilir.
func (a *AuthIstek) Done() bool {
	return a.Subject != "" && !a.AuthTime.IsZero()
}

// IstekDepo, authorization request'leri ve tek kullanimlik authorization
// code'lari Redis'te tutar.
//
// Anahtar semasi:
//
//	kimlik:istek:<id>   -> JSON, TTL 10 dakika  (giris tamamlanana kadar)
//	kimlik:kod:<kod>    -> istek id'si, TTL 60 saniye
type IstekDepo struct {
	rdb *redis.Client
}

func NewIstekDepo(rdb *redis.Client) *IstekDepo { return &IstekDepo{rdb: rdb} }

func istekAnahtar(id string) string { return "kimlik:istek:" + id }
func kodAnahtar(kod string) string  { return "kimlik:kod:" + kod }

// Olustur, gelen authorization request'i AuthIstek'e cevirir ve Redis'e
// yazar. Istek ID'si HER ZAMAN burada uretilir: kutuphanenin
// CreateAuthRequest'e verdigi userID parametresi id_token_hint'in "sub"
// degeridir, bir istek ID'si DEGILDIR (pkg/op/auth_request.go,
// ValidateAuthReqIDTokenHint). Onu ID olarak kullanmak, es zamanli iki
// akisin ayni Redis anahtarini ezmesine yol acardi; bu yuzden ipucu
// yalnizca IpucuSubject alaninda saklanir.
func (d *IstekDepo) Olustur(ctx context.Context, req *oidc.AuthRequest, ipucuSubject string) (*AuthIstek, error) {
	istek := &AuthIstek{
		ID:                  uuid.NewString(),
		IpucuSubject:        ipucuSubject,
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		Scopes:              []string(req.Scopes),
		ResponseType:        req.ResponseType,
		ResponseMode:        req.ResponseMode,
		State:               req.State,
		Nonce:               req.Nonce,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
	}
	if err := d.yaz(ctx, istek, istekTTL); err != nil {
		return nil, err
	}
	return istek, nil
}

func (d *IstekDepo) yaz(ctx context.Context, istek *AuthIstek, ttl time.Duration) error {
	veri, err := json.Marshal(istek)
	if err != nil {
		return fmt.Errorf("istek serilenemedi: %w", err)
	}
	if err := d.rdb.Set(ctx, istekAnahtar(istek.ID), veri, ttl).Err(); err != nil {
		return fmt.Errorf("istek yazilamadi: %w", err)
	}
	return nil
}

// IDileOku, id'si verilen authorization request'i okur.
func (d *IstekDepo) IDileOku(ctx context.Context, id string) (*AuthIstek, error) {
	veri, err := d.rdb.Get(ctx, istekAnahtar(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrIstekYok
	}
	if err != nil {
		return nil, fmt.Errorf("istek okunamadi: %w", err)
	}
	var istek AuthIstek
	if err := json.Unmarshal(veri, &istek); err != nil {
		return nil, fmt.Errorf("istek cozumlenemedi: %w", err)
	}
	return &istek, nil
}

// KodKaydet, tamamlanmis bir authorization request'e ait kodu, istek
// id'sine esler. Kod 60 saniye sonra kendiliginden gecersiz olur.
func (d *IstekDepo) KodKaydet(ctx context.Context, id string, kod string) error {
	if err := d.rdb.Set(ctx, kodAnahtar(kod), id, kodTTL).Err(); err != nil {
		return fmt.Errorf("kod kaydedilemedi: %w", err)
	}
	return nil
}

// KodlaOku, kodu istek id'sine cozer ve kodu ATOMIK olarak siler
// (GETDEL): kod tek kullanimliktir, ikinci okuma ErrIstekYok doner.
// Boylece adres cubugundan veya loglardan kod kapan biri, kod zaten
// kullanildiysa jeton alamaz.
func (d *IstekDepo) KodlaOku(ctx context.Context, kod string) (*AuthIstek, error) {
	id, err := d.rdb.GetDel(ctx, kodAnahtar(kod)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrIstekYok
	}
	if err != nil {
		return nil, fmt.Errorf("kod okunamadi: %w", err)
	}
	return d.IDileOku(ctx, id)
}

// Sil, authorization request'i Redis'ten kaldirir (ornegin kod
// degisimi tamamlaninca kullanilir).
func (d *IstekDepo) Sil(ctx context.Context, id string) error {
	if err := d.rdb.Del(ctx, istekAnahtar(id)).Err(); err != nil {
		return fmt.Errorf("istek silinemedi: %w", err)
	}
	return nil
}

// TamamlandiIsaretle, istegi okur, Subject ve AuthTime'i doldurur ve
// geri yazar. Kalan TTL okunup ayni sure ile yeniden uygulanir; aksi
// halde her yeniden yazma TTL'i sifirlar ve istek gerekenden uzun
// yasar (ya da suresi dolmus bir kayit sonsuza kadar kalir).
//
// IKINCI CAGRI: zaten tamamlanmis bir istegin Subject'i EZILMEZ.
//   - Ayni kullanici icin: NO-OP (nil doner, yeniden yazilmaz).
//     Gerekce: durust bir cift gonderim (formu iki kez yollamak,
//     geri tusu) kullaniciya hata gostermemeli; sonuc zaten istenen
//     durumun aynisi. Yeniden yazmamak AuthTime'in gercek giris anini
//     korumasini da saglar.
//   - BASKA bir kullanici icin: REDDEDILIR (ErrIstekZatenTamamlandi).
//     Gerekce: bu, bekleyen bir auth istegini kod uretilene kadar
//     istenildigi kadar farkli kimlige yeniden yonlendirme yolunun
//     kendisiydi; sessizce ezmek yerine acikca basarisiz olmali.
func (d *IstekDepo) TamamlandiIsaretle(ctx context.Context, id string, userID string) error {
	istek, err := d.IDileOku(ctx, id)
	if err != nil {
		return err
	}
	if istek.Done() {
		if istek.Subject == userID {
			return nil
		}
		return ErrIstekZatenTamamlandi
	}
	ttl, err := d.rdb.TTL(ctx, istekAnahtar(id)).Result()
	if err != nil {
		return fmt.Errorf("TTL okunamadi: %w", err)
	}
	if ttl <= 0 {
		ttl = istekTTL
	}
	istek.Subject = userID
	istek.AuthTime = time.Now()
	return d.yaz(ctx, istek, ttl)
}

// ---- OturumDepo ----
//
// Cerez baglamasi, auth istegiyle AYNI depoda ve AYNI TTL ile tutulur:
// baglamanin istekten uzun yasamasi anlamsiz, kisa yasamasi akisi
// ortasinda kirar.

func oturumAnahtar(oturum string) string { return "kimlik:oturum:" + oturum }

var _ OturumDepo = (*IstekDepo)(nil)

// OturumYaz, cerez degerini (oturum) auth istegi ve CSRF jetonuna
// esler.
func (d *IstekDepo) OturumYaz(ctx context.Context, oturum string, baglama OturumBaglama, ttl time.Duration) error {
	veri, err := json.Marshal(baglama)
	if err != nil {
		return fmt.Errorf("oturum serilenemedi: %w", err)
	}
	if err := d.rdb.Set(ctx, oturumAnahtar(oturum), veri, ttl).Err(); err != nil {
		return fmt.Errorf("oturum yazilamadi: %w", err)
	}
	return nil
}

// OturumOku, cerez degerine bagli auth istegi ve CSRF jetonunu doner.
// Kayit yoksa ErrOturumYok doner.
func (d *IstekDepo) OturumOku(ctx context.Context, oturum string) (OturumBaglama, error) {
	veri, err := d.rdb.Get(ctx, oturumAnahtar(oturum)).Bytes()
	if errors.Is(err, redis.Nil) {
		return OturumBaglama{}, ErrOturumYok
	}
	if err != nil {
		return OturumBaglama{}, fmt.Errorf("oturum okunamadi: %w", err)
	}
	var baglama OturumBaglama
	if err := json.Unmarshal(veri, &baglama); err != nil {
		return OturumBaglama{}, fmt.Errorf("oturum cozumlenemedi: %w", err)
	}
	return baglama, nil
}
