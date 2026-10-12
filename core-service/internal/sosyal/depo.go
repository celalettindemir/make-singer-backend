package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProfilDeposu, sosyal.profil uzerindeki islemler.
type ProfilDeposu interface {
	// ProfilGetir, kullanicinin profilini doner; yoksa ErrProfilYok.
	ProfilGetir(ctx context.Context, kullaniciID string) (*ProfilKaydi, error)
	// ProfilAdlaGetir, kullanici adiyla profil doner; yoksa ErrBulunamadi.
	ProfilAdlaGetir(ctx context.Context, ad string) (*ProfilKaydi, error)
	// ProfilKaydet, profili olusturur ya da gunceller. Ad baskasindaysa
	// ErrKullaniciAdiAlinmis.
	ProfilKaydet(ctx context.Context, k ProfilKaydi) (*ProfilKaydi, error)
	// AdSahipleri, verilen adlardan alinmis olanlari ad -> kullanici id
	// eslemesi olarak doner.
	AdSahipleri(ctx context.Context, adlar []string) (map[string]string, error)
	// ProfilSayilari, sahibin sayaclarini izleyiciye gore doner.
	ProfilSayilari(ctx context.Context, sahipID, izleyiciID string) (*ProfilSayilari, error)
}

// Depo, sosyal modulunun tum kalici islemleridir.
type Depo interface {
	ProfilDeposu
	YayinDeposu
	HazirlikDeposu
	AkisDeposu
	BegeniDeposu
	TakipDeposu
	YorumDeposu
	SikayetDeposu
}

// PostgresDepo, Depo'nun Postgres uygulamasidir. Kimlikler cagri
// noktasinda uuid olarak dogrulanmis gelir (servis kimlikCoz kullanir).
type PostgresDepo struct {
	havuz *pgxpool.Pool
}

func NewPostgresDepo(havuz *pgxpool.Pool) *PostgresDepo {
	return &PostgresDepo{havuz: havuz}
}

var _ Depo = (*PostgresDepo)(nil)

// tekilIhlali, err bir UNIQUE ihlaliyse kisitin adini doner.
func tekilIhlali(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

const profilKolonlari = `kullanici_id::text, kullanici_adi, gorunen_ad, bio, olusturma`

// profilKolonlariP, profilKolonlari'nin "p" takma adli bicimidir.
const profilKolonlariP = `p.kullanici_id::text, p.kullanici_adi, p.gorunen_ad, p.bio, p.olusturma`

func profilTara(satir pgx.Row) (*ProfilKaydi, error) {
	var k ProfilKaydi
	if err := satir.Scan(&k.KullaniciID, &k.KullaniciAdi, &k.GorunenAd, &k.Bio, &k.Olusturma); err != nil {
		return nil, err
	}
	return &k, nil
}

func (d *PostgresDepo) ProfilGetir(ctx context.Context, kullaniciID string) (*ProfilKaydi, error) {
	k, err := profilTara(d.havuz.QueryRow(ctx,
		`SELECT `+profilKolonlari+` FROM sosyal.profil WHERE kullanici_id = $1`, kullaniciID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProfilYok
	}
	if err != nil {
		return nil, fmt.Errorf("profil okunamadi: %w", err)
	}
	return k, nil
}

func (d *PostgresDepo) ProfilAdlaGetir(ctx context.Context, ad string) (*ProfilKaydi, error) {
	k, err := profilTara(d.havuz.QueryRow(ctx,
		`SELECT `+profilKolonlari+` FROM sosyal.profil WHERE kullanici_adi = $1`, ad))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("profil okunamadi: %w", err)
	}
	return k, nil
}

func (d *PostgresDepo) ProfilKaydet(ctx context.Context, k ProfilKaydi) (*ProfilKaydi, error) {
	kayit, err := profilTara(d.havuz.QueryRow(ctx, `
		INSERT INTO sosyal.profil (kullanici_id, kullanici_adi, gorunen_ad, bio)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (kullanici_id) DO UPDATE
		   SET kullanici_adi = EXCLUDED.kullanici_adi,
		       gorunen_ad    = EXCLUDED.gorunen_ad,
		       bio           = EXCLUDED.bio
		RETURNING `+profilKolonlari,
		k.KullaniciID, k.KullaniciAdi, k.GorunenAd, k.Bio))
	if _, ok := tekilIhlali(err); ok {
		return nil, ErrKullaniciAdiAlinmis
	}
	if err != nil {
		return nil, fmt.Errorf("profil kaydedilemedi: %w", err)
	}
	return kayit, nil
}

func (d *PostgresDepo) AdSahipleri(ctx context.Context, adlar []string) (map[string]string, error) {
	sahipler := make(map[string]string, len(adlar))
	if len(adlar) == 0 {
		return sahipler, nil
	}
	satirlar, err := d.havuz.Query(ctx,
		`SELECT kullanici_adi, kullanici_id::text FROM sosyal.profil WHERE kullanici_adi = ANY($1)`, adlar)
	if err != nil {
		return nil, fmt.Errorf("adlar okunamadi: %w", err)
	}
	defer satirlar.Close()
	for satirlar.Next() {
		var ad, sahip string
		if err := satirlar.Scan(&ad, &sahip); err != nil {
			return nil, fmt.Errorf("ad okunamadi: %w", err)
		}
		sahipler[ad] = sahip
	}
	return sahipler, satirlar.Err()
}

func (d *PostgresDepo) ProfilSayilari(ctx context.Context, sahipID, izleyiciID string) (*ProfilSayilari, error) {
	gorunur := gorunurlukKosulu("y", "$2", false)
	var s ProfilSayilari
	err := d.havuz.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM sosyal.yayin y
		    WHERE y.sahip_id = $1 AND y.kaynak_yayin_id IS NULL AND y.durum = 'yayinda' AND `+gorunur+`),
		  (SELECT count(*) FROM sosyal.yayin y
		    WHERE y.sahip_id = $1 AND y.kaynak_yayin_id IS NOT NULL AND y.durum = 'yayinda' AND `+gorunur+`),
		  (SELECT count(*) FROM sosyal.takip WHERE takip_edilen_id = $1),
		  (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1),
		  EXISTS (SELECT 1 FROM sosyal.takip WHERE takipci_id = $2 AND takip_edilen_id = $1)`,
		sahipID, izleyiciID,
	).Scan(&s.Istatistik.Sarki, &s.Istatistik.Cover, &s.Istatistik.Takipci, &s.Istatistik.Takip, &s.TakipEdiyorMuyum)
	if err != nil {
		return nil, fmt.Errorf("profil sayaclari okunamadi: %w", err)
	}
	return &s, nil
}
