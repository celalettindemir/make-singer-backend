package social

import (
	"context"
	"fmt"
)

// ReportStore covers the operations on sosyal.sikayet (spec §4.7).
type ReportStore interface {
	// InsertReport writes the report. A repeat by the same reporter on the
	// same target creates no new row and is not an error.
	InsertReport(ctx context.Context, report YeniSikayet) error
}

func (s *PostgresStore) InsertReport(ctx context.Context, report YeniSikayet) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden, aciklama)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bildiren_id, hedef_turu, hedef_id) DO NOTHING`,
		report.ID, report.BildirenID, report.HedefTuru, report.HedefID, report.YayinID, report.Neden, report.Aciklama); err != nil {
		return fmt.Errorf("sikayet eklenemedi: %w", err)
	}
	return nil
}
