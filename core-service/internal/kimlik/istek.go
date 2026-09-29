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
func (a *AuthIstek) GetCodeChallenge() *oidc.CodeChallenge {
	if a.CodeChallenge == "" {
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
// yazar. id parametresi bos gecilirse yeni bir UUID uretilir (op
// kutuphanesi bazi cagrilarda hazir id vermez).
func (d *IstekDepo) Olustur(ctx context.Context, req *oidc.AuthRequest, id string) (*AuthIstek, error) {
	if id == "" {
		id = uuid.NewString()
	}
	istek := &AuthIstek{
		ID:                  id,
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
func (d *IstekDepo) TamamlandiIsaretle(ctx context.Context, id string, userID string) error {
	istek, err := d.IDileOku(ctx, id)
	if err != nil {
		return err
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
