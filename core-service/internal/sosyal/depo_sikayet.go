package sosyal

import (
	"context"
	"fmt"
)

// SikayetDeposu, sosyal.sikayet islemleri (spec §4.7).
type SikayetDeposu interface {
	// SikayetEkle, sikayeti yazar. Ayni bildirenin ayni hedefe tekrari
	// yeni kayit acmaz ve hata da degildir.
	SikayetEkle(ctx context.Context, s YeniSikayet) error
}

func (d *PostgresDepo) SikayetEkle(ctx context.Context, s YeniSikayet) error {
	if _, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden, aciklama)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bildiren_id, hedef_turu, hedef_id) DO NOTHING`,
		s.ID, s.BildirenID, s.HedefTuru, s.HedefID, s.YayinID, s.Neden, s.Aciklama); err != nil {
		return fmt.Errorf("sikayet eklenemedi: %w", err)
	}
	return nil
}
