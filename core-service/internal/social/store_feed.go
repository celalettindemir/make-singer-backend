package social

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// FeedStore holds the queries behind the list endpoints. Each method returns
// at most limit+1 items; the extra one is the service's "there is a next
// page" signal.
type FeedStore interface {
	// ListProfilePublications lists the owner's originals or covers, newest
	// first, limited to what the viewer may see.
	ListProfilePublications(ctx context.Context, ownerID, viewerID string, covers bool, cursor *ZamanImleci, limit int) ([]YayinOzeti, error)
	// FeedNew: everyone-visible, published, non-deleted; newest first.
	FeedNew(ctx context.Context, viewerID string, cursor *ZamanImleci, limit int) ([]YayinOzeti, error)
	// FeedFollowing: the everyone and followers publications of the followees.
	FeedFollowing(ctx context.Context, viewerID string, cursor *ZamanImleci, limit int) ([]YayinOzeti, error)
	// FeedForYou: everyone-visible publications not owned by the viewer, by
	// descending score relative to the reference time.
	FeedForYou(ctx context.Context, viewerID string, reference time.Time, cursor *PuanImleci, limit int) ([]ScoredSummary, error)
}

// LikeStore covers the operations on sosyal.begeni. Both are safe to repeat.
type LikeStore interface {
	Like(ctx context.Context, userID, pubID string) error
	Unlike(ctx context.Context, userID, pubID string) error
}

// ScoredSummary is a feed item ordered by score; the score goes into the cursor.
type ScoredSummary struct {
	YayinOzeti
	Score float64
}

// timePage fetches a page in descending time order with the given condition
// and parameters, where $1 is the viewer. The cursor parameters are appended
// at the end.
func (s *PostgresStore) timePage(ctx context.Context, condition string, cursor *ZamanImleci, limit int, args ...any) ([]YayinOzeti, error) {
	var cursorTime, cursorID any
	if cursor != nil {
		cursorTime, cursorID = cursor.T, cursor.ID
	}
	argCount := len(args)
	args = append(args, cursorTime, cursorID, limit+1)
	query := fmt.Sprintf(`SELECT %s %s
		WHERE %s
		  AND ($%d::timestamptz IS NULL OR (y.olusturma, y.id) < ($%d::timestamptz, $%d::uuid))
		ORDER BY y.olusturma DESC, y.id DESC
		LIMIT $%d`, summarySelect, summaryFrom, condition, argCount+1, argCount+1, argCount+2, argCount+3)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("liste okunamadi: %w", err)
	}
	return scanSummaries(rows)
}

func scanSummaries(rows pgx.Rows) ([]YayinOzeti, error) {
	defer rows.Close()
	items := []YayinOzeti{}
	for rows.Next() {
		var sc summaryScanner
		if err := rows.Scan(sc.targets()...); err != nil {
			return nil, fmt.Errorf("liste ogesi okunamadi: %w", err)
		}
		items = append(items, sc.summary())
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListProfilePublications(ctx context.Context, ownerID, viewerID string, covers bool, cursor *ZamanImleci, limit int) ([]YayinOzeti, error) {
	condition := `y.sahip_id = $2 AND (y.kaynak_yayin_id IS NOT NULL) = $3 AND ` + gorunurlukKosulu("y", "$1", false)
	return s.timePage(ctx, condition, cursor, limit, viewerID, ownerID, covers)
}

func (s *PostgresStore) FeedNew(ctx context.Context, viewerID string, cursor *ZamanImleci, limit int) ([]YayinOzeti, error) {
	condition := `y.gorunurluk = 'herkes' AND y.durum = 'yayinda' AND y.silinme IS NULL`
	return s.timePage(ctx, condition, cursor, limit, viewerID)
}

func (s *PostgresStore) FeedFollowing(ctx context.Context, viewerID string, cursor *ZamanImleci, limit int) ([]YayinOzeti, error) {
	condition := `y.gorunurluk IN ('herkes', 'takipciler') AND y.durum = 'yayinda' AND y.silinme IS NULL
		AND EXISTS (SELECT 1 FROM sosyal.takip tk WHERE tk.takipci_id = $1 AND tk.takip_edilen_id = y.sahip_id)`
	return s.timePage(ctx, condition, cursor, limit, viewerID)
}

// scoreExpr, spec §5.1: (1 + b + 2y + 3c) / (hours + 2)^1.5. b, y and c are
// the like, comment and cover counts of the 7 days before the reference;
// hours is the time from publication to the reference. Interactions after the
// reference do not count, so the score stays fixed across pages fetched with
// the same reference.
const scoreExpr = `
	(1
	 + (SELECT count(*) FROM sosyal.begeni b
	     WHERE b.yayin_id = y.id AND b.olusturma > $2::timestamptz - interval '7 days' AND b.olusturma <= $2::timestamptz)
	 + 2 * (SELECT count(*) FROM sosyal.yorum c
	     WHERE c.yayin_id = y.id AND c.silinme IS NULL
	       AND c.olusturma > $2::timestamptz - interval '7 days' AND c.olusturma <= $2::timestamptz)
	 + 3 * (SELECT count(*) FROM sosyal.yayin cv
	     WHERE cv.kaynak_yayin_id = y.id AND cv.durum = 'yayinda' AND cv.silinme IS NULL
	       AND cv.olusturma > $2::timestamptz - interval '7 days' AND cv.olusturma <= $2::timestamptz)
	)::float8
	/ power(extract(epoch FROM ($2::timestamptz - y.olusturma))::float8 / 3600.0 + 2, 1.5)`

func (s *PostgresStore) FeedForYou(ctx context.Context, viewerID string, reference time.Time, cursor *PuanImleci, limit int) ([]ScoredSummary, error) {
	var cursorScore, cursorID any
	if cursor != nil {
		cursorScore, cursorID = cursor.P, cursor.ID
	}
	// A publication created after the reference is excluded from this feed
	// session: its score would be undefined (negative hours) and the pages
	// would shift.
	rows, err := s.pool.Query(ctx, `
		SELECT s.* FROM (
		  SELECT `+summarySelect+`, `+scoreExpr+` AS puan, y.id AS sira_id
		  `+summaryFrom+`
		  WHERE y.gorunurluk = 'herkes' AND y.durum = 'yayinda' AND y.silinme IS NULL
		    AND y.sahip_id <> $1 AND y.olusturma <= $2::timestamptz
		) s
		WHERE ($3::float8 IS NULL OR (s.puan, s.sira_id) < ($3::float8, $4::uuid))
		ORDER BY s.puan DESC, s.sira_id DESC
		LIMIT $5`, viewerID, reference, cursorScore, cursorID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("akis okunamadi: %w", err)
	}
	defer rows.Close()
	items := []ScoredSummary{}
	for rows.Next() {
		var (
			sc     summaryScanner
			score  float64
			rankID string
		)
		if err := rows.Scan(append(sc.targets(), &score, &rankID)...); err != nil {
			return nil, fmt.Errorf("akis ogesi okunamadi: %w", err)
		}
		items = append(items, ScoredSummary{YayinOzeti: sc.summary(), Score: score})
	}
	return items, rows.Err()
}

func (s *PostgresStore) Like(ctx context.Context, userID, pubID string) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, pubID); err != nil {
		return fmt.Errorf("begeni eklenemedi: %w", err)
	}
	return nil
}

func (s *PostgresStore) Unlike(ctx context.Context, userID, pubID string) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM sosyal.begeni WHERE kullanici_id = $1 AND yayin_id = $2`, userID, pubID); err != nil {
		return fmt.Errorf("begeni kaldirilamadi: %w", err)
	}
	return nil
}
