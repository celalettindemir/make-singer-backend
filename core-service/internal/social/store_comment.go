package social

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CommentStore covers the operations on sosyal.yorum (spec §4.5).
type CommentStore interface {
	// InsertComment writes the comment and returns it with the author's summary.
	InsertComment(ctx context.Context, input CommentInsert) (*Comment, error)
	// ListComments returns up to limit+1 of the publication's non-deleted
	// comments in contract order. CanDelete depends on the viewer (the author
	// or the publication owner).
	ListComments(ctx context.Context, pubID, viewerID string, cursor *CommentCursor, limit int) ([]Comment, error)
	// GetComment returns the comment for the delete decision, or ErrNotFound.
	GetComment(ctx context.Context, id string) (*CommentRecord, error)
	// DeleteComment soft-deletes. If missing or already deleted, ErrNotFound.
	DeleteComment(ctx context.Context, id string) error
}

// commentColumns are the columns of Comment: c is the comment, p the author's profile.
const commentColumns = `c.id::text, c.metin, c.zaman_ms, c.olusturma, c.yazar_id::text, p.kullanici_adi, p.gorunen_ad`

func scanComment(row pgx.Row, extra ...any) (*Comment, error) {
	var (
		comment  Comment
		authorID string
	)
	dests := append([]any{&comment.ID, &comment.Text, &comment.TimeMs, &comment.CreatedAt, &authorID,
		&comment.Author.Username, &comment.Author.DisplayName}, extra...)
	if err := row.Scan(dests...); err != nil {
		return nil, err
	}
	comment.Author.AvatarColor = AvatarColor(authorID)
	comment.CreatedAt = comment.CreatedAt.UTC()
	return &comment, nil
}

func (s *PostgresStore) InsertComment(ctx context.Context, input CommentInsert) (*Comment, error) {
	comment, err := scanComment(s.pool.QueryRow(ctx, `
		WITH c AS (
		  INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, zaman_ms)
		  VALUES ($1, $2, $3, $4, $5)
		  RETURNING *
		)
		SELECT `+commentColumns+` FROM c JOIN sosyal.profil p ON p.kullanici_id = c.yazar_id`,
		input.ID, input.PublicationID, input.AuthorID, input.Text, input.TimeMs))
	if err != nil {
		return nil, fmt.Errorf("yorum eklenemedi: %w", err)
	}
	return comment, nil
}

// ListComments orders two groups in a single query: comments with zaman_ms
// first (zaman_ms, olusturma, id ascending), then the untimed ones
// (olusturma, id descending). If the cursor group is 0, the remaining timed
// comments and all untimed ones follow; if it is 1, only the remaining
// untimed ones do.
func (s *PostgresStore) ListComments(ctx context.Context, pubID, viewerID string, cursor *CommentCursor, limit int) ([]Comment, error) {
	var group, timeMs, createdAt, id any
	if cursor != nil {
		group, createdAt, id = cursor.Group, cursor.T, cursor.ID
		if cursor.TimeMs != nil {
			timeMs = *cursor.TimeMs
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+commentColumns+`, (c.yazar_id = $2 OR y.sahip_id = $2)
		  FROM sosyal.yorum c
		  JOIN sosyal.yayin y ON y.id = c.yayin_id
		  JOIN sosyal.profil p ON p.kullanici_id = c.yazar_id
		 WHERE c.yayin_id = $1 AND c.silinme IS NULL
		   AND ($3::int IS NULL
		        OR ($3::int = 0 AND (c.zaman_ms IS NULL
		            OR (c.zaman_ms, c.olusturma, c.id) > ($4::int, $5::timestamptz, $6::uuid)))
		        OR ($3::int = 1 AND c.zaman_ms IS NULL
		            AND (c.olusturma, c.id) < ($5::timestamptz, $6::uuid)))
		 ORDER BY (c.zaman_ms IS NULL), c.zaman_ms,
		          CASE WHEN c.zaman_ms IS NULL THEN c.olusturma END DESC,
		          CASE WHEN c.zaman_ms IS NULL THEN c.id END DESC,
		          c.olusturma, c.id
		 LIMIT $7`, pubID, viewerID, group, timeMs, createdAt, id, limit+1)
	if err != nil {
		return nil, fmt.Errorf("yorumlar okunamadi: %w", err)
	}
	defer rows.Close()
	comments := []Comment{}
	for rows.Next() {
		var canDelete bool
		comment, err := scanComment(rows, &canDelete)
		if err != nil {
			return nil, fmt.Errorf("yorum okunamadi: %w", err)
		}
		comment.CanDelete = canDelete
		comments = append(comments, *comment)
	}
	return comments, rows.Err()
}

func (s *PostgresStore) GetComment(ctx context.Context, id string) (*CommentRecord, error) {
	var record CommentRecord
	err := s.pool.QueryRow(ctx, `
		SELECT c.id::text, c.yayin_id::text, c.yazar_id::text, y.sahip_id::text, c.silinme IS NOT NULL
		  FROM sosyal.yorum c JOIN sosyal.yayin y ON y.id = c.yayin_id
		 WHERE c.id = $1`, id).Scan(&record.ID, &record.PublicationID, &record.AuthorID, &record.PublicationOwnerID, &record.Deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("yorum okunamadi: %w", err)
	}
	return &record, nil
}

func (s *PostgresStore) DeleteComment(ctx context.Context, id string) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE sosyal.yorum SET silinme = now() WHERE id = $1 AND silinme IS NULL`, id)
	if err != nil {
		return fmt.Errorf("yorum silinemedi: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
