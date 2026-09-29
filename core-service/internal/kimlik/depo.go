package kimlik

import (
	"context"
	"errors"
	"fmt"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"

	"github.com/makeasinger/api/internal/config"
)

// Depo, op.Storage arayuzunun tamamini karsilar. Kendisi hicbir depolama
// mantigi tasimaz; UserStore, TokenStore, IstekDepo ve Anahtar'a delege
// eder. Bu dosyanin isi mekaniktir: kutuphanenin bekledigi imzalari bizim
// Turkce depolarimiza baglamak.
type Depo struct {
	cfg       *config.AuthConfig
	kullanici UserStore
	jetonlar  TokenStore
	istekler  *IstekDepo
	anahtar   *Anahtar
	istemci   *MobilIstemci

	havuz *pgxpool.Pool // Health icin; nil olabilir
	rdb   *redis.Client // Health icin; nil olabilir
}

// NewDepo, testlerin ve uretimin ortak kurulum noktasidir. Postgres/Redis
// baglantilarina Health icin ihtiyac varsa SaglikBagla ile sonradan
// eklenir; boylece testler canli baglanti olmadan Depo kurabilir.
func NewDepo(cfg *config.AuthConfig, kullanici UserStore, jetonlar TokenStore, istekler *IstekDepo, anahtar *Anahtar) *Depo {
	return &Depo{
		cfg:       cfg,
		kullanici: kullanici,
		jetonlar:  jetonlar,
		istekler:  istekler,
		anahtar:   anahtar,
		istemci:   NewMobilIstemci(cfg),
	}
}

// SaglikBagla, Health'in kullanacagi Postgres havuzunu ve Redis
// istemcisini baglar. cagrilmazsa Health ilgili bacagi atlar (bkz. Health).
func (d *Depo) SaglikBagla(havuz *pgxpool.Pool, rdb *redis.Client) {
	d.havuz = havuz
	d.rdb = rdb
}

// Derleme zamaninda Depo'nun op.Storage'in TAMAMINI karsiladigini
// dogrula. Bir metot eksik veya imzasi yanlissa derleme burada kirilir.
var _ op.Storage = (*Depo)(nil)

// ---- AuthStorage ----

// CreateAuthRequest, gelen authorization request'i IstekDepo'ya devreder.
// Ucuncu parametre (userID) genelde bostur: kullanici bu asamada henuz
// giris yapmamistir; TamamlandiIsaretle giris tamamlaninca doldurur.
func (d *Depo) CreateAuthRequest(ctx context.Context, authReq *oidc.AuthRequest, userID string) (op.AuthRequest, error) {
	istek, err := d.istekler.Olustur(ctx, authReq, userID)
	if err != nil {
		return nil, err
	}
	return istek, nil
}

func (d *Depo) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	return d.istekler.IDileOku(ctx, id)
}

func (d *Depo) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	return d.istekler.KodlaOku(ctx, code)
}

func (d *Depo) SaveAuthCode(ctx context.Context, id string, code string) error {
	return d.istekler.KodKaydet(ctx, id, code)
}

func (d *Depo) DeleteAuthRequest(ctx context.Context, id string) error {
	return d.istekler.Sil(ctx, id)
}

// clientIDGetter, hem AuthIstek hem refreshIstek'in ortak paydasidir:
// CreateAccessToken/CreateAccessAndRefreshTokens'a gelen TokenRequest'in
// somut turunu bilmeden client ID'sini cikarmak icin kullanilir.
type clientIDGetter interface {
	GetClientID() string
}

// amrGetter / authTimeGetter: AuthIstek ve refreshIstek bu bilgiyi tasir,
// ama op.TokenRequest arayuzu bunlari zorunlu kilmaz (ornegin gelecekte
// baska bir TokenRequest turu eklenirse bu bilgiler olmayabilir).
type amrGetter interface{ GetAMR() []string }
type authTimeGetter interface{ GetAuthTime() time.Time }

// CreateAccessToken, offline_access istenmeyen authorization code
// akisinda (ve implicit/JWT profile gibi bizim desteklemedigimiz
// akislarda) cagrilir. Access kaydi TokenStore'a yazilir; JWT access
// token bu ID'yi jti olarak tasir.
func (d *Depo) CreateAccessToken(ctx context.Context, request op.TokenRequest) (string, time.Time, error) {
	getter, uygun := request.(clientIDGetter)
	if !uygun {
		return "", time.Time{}, fmt.Errorf("token istegi turu desteklenmiyor: %T", request)
	}
	id := uuid.NewString()
	expiresAt := time.Now().UTC().Add(d.cfg.AccessTTL)
	if err := d.jetonlar.AccessKaydet(ctx, id, request.GetSubject(), getter.GetClientID(), request.GetScopes(), expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return id, expiresAt, nil
}

// CreateAccessAndRefreshTokens, hem authorization code akisinda
// (offline_access istendiginde, currentRefreshToken bos) hem refresh_token
// akisinda (currentRefreshToken dolu) cagrilir. Rotasyon TokenStore'a
// devredilir: currentRefreshToken bos ise yeni bir aile baslar, doluysa
// RefreshDondur mevcut aileyi surdurur.
func (d *Depo) CreateAccessAndRefreshTokens(ctx context.Context, request op.TokenRequest, currentRefreshToken string) (accessTokenID string, newRefreshToken string, expiration time.Time, err error) {
	getter, uygun := request.(clientIDGetter)
	if !uygun {
		return "", "", time.Time{}, fmt.Errorf("token istegi turu desteklenmiyor: %T", request)
	}
	clientID := getter.GetClientID()
	userID := request.GetSubject()
	scopes := request.GetScopes()
	audience := request.GetAudience()

	var amr []string
	if g, uygun := request.(amrGetter); uygun {
		amr = g.GetAMR()
	}
	var authTime time.Time
	if g, uygun := request.(authTimeGetter); uygun {
		authTime = g.GetAuthTime()
	}

	accessID := uuid.NewString()
	accessExpiresAt := time.Now().UTC().Add(d.cfg.AccessTTL)
	if err := d.jetonlar.AccessKaydet(ctx, accessID, userID, clientID, scopes, accessExpiresAt); err != nil {
		return "", "", time.Time{}, err
	}

	yeni := &RefreshKayit{
		UserID:    userID,
		ClientID:  clientID,
		Scopes:    scopes,
		Audience:  audience,
		AMR:       amr,
		AuthTime:  authTime,
		ExpiresAt: time.Now().UTC().Add(d.cfg.RefreshTTL),
	}

	var refreshToken string
	if currentRefreshToken == "" {
		// Authorization code akisi: sunulan jeton yok, yeni bir aile baslar.
		refreshToken, err = d.jetonlar.RefreshOlustur(ctx, yeni)
	} else {
		refreshToken, err = d.jetonlar.RefreshDondur(ctx, currentRefreshToken, yeni)
		if errors.Is(err, ErrJetonTekrar) || errors.Is(err, ErrJetonYok) {
			err = op.ErrInvalidRefreshToken
		}
	}
	if err != nil {
		return "", "", time.Time{}, err
	}
	return accessID, refreshToken, accessExpiresAt, nil
}

// refreshIstek, op.RefreshTokenRequest arayuzunu karsilayan, RefreshKayit
// uzerine ince bir sarmalayicidir. SetCurrentScopes, kutuphanenin refresh
// istegindeki scope kisitlamasini (istenen scope orijinalin alt kumesiyse)
// uygulayabilmesi icin yazilabilir olmak zorunda.
type refreshIstek struct {
	userID   string
	clientID string
	scopes   []string
	audience []string
	amr      []string
	authTime time.Time
}

var _ op.RefreshTokenRequest = (*refreshIstek)(nil)

func (r *refreshIstek) GetAMR() []string                 { return r.amr }
func (r *refreshIstek) GetAudience() []string            { return r.audience }
func (r *refreshIstek) GetAuthTime() time.Time           { return r.authTime }
func (r *refreshIstek) GetClientID() string              { return r.clientID }
func (r *refreshIstek) GetScopes() []string              { return r.scopes }
func (r *refreshIstek) GetSubject() string               { return r.userID }
func (r *refreshIstek) SetCurrentScopes(scopes []string) { r.scopes = scopes }

// TokenRequestByRefreshToken, sunulan refresh jetonunu SADECE dogrular ve
// okur (RefreshOku); rotasyon burada YAPILMAZ. Kutuphane bu sonucu
// dogruladiktan sonra CreateAccessAndRefreshTokens'i ayni jetonla tekrar
// cagirir, rotasyon orada gerceklesir.
func (d *Depo) TokenRequestByRefreshToken(ctx context.Context, refreshToken string) (op.RefreshTokenRequest, error) {
	kayit, err := d.jetonlar.RefreshOku(ctx, refreshToken)
	if err != nil {
		if errors.Is(err, ErrJetonYok) || errors.Is(err, ErrJetonTekrar) {
			return nil, op.ErrInvalidRefreshToken
		}
		return nil, err
	}
	return &refreshIstek{
		userID:   kayit.UserID,
		clientID: kayit.ClientID,
		scopes:   kayit.Scopes,
		audience: kayit.Audience,
		amr:      kayit.AMR,
		authTime: kayit.AuthTime,
	}, nil
}

// TerminateSession, cikis akisinda kullanicinin o istemcideki tum refresh
// jetonlarini iptal eder. JWT access token'lar dogal sureleriyle biter.
func (d *Depo) TerminateSession(ctx context.Context, userID string, clientID string) error {
	return d.jetonlar.KullaniciIptal(ctx, userID, clientID)
}

// RevokeToken, RFC 7009 /revoke uc noktasindan cagrilir. Refresh token
// icin tum aile iptal edilir (tek jetonu degil): aksi halde ayni aileden
// baska bir jeton hala gecerli kalir. Access token icin sadece TokenStore
// kaydi silinir; JWT'nin kendisi imza gecerliyse gecerliligini korur,
// ama introspection/userinfo artik onu bulamaz.
func (d *Depo) RevokeToken(ctx context.Context, tokenOrTokenID string, userID string, clientID string) *oidc.Error {
	// Once refresh token olarak dene: RefreshOku basariliysa aileyi iptal et.
	if kayit, err := d.jetonlar.RefreshOku(ctx, tokenOrTokenID); err == nil {
		if kayit.ClientID != clientID {
			hata := oidc.ErrInvalidClient().WithDescription("jeton bu istemci icin verilmemis")
			return hata
		}
		if err := d.jetonlar.AileIptal(ctx, kayit.FamilyID); err != nil {
			hata := oidc.ErrServerError().WithParent(err)
			return hata
		}
		return nil
	}
	// Refresh degilse access tokenID olarak dene.
	if kayit, err := d.jetonlar.AccessOku(ctx, tokenOrTokenID); err == nil {
		if kayit.ClientID != clientID {
			hata := oidc.ErrInvalidClient().WithDescription("jeton bu istemci icin verilmemis")
			return hata
		}
		if err := d.jetonlar.AccessSil(ctx, tokenOrTokenID); err != nil {
			hata := oidc.ErrServerError().WithParent(err)
			return hata
		}
		return nil
	}
	// Ne refresh ne access: kutuphanenin beklentisi budur -- jeton zaten
	// gecersizse "basarili" sayilir (RFC 7009 idempotent iptal).
	return nil
}

// GetRefreshTokenInfo, sunulan degerin gercekten bir refresh token olup
// olmadigini kontrol eder; degilse (veya tuketilmis/iptal edilmisse)
// op.ErrInvalidRefreshToken donmek ZORUNLUDUR (kutuphane sozlesmesi).
func (d *Depo) GetRefreshTokenInfo(ctx context.Context, clientID string, token string) (userID string, tokenID string, err error) {
	kayit, err := d.jetonlar.RefreshOku(ctx, token)
	if err != nil {
		return "", "", op.ErrInvalidRefreshToken
	}
	return kayit.UserID, kayit.ID, nil
}

func (d *Depo) SigningKey(ctx context.Context) (op.SigningKey, error) {
	return d.anahtar, nil
}

func (d *Depo) SignatureAlgorithms(ctx context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{d.anahtar.SignatureAlgorithm()}, nil
}

// KeySet, JWKS yayini icin acik anahtari doner. d.anahtar dogrudan
// donulemez: *Anahtar op.Key'i KASITLI OLARAK karsilamaz (Algorithm/Use
// metotlari yok), boylece ozel anahtarin yanlislikla JWKS'e sizmasi
// derleme zamaninda engellenir.
func (d *Depo) KeySet(ctx context.Context) ([]op.Key, error) {
	return []op.Key{d.anahtar.AcikAnahtar()}, nil
}

// ---- OPStorage ----

// GetClientByClientID, tek yapilandirilmis istemcimiz disinda HER SEYI
// reddeder: makesinger'in ikinci bir istemcisi yok, olsa da burada
// tanimlanmadan kabul edilmemeli.
func (d *Depo) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	if clientID != d.cfg.ClientID {
		return nil, fmt.Errorf("istemci bulunamadi: %s", clientID)
	}
	return d.istemci, nil
}

// AuthorizeClientIDSecret HER ZAMAN hata doner: tek istemcimiz public
// (native, AuthMethodNone) oldugu icin secret tasimaz. Basarili donerse
// bos secret ile istemci taklit edilebilir.
func (d *Depo) AuthorizeClientIDSecret(ctx context.Context, clientID, clientSecret string) error {
	return fmt.Errorf("istemci %q secret tasimaz (public client)", clientID)
}

// SetUserinfoFromScopes kutuphane tarafindan deprecated ilan edildi;
// bos implementasyon beklenen davranis (SetUserinfoFromRequest onun
// yerini alir, ama op.Storage'in zorunlu kildigi minimum arayuz bu).
func (d *Depo) SetUserinfoFromScopes(ctx context.Context, userinfo *oidc.UserInfo, userID, clientID string, scopes []string) error {
	return nil
}

// SetUserinfoFromToken, /userinfo uc noktasi tarafindan cagrilir: access
// token kaydini bulur ve claim'leri scope'a gore doldurur.
func (d *Depo) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, subject, origin string) error {
	kayit, err := d.jetonlar.AccessOku(ctx, tokenID)
	if err != nil {
		return err
	}
	return d.userinfoDoldur(ctx, userinfo, kayit.UserID, kayit.Scopes)
}

// SetIntrospectionFromToken, /introspect uc noktasi tarafindan cagrilir.
func (d *Depo) SetIntrospectionFromToken(ctx context.Context, introspection *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	kayit, err := d.jetonlar.AccessOku(ctx, tokenID)
	if err != nil {
		return err
	}
	if kayit.ClientID != clientID {
		return fmt.Errorf("jeton bu istemci icin verilmemis")
	}
	introspection.Scope = kayit.Scopes
	introspection.ClientID = kayit.ClientID

	userinfo := new(oidc.UserInfo)
	if err := d.userinfoDoldur(ctx, userinfo, kayit.UserID, kayit.Scopes); err != nil {
		return err
	}
	introspection.SetUserInfo(userinfo)
	return nil
}

// userinfoDoldur, kullanici bilgisini scope'a gore claim'lere yazar.
// profile scope'u ad, email scope'u eposta ve dogrulanma durumunu acar;
// baska scope tanimli degil (makesinger'in tek kimlik dogrulama yontemi
// eposta+sifre, ek profil verisi yok).
func (d *Depo) userinfoDoldur(ctx context.Context, userinfo *oidc.UserInfo, userID string, scopes []string) error {
	kullanici, err := d.kullanici.ByID(ctx, userID)
	if err != nil {
		return err
	}
	userinfo.Subject = kullanici.ID
	for _, scope := range scopes {
		switch scope {
		case oidc.ScopeProfile:
			userinfo.Name = kullanici.Name
		case oidc.ScopeEmail:
			userinfo.Email = kullanici.Email
			userinfo.EmailVerified = oidc.Bool(kullanici.EmailVerified)
		}
	}
	return nil
}

// GetPrivateClaimsFromScopes, JWT access token'a ozel claim eklemek
// icindir. Makesinger'in ozel scope'u yok; standart scope'lar id_token ve
// userinfo uzerinden zaten tasiniyor.
func (d *Depo) GetPrivateClaimsFromScopes(ctx context.Context, userID, clientID string, scopes []string) (map[string]any, error) {
	return nil, nil
}

// GetKeyByIDAndClientID, JWT profile / private_key_jwt istemci
// dogrulamasi icindir. Desteklenmiyor: tek istemcimiz public ve secret'siz.
func (d *Depo) GetKeyByIDAndClientID(ctx context.Context, keyID, clientID string) (*jose.JSONWebKey, error) {
	return nil, fmt.Errorf("private_key_jwt destegi yok")
}

// ValidateJWTProfileScopes, JWT Profile Authorization Grant (RFC 7523)
// icindir. Desteklenmiyor: makesinger yalnizca authorization code + PKCE
// ve refresh_token kullanir.
func (d *Depo) ValidateJWTProfileScopes(ctx context.Context, userID string, scopes []string) ([]string, error) {
	return nil, fmt.Errorf("JWT profile grant desteklenmiyor")
}

// Health, ikincil dinleyicinin /healthz'i tarafindan cagrilir. havuz veya
// rdb SaglikBagla ile baglanmadiysa (orn. testlerde) o bacak atlanir;
// boylece Depo agsiz test edilebilir ama uretimde her iki bagimlilik da
// gercekten yoklanir.
func (d *Depo) Health(ctx context.Context) error {
	if d.havuz != nil {
		if err := d.havuz.Ping(ctx); err != nil {
			return fmt.Errorf("postgres saglik kontrolu basarisiz: %w", err)
		}
	}
	if d.rdb != nil {
		if err := d.rdb.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis saglik kontrolu basarisiz: %w", err)
		}
	}
	return nil
}
