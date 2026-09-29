package kimlik

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	ErrJetonYok    = errors.New("jeton bulunamadi")
	ErrJetonTekrar = errors.New("jeton yeniden kullanildi")
)

type AccessKayit struct {
	ID        string
	UserID    string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
}

type RefreshKayit struct {
	ID        string
	FamilyID  string
	UserID    string
	ClientID  string
	TokenHash []byte
	Scopes    []string
	Audience  []string
	AMR       []string
	AuthTime  time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

type TokenStore interface {
	AccessKaydet(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error
	AccessOku(ctx context.Context, id string) (*AccessKayit, error)
	// AccessSil, tek bir access token kaydini siler. op.Storage.RevokeToken
	// bir access token'i hedef aldiginda cagirir: jeton kendisi (JWT
	// oldugu icin) hala imza gecerliyse dogrulanir, ama introspection ve
	// /userinfo artik onu bulamaz.
	AccessSil(ctx context.Context, id string) error
	RefreshOlustur(ctx context.Context, k *RefreshKayit) (string, error)
	RefreshDondur(ctx context.Context, sunulan string, yeni *RefreshKayit) (string, error)
	RefreshOku(ctx context.Context, sunulan string) (*RefreshKayit, error)
	AileIptal(ctx context.Context, familyID string) error
	KullaniciIptal(ctx context.Context, userID, clientID string) error
}

// jetonUret, 32 baytlik kriptografik rastgele jeton uretir ve URL'de
// tasinabilir bicimde dondurur.
func jetonUret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("jeton uretilemedi: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// jetonOzet, saklanacak degeri uretir. Jetonun kendisi HICBIR ZAMAN
// saklanmaz: veritabani sizarsa jetonlar kullanilamaz olmali.
func jetonOzet(jeton string) []byte {
	o := sha256.Sum256([]byte(jeton))
	return o[:]
}

// accessAnahtar, access token kaydinin Redis anahtaridir. Access
// kayitlari kisa omurludur ve TTL ile kendiliginden silinir; bu yuzden
// Postgres'te degil Redis'te tutulurlar.
func accessAnahtar(id string) string { return "kimlik:at:" + id }

// PostgresTokenStore: refresh kayitlari Postgres'te (kalici, aile
// iliskisi ve iptal gecmisi gerektigi icin), access kayitlari Redis'te
// (kisa omurlu, TTL ile silinir).
type PostgresTokenStore struct {
	havuz *pgxpool.Pool
	rdb   *redis.Client
}

func NewPostgresTokenStore(havuz *pgxpool.Pool, rdb *redis.Client) *PostgresTokenStore {
	return &PostgresTokenStore{havuz: havuz, rdb: rdb}
}

func (s *PostgresTokenStore) AccessKaydet(ctx context.Context, id, userID, clientID string, scopes []string, expiresAt time.Time) error {
	kayit := AccessKayit{
		ID:        id,
		UserID:    userID,
		ClientID:  clientID,
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	veri, err := json.Marshal(kayit)
	if err != nil {
		return fmt.Errorf("access kaydi serilenemedi: %w", err)
	}
	// TTL = access omru: kayit kendiliginden silinir, Redis'e kalici veri
	// yazilmaz. Okuma tarafi yine de ExpiresAt'i kontrol eder (saat kaymasi
	// ve TTL'in henuz isletilmemis olmasi ihtimaline karsi savunma).
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil
	}
	if err := s.rdb.Set(ctx, accessAnahtar(id), veri, ttl).Err(); err != nil {
		return fmt.Errorf("access kaydi yazilamadi: %w", err)
	}
	return nil
}

func (s *PostgresTokenStore) AccessOku(ctx context.Context, id string) (*AccessKayit, error) {
	veri, err := s.rdb.Get(ctx, accessAnahtar(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrJetonYok
	}
	if err != nil {
		return nil, fmt.Errorf("access kaydi okunamadi: %w", err)
	}
	var kayit AccessKayit
	if err := json.Unmarshal(veri, &kayit); err != nil {
		return nil, fmt.Errorf("access kaydi cozumlenemedi: %w", err)
	}
	if time.Now().UTC().After(kayit.ExpiresAt) {
		return nil, ErrJetonYok
	}
	return &kayit, nil
}

// AccessSil, access kaydini Redis'ten kaldirir. Kayit zaten yoksa
// (suresi dolmus veya hic yazilmamis) sessizce basarili sayilir.
func (s *PostgresTokenStore) AccessSil(ctx context.Context, id string) error {
	if err := s.rdb.Del(ctx, accessAnahtar(id)).Err(); err != nil {
		return fmt.Errorf("access kaydi silinemedi: %w", err)
	}
	return nil
}

// RefreshOlustur yeni bir AILE baslatir: family_id yeni uretilir. Rotasyon
// bu aileyi surdurur, yeniden kullanim tespiti ise aileyi toptan iptal eder.
func (s *PostgresTokenStore) RefreshOlustur(ctx context.Context, k *RefreshKayit) (string, error) {
	jeton, err := jetonUret()
	if err != nil {
		return "", err
	}
	if err := s.refreshEkle(ctx, s.havuz, k, uuid.NewString(), jeton); err != nil {
		return "", err
	}
	return jeton, nil
}

// calistirici, hem havuzun hem de islemin karsiladigi asgari arayuz:
// refreshEkle ayni SQL'i islem icinde de islem disinda da kullanabilsin.
type calistirici interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (s *PostgresTokenStore) refreshEkle(ctx context.Context, c calistirici, k *RefreshKayit, familyID, jeton string) error {
	_, err := c.Exec(ctx,
		`INSERT INTO refresh_tokens
		   (id, family_id, user_id, client_id, token_hash, scopes, audience, amr, auth_time, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uuid.NewString(), familyID, k.UserID, k.ClientID, jetonOzet(jeton),
		k.Scopes, k.Audience, k.AMR, k.AuthTime, k.ExpiresAt)
	if err != nil {
		return fmt.Errorf("refresh jetonu yazilamadi: %w", err)
	}
	return nil
}

// RefreshDondur, sunulan jetonu tuketip ayni aileye yeni bir jeton ekler.
// Tek islemde kosar ve satiri kilitler.
func (s *PostgresTokenStore) RefreshDondur(ctx context.Context, sunulan string, yeni *RefreshKayit) (string, error) {
	islem, err := s.havuz.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("islem baslatilamadi: %w", err)
	}
	defer func() { _ = islem.Rollback(ctx) }()

	var (
		id, familyID     string
		userID, clientID string
		expiresAt        time.Time
		usedAt           *time.Time
		revokedAt        *time.Time
	)
	// FOR UPDATE: ayni jetonla gelen iki istek yarisirsa ikincisi
	// birincinin used_at yazmasini gormeli, yoksa yeniden kullanim
	// tespiti sessizce kacar.
	err = islem.QueryRow(ctx,
		`SELECT id, family_id, user_id, client_id, expires_at, used_at, revoked_at
		   FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
		jetonOzet(sunulan)).Scan(&id, &familyID, &userID, &clientID, &expiresAt, &usedAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrJetonYok
	}
	if err != nil {
		return "", fmt.Errorf("jeton okunamadi: %w", err)
	}
	// Sira onemli: iptal edilmis jeton zaten olu oldugu icin (aile bir
	// kez iptal edildikten sonra) yeniden kullanim alarmi tekrar
	// calmasin; yeniden kullanim kontrolu ondan sonra gelir.
	if revokedAt != nil {
		return "", ErrJetonYok
	}
	if usedAt != nil {
		// Yeniden kullanim: ailenin tamamini oldur ve ayri bir hata don.
		if _, err := islem.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = now()
			  WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
			return "", fmt.Errorf("aile iptal edilemedi: %w", err)
		}
		if err := islem.Commit(ctx); err != nil {
			// Commit hatasi yeniden kullanim bilgisini YUTMAMALI: cagiran
			// errors.Is ile bunun bir hirsizlik alarmi oldugunu gormeli.
			return "", fmt.Errorf("%w (iptal commit edilemedi: %v)", ErrJetonTekrar, err)
		}
		return "", ErrJetonTekrar
	}
	// Sahiplik: cagiran katmanda bir hata olursa ayni aileye baska bir
	// kullanicinin (veya baska istemcinin) kaydi eklenmesin. Ucuz savunma,
	// uyusmazlikta jeton hic yokmus gibi davranilir.
	if userID != yeni.UserID || clientID != yeni.ClientID {
		return "", ErrJetonYok
	}
	if time.Now().UTC().After(expiresAt) {
		return "", ErrJetonYok
	}

	jeton, err := jetonUret()
	if err != nil {
		return "", err
	}
	if _, err := islem.Exec(ctx,
		`UPDATE refresh_tokens SET used_at = now() WHERE id = $1`, id); err != nil {
		return "", fmt.Errorf("jeton tuketilemedi: %w", err)
	}
	// Yeni kayit AYNI family_id ile eklenir: zincirin izi korunur ki
	// sonradan bir yeniden kullanim gorulurse tum zincir iptal edilebilsin.
	if err := s.refreshEkle(ctx, islem, yeni, familyID, jeton); err != nil {
		return "", err
	}
	if err := islem.Commit(ctx); err != nil {
		return "", fmt.Errorf("rotasyon commit edilemedi: %w", err)
	}
	return jeton, nil
}

// RefreshOku, sunulan jetonu dogrular. Tuketilmis (used_at), iptal
// edilmis (revoked_at) ve suresi gecmis jetonlarin hepsi ErrJetonYok'tur:
// cagirana hangisi oldugu sizdirilmaz.
func (s *PostgresTokenStore) RefreshOku(ctx context.Context, sunulan string) (*RefreshKayit, error) {
	var k RefreshKayit
	err := s.havuz.QueryRow(ctx,
		`SELECT id, family_id, user_id, client_id, token_hash, scopes, audience, amr,
		        auth_time, expires_at, used_at, revoked_at
		   FROM refresh_tokens WHERE token_hash = $1`,
		jetonOzet(sunulan)).Scan(
		&k.ID, &k.FamilyID, &k.UserID, &k.ClientID, &k.TokenHash, &k.Scopes, &k.Audience,
		&k.AMR, &k.AuthTime, &k.ExpiresAt, &k.UsedAt, &k.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrJetonYok
	}
	if err != nil {
		return nil, fmt.Errorf("jeton okunamadi: %w", err)
	}
	if k.RevokedAt != nil || k.UsedAt != nil || time.Now().UTC().After(k.ExpiresAt) {
		return nil, ErrJetonYok
	}
	return &k, nil
}

// AileIptal, bir ailenin tum yasayan jetonlarini iptal eder.
func (s *PostgresTokenStore) AileIptal(ctx context.Context, familyID string) error {
	if _, err := s.havuz.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		  WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
		return fmt.Errorf("aile iptal edilemedi: %w", err)
	}
	return nil
}

// KullaniciIptal, bir kullanicinin o istemcideki tum refresh jetonlarini
// iptal eder. Cikis bunu kullanir; baska kullanicinin jetonuna dokunmaz.
func (s *PostgresTokenStore) KullaniciIptal(ctx context.Context, userID, clientID string) error {
	if _, err := s.havuz.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		  WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL`,
		userID, clientID); err != nil {
		return fmt.Errorf("kullanici jetonlari iptal edilemedi: %w", err)
	}
	return nil
}

// Iki uygulama da arayuzden sapmasin: biri degisirse derleme kirilir.
var (
	_ TokenStore = (*PostgresTokenStore)(nil)
	_ TokenStore = (*SahteTokenStore)(nil)
)
