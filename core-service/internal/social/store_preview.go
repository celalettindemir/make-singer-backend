package social

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PreviewStore is the query behind the unauthenticated preview endpoint (spec §4.8).
type PreviewStore interface {
	// GetPreview returns the publication with that slug only if it qualifies
	// for the public preview: everyone or link visibility, published,
	// non-deleted. Otherwise it is ErrNotFound.
	GetPreview(ctx context.Context, slug string) (*OnizlemeKaydi, error)
}

// previewCondition is the "Public preview" column of the spec §4.9 table.
// There is no viewer: the owner's own visibility and following play no part
// here.
const previewCondition = `y.silinme IS NULL AND y.durum = 'yayinda' AND y.gorunurluk IN ('herkes', 'link')`

func (s *PostgresStore) GetPreview(ctx context.Context, slug string) (*OnizlemeKaydi, error) {
	var (
		record     OnizlemeKaydi
		waveform   []float64
		previewKey *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT y.baslik, y.sahip_id::text, p.kullanici_adi, p.gorunen_ad, y.sure_sn, y.dalga_formu,
		       y.onizleme_anahtari, y.tur
		  FROM sosyal.yayin y JOIN sosyal.profil p ON p.kullanici_id = y.sahip_id
		 WHERE y.slug = $1 AND `+previewCondition, slug,
	).Scan(&record.Baslik, &record.SahipID, &record.Sahip.KullaniciAdi, &record.Sahip.GorunenAd, &record.SureSn, &waveform, &previewKey, &record.Tur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("onizleme okunamadi: %w", err)
	}
	record.Sahip.AvatarRengi = AvatarRengi(record.SahipID)
	record.DalgaFormu = waveform
	if record.DalgaFormu == nil {
		record.DalgaFormu = []float64{}
	}
	if previewKey != nil {
		record.OnizlemeAnahtari = *previewKey
	}
	return &record, nil
}
