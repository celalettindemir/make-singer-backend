package social

import (
	"context"
	"fmt"
)

// ListPublicationIDs returns ALL of the user's publication IDs (soft-deleted
// ones included). Account deletion clears the sosyal/{id}/ prefix for each of
// them; the rows are deleted only once the R2 cleanup succeeded, so the
// delayed retry can find the same IDs again.
func (s *PostgresStore) ListPublicationIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text FROM sosyal.yayin WHERE sahip_id = $1 ORDER BY olusturma`, userID)
	if err != nil {
		return nil, fmt.Errorf("yayinlar listelenemedi: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("yayin kimligi okunamadi: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// deleteCommentReportsSQL deletes the reports (filed by others) on the
// user's comments. hedef_id is polymorphic, so there is no cascade; this
// must run BEFORE the comments are deleted.
const deleteCommentReportsSQL = `DELETE FROM sosyal.sikayet
	WHERE hedef_turu = 'yorum' AND hedef_id IN (SELECT id FROM sosyal.yorum WHERE yazar_id = $1)`

// traceDeleteQueries delete all of the user's records except publications.
var traceDeleteQueries = []string{
	deleteCommentReportsSQL,
	`DELETE FROM sosyal.begeni WHERE kullanici_id = $1`,
	`DELETE FROM sosyal.yorum WHERE yazar_id = $1`,
	`DELETE FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1`,
	`DELETE FROM sosyal.sikayet WHERE bildiren_id = $1`,
	`DELETE FROM sosyal.profil WHERE kullanici_id = $1`,
}

// DeleteUserTraces deletes the user's records OTHER than publications in a
// single transaction: reports on their comments, likes, comments, follows
// (both directions), reports they filed, and the profile. Read paths are
// tied to the profile, so the publications become invisible at once too; the
// publication rows stay until the R2 cleanup is done so the delayed retry can
// list the IDs again.
func (s *PostgresStore) DeleteUserTraces(ctx context.Context, userID string) error {
	return s.deleteInTx(ctx, userID, traceDeleteQueries)
}

// DeleteUserData deletes the user's social records in a single transaction:
// the traces (same as DeleteUserTraces) and the publications (with their
// likes, comments and reports by cascade). Other users' covers stay; their
// source shows as deleted.
func (s *PostgresStore) DeleteUserData(ctx context.Context, userID string) error {
	queries := append(append([]string{}, traceDeleteQueries...), `DELETE FROM sosyal.yayin WHERE sahip_id = $1`)
	return s.deleteInTx(ctx, userID, queries)
}

func (s *PostgresStore) deleteInTx(ctx context.Context, userID string, queries []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sosyal silme islemi baslatilamadi: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query, userID); err != nil {
			return fmt.Errorf("sosyal veri silinemedi: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sosyal silme commit edilemedi: %w", err)
	}
	return nil
}
