package social

import (
	"context"
	"fmt"
)

// FollowStore covers the operations on sosyal.takip and suggestions (spec §4.4).
type FollowStore interface {
	// Follow and Unfollow are safe to repeat.
	Follow(ctx context.Context, followerID, followeeID string) error
	Unfollow(ctx context.Context, followerID, followeeID string) error
	// FollowStatus returns the followee's follower count and whether the
	// follower follows them.
	FollowStatus(ctx context.Context, followerID, followeeID string) (*FollowStatus, error)
	// Suggestions returns up to limit publishers the viewer does not follow
	// (excluding themselves), ranked by the likes they received in the last
	// 30 days.
	Suggestions(ctx context.Context, viewerID string, limit int) ([]ProfileRecord, error)
}

func (s *PostgresStore) Follow(ctx context.Context, followerID, followeeID string) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO sosyal.takip (takipci_id, takip_edilen_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, followerID, followeeID); err != nil {
		return fmt.Errorf("takip eklenemedi: %w", err)
	}
	return nil
}

func (s *PostgresStore) Unfollow(ctx context.Context, followerID, followeeID string) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM sosyal.takip WHERE takipci_id = $1 AND takip_edilen_id = $2`,
		followerID, followeeID); err != nil {
		return fmt.Errorf("takip kaldirilamadi: %w", err)
	}
	return nil
}

func (s *PostgresStore) FollowStatus(ctx context.Context, followerID, followeeID string) (*FollowStatus, error) {
	var status FollowStatus
	if err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.takip WHERE takip_edilen_id = $2),
		       EXISTS (SELECT 1 FROM sosyal.takip WHERE takipci_id = $1 AND takip_edilen_id = $2)`,
		followerID, followeeID).Scan(&status.Followers, &status.FollowedByMe); err != nil {
		return nil, fmt.Errorf("takip durumu okunamadi: %w", err)
	}
	return &status, nil
}

// Suggestions: a publisher is a profile with at least one published,
// non-deleted publication with everyone or followers visibility (link
// publications do not put it in the suggestions: that would leak its
// existence). The score is the likes those publications received from others
// in the last 30 days (the owner's own likes do not count). On a tie, the
// newer latest publication wins, then the ID.
func (s *PostgresStore) Suggestions(ctx context.Context, viewerID string, limit int) ([]ProfileRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+profileColumnsP+`
		  FROM sosyal.profil p
		  JOIN LATERAL (
		    SELECT count(b.yayin_id) AS begeni, max(y.olusturma) AS son
		      FROM sosyal.yayin y
		      LEFT JOIN sosyal.begeni b
		        ON b.yayin_id = y.id AND b.kullanici_id <> y.sahip_id
		       AND b.olusturma > now() - interval '30 days'
		     WHERE y.sahip_id = p.kullanici_id AND y.durum = 'yayinda' AND y.silinme IS NULL
		       AND y.gorunurluk IN ('herkes', 'takipciler')
		  ) s ON s.son IS NOT NULL
		 WHERE p.kullanici_id <> $1
		   AND NOT EXISTS (SELECT 1 FROM sosyal.takip t WHERE t.takipci_id = $1 AND t.takip_edilen_id = p.kullanici_id)
		 ORDER BY s.begeni DESC, s.son DESC, p.kullanici_id
		 LIMIT $2`, viewerID, limit)
	if err != nil {
		return nil, fmt.Errorf("oneriler okunamadi: %w", err)
	}
	defer rows.Close()
	records := []ProfileRecord{}
	for rows.Next() {
		profile, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("oneri okunamadi: %w", err)
		}
		records = append(records, *profile)
	}
	return records, rows.Err()
}
