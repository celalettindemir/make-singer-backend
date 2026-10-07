package kimlik

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrKimlikYok        = errors.New("federe kimlik bulunamadi")
	ErrKimlikKullanimda = errors.New("federe kimlik baska hesaba bagli")
)

// FedereDepo, (saglayici, sub) ile kullanici arasindaki eslesmeyi
// yonetir. Faz 2'nin federasyon akislari (Apple/Google) bu depoyu
// kullanarak bizim User'imizi bulur veya olusturur.
type FedereDepo interface {
	// KimlikleKullanici, (saglayici, sub) ciftine bagli User'i doner.
	// Eslesme yoksa ErrKimlikYok doner, nil User degil.
	KimlikleKullanici(ctx context.Context, saglayici, sub string) (*User, error)

	// Bagla, VAR OLAN bir kullaniciya yeni bir federe kimlik baglar.
	// (saglayici, sub) baska bir hesaba zaten bagliysa veya bu kullanici
	// bu saglayiciya zaten bagliysa ErrKimlikKullanimda doner.
	Bagla(ctx context.Context, saglayici, sub, userID string) error

	// FedereKullaniciOlustur, sifresi OLMAYAN yeni bir kullanici olusturur
	// ve onu ayni islemde (saglayici, sub) ile baglar.
	FedereKullaniciOlustur(ctx context.Context, saglayici, sub, email, ad string, epostaDogrulandi bool) (*User, error)

	// SaglayicilarByUserID, bir kullanicinin bagli oldugu tum saglayici
	// adlarini doner (ornek: K1'in 409 sayfasinin metnini beslemek icin).
	SaglayicilarByUserID(ctx context.Context, userID string) ([]string, error)
}

type PostgresFedereDepo struct {
	havuz *pgxpool.Pool
}

func NewPostgresFedereDepo(havuz *pgxpool.Pool) *PostgresFedereDepo {
	return &PostgresFedereDepo{havuz: havuz}
}

func (d *PostgresFedereDepo) KimlikleKullanici(ctx context.Context, saglayici, sub string) (*User, error) {
	var k User
	err := d.havuz.QueryRow(ctx,
		`SELECT u.id, u.email, u.email_verified, u.name, coalesce(u.password_hash, ''), u.created_at
		 FROM federe_kimlikler f
		 JOIN users u ON u.id = f.user_id
		 WHERE f.saglayici = $1 AND f.sub = $2`,
		saglayici, sub,
	).Scan(&k.ID, &k.Email, &k.EmailVerified, &k.Name, &k.PasswordHash, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKimlikYok
	}
	if err != nil {
		return nil, fmt.Errorf("federe kimlik okunamadi: %w", err)
	}
	return &k, nil
}

func (d *PostgresFedereDepo) Bagla(ctx context.Context, saglayici, sub, userID string) error {
	_, err := d.havuz.Exec(ctx,
		`INSERT INTO federe_kimlikler (saglayici, sub, user_id) VALUES ($1, $2, $3)`,
		saglayici, sub, userID)
	if err != nil {
		// 23505 = unique_violation. Hem (saglayici, sub) birincil
		// anahtari, hem de (user_id, saglayici) tekilligi bu kodu
		// uretir; her iki durumda da anlam aynidir: bu kimlik baska bir
		// esleseme ile cakisiyor.
		var pgHata *pgconn.PgError
		if errors.As(err, &pgHata) && pgHata.Code == "23505" {
			return ErrKimlikKullanimda
		}
		return fmt.Errorf("federe kimlik baglanamadi: %w", err)
	}
	return nil
}

// FedereKullaniciOlustur, sifresi OLMAYAN bir kullanici ile federe
// kimligini TEK ISLEMDE yazar. Islem zorunlu: iki ayri Exec arasinda
// hata olursa ortada sifresi de federe kimligi de olmayan, yani HIC
// GIRILEMEYEN bir hesap kalirdi -- ve e-posta tekil oldugu icin
// kullanici bir daha kaydolamazdi.
func (d *PostgresFedereDepo) FedereKullaniciOlustur(
	ctx context.Context, saglayici, sub, email, ad string, epostaDogrulandi bool,
) (*User, error) {
	islem, err := d.havuz.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("islem acilamadi: %w", err)
	}
	defer func() { _ = islem.Rollback(ctx) }()

	k := &User{
		ID:            uuid.NewString(),
		Email:         epostaNormalize(email),
		EmailVerified: epostaDogrulandi,
		Name:          strings.TrimSpace(ad),
		CreatedAt:     time.Now().UTC(),
	}
	// password_hash ACIKCA NULL: SifreDogru bos hash'te her zaman false
	// doner, yani bu hesaba sifreyle girilemez.
	if _, err := islem.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at)
		 VALUES ($1, $2, $3, $4, NULL, $5)`,
		k.ID, k.Email, k.EmailVerified, k.Name, k.CreatedAt); err != nil {
		var pgHata *pgconn.PgError
		if errors.As(err, &pgHata) && pgHata.Code == "23505" {
			return nil, ErrEpostaKullanimda
		}
		return nil, fmt.Errorf("federe kullanici olusturulamadi: %w", err)
	}
	if _, err := islem.Exec(ctx,
		`INSERT INTO federe_kimlikler (saglayici, sub, user_id) VALUES ($1, $2, $3)`,
		saglayici, sub, k.ID); err != nil {
		var pgHata *pgconn.PgError
		if errors.As(err, &pgHata) && pgHata.Code == "23505" {
			return nil, ErrKimlikKullanimda
		}
		return nil, fmt.Errorf("federe kimlik yazilamadi: %w", err)
	}
	if err := islem.Commit(ctx); err != nil {
		return nil, fmt.Errorf("islem tamamlanamadi: %w", err)
	}
	return k, nil
}

func (d *PostgresFedereDepo) SaglayicilarByUserID(ctx context.Context, userID string) ([]string, error) {
	satirlar, err := d.havuz.Query(ctx,
		`SELECT saglayici FROM federe_kimlikler WHERE user_id = $1 ORDER BY saglayici`, userID)
	if err != nil {
		return nil, fmt.Errorf("saglayicilar okunamadi: %w", err)
	}
	defer satirlar.Close()

	var sag []string
	for satirlar.Next() {
		var s string
		if err := satirlar.Scan(&s); err != nil {
			return nil, fmt.Errorf("saglayicilar okunamadi: %w", err)
		}
		sag = append(sag, s)
	}
	if err := satirlar.Err(); err != nil {
		return nil, fmt.Errorf("saglayicilar okunamadi: %w", err)
	}
	return sag, nil
}

// Iki uygulama da arayuzden sapmasin: biri degisirse derleme kirilir.
var (
	_ FedereDepo = (*PostgresFedereDepo)(nil)
	_ FedereDepo = (*SahteFedereDepo)(nil)
)
