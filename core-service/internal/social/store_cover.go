package social

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CoverStore holds the cover list and cover start queries (spec §4.6).
type CoverStore interface {
	// ListCovers returns up to limit+1 of the source's published, non-deleted
	// covers, newest first, limited to what the viewer may see in a list.
	ListCovers(ctx context.Context, sourceID, viewerID string, cursor *ZamanImleci, limit int) ([]YayinOzeti, error)
	// ListStems returns the publication's copied stems (with their internal
	// keys), or ErrNotFound if the publication does not exist.
	ListStems(ctx context.Context, pubID string) ([]StemKaydi, error)
}

func (s *PostgresStore) ListCovers(ctx context.Context, sourceID, viewerID string, cursor *ZamanImleci, limit int) ([]YayinOzeti, error) {
	condition := `y.kaynak_yayin_id = $2 AND y.durum = 'yayinda' AND ` + gorunurlukKosulu("y", "$1", false)
	return s.timePage(ctx, condition, cursor, limit, viewerID, sourceID)
}

func (s *PostgresStore) ListStems(ctx context.Context, pubID string) ([]StemKaydi, error) {
	var stems []StemKaydi
	err := s.pool.QueryRow(ctx, `SELECT stem_listesi FROM sosyal.yayin WHERE id = $1`, pubID).Scan(&stems)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("stem listesi okunamadi: %w", err)
	}
	if stems == nil {
		stems = []StemKaydi{}
	}
	return stems, nil
}
