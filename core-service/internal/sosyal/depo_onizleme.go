package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OnizlemeDeposu, kimliksiz onizleme ucunun sorgusu (spec §4.8).
type OnizlemeDeposu interface {
	// OnizlemeGetir, slug'i tasiyan yayini yalnizca herkese acik
	// onizlemeye uygunsa doner: herkes ya da link, yayinda, silinmemis.
	// Aksi ErrBulunamadi'dir.
	OnizlemeGetir(ctx context.Context, slug string) (*OnizlemeKaydi, error)
}

// onizlemeKosulu, spec §4.9 tablosunun "Herkese acik onizleme"
// sutunudur. Izleyici yoktur: sahibin kendi gorunurlugu ya da takip
// burada rol oynamaz.
const onizlemeKosulu = `y.silinme IS NULL AND y.durum = 'yayinda' AND y.gorunurluk IN ('herkes', 'link')`

func (d *PostgresDepo) OnizlemeGetir(ctx context.Context, slug string) (*OnizlemeKaydi, error) {
	var (
		k        OnizlemeKaydi
		dalga    []float64
		onizleme *string
	)
	err := d.havuz.QueryRow(ctx, `
		SELECT y.baslik, y.sahip_id::text, p.kullanici_adi, p.gorunen_ad, y.sure_sn, y.dalga_formu,
		       y.onizleme_anahtari, y.tur
		  FROM sosyal.yayin y JOIN sosyal.profil p ON p.kullanici_id = y.sahip_id
		 WHERE y.slug = $1 AND `+onizlemeKosulu, slug,
	).Scan(&k.Baslik, &k.SahipID, &k.Sahip.KullaniciAdi, &k.Sahip.GorunenAd, &k.SureSn, &dalga, &onizleme, &k.Tur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("onizleme okunamadi: %w", err)
	}
	k.Sahip.AvatarRengi = AvatarRengi(k.SahipID)
	k.DalgaFormu = dalga
	if k.DalgaFormu == nil {
		k.DalgaFormu = []float64{}
	}
	if onizleme != nil {
		k.OnizlemeAnahtari = *onizleme
	}
	return &k, nil
}
