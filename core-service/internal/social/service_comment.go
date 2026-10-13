package social

import (
	"context"
	"github.com/makeasinger/api/internal/i18n"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxCommentLen = 500

// buildCommentPage builds the page and the next cursor from limit+1 comments.
func buildCommentPage(items []Comment, limit int) *Page[Comment] {
	page := &Page[Comment]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = commentCursorAfter(page.Items[limit-1])
	}
	if page.Items == nil {
		page.Items = []Comment{}
	}
	return page
}

// Comments is GET /yayinlar/{id}/yorumlar. ErrNotFound if the publication is
// not visible, ErrCommentsDisabled if its comments are disabled (owner
// included).
func (s *Service) Comments(ctx context.Context, userID, pubID, rawCursor string, limit int) (*Page[Comment], error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	cursor, err := decodeCommentCursor(rawCursor)
	if err != nil {
		return nil, err
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if !rec.CommentsEnabled {
		return nil, ErrCommentsDisabled
	}
	items, err := s.store.ListComments(ctx, rec.ID, id, cursor, limit)
	if err != nil {
		return nil, err
	}
	return buildCommentPage(items, limit), nil
}

// PostComment is POST /yayinlar/{id}/yorumlar (201). Order: body (422),
// visibility (404), yorumAcik (403 yorum_kapali), profile (409
// profil_yok), time range (422). metin is trimmed, 1-500 characters;
// zamanMs is null or 0 <= zamanMs <= sureSn*1000.
func (s *Service) PostComment(ctx context.Context, userID, pubID string, input CommentInput) (*Comment, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(input.Text)
	if containsNUL(text) {
		return nil, invalid(i18n.KeyCommentTextNUL)
	}
	if n := utf8.RuneCountInString(text); n < 1 || n > maxCommentLen {
		return nil, invalid(i18n.KeyCommentTextLength)
	}
	if input.TimeMs != nil && *input.TimeMs < 0 {
		return nil, invalid(i18n.KeyCommentTimeNegative)
	}
	// The time_ms column is an integer; sureSn has no upper bound, so the
	// duration check alone does not prevent an INSERT overflow (500).
	if input.TimeMs != nil && *input.TimeMs > math.MaxInt32 {
		return nil, invalid(i18n.KeyCommentTimeTooLarge)
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if !rec.CommentsEnabled {
		return nil, ErrCommentsDisabled
	}
	if _, err := s.store.GetProfile(ctx, id); err != nil {
		return nil, err
	}
	if input.TimeMs != nil && *input.TimeMs > rec.DurationSec*1000 {
		return nil, invalid(i18n.KeyCommentTimeBeyondDuration)
	}
	inserted, err := s.store.InsertComment(ctx, CommentInsert{
		ID: uuid.NewString(), PublicationID: rec.ID, AuthorID: id, Text: text, TimeMs: input.TimeMs,
	})
	if err != nil {
		return nil, err
	}
	inserted.CanDelete = true
	return inserted, nil
}

// DeleteComment is DELETE /yorumlar/{id} (204). Only the author or the
// publication owner deletes; if someone else can see the publication it is
// ErrForbidden (403), if they cannot it is ErrNotFound (existence is not
// leaked). A deleted comment is ErrNotFound (404 again).
func (s *Service) DeleteComment(ctx context.Context, userID, commentID string) error {
	id, err := parseUserID(userID)
	if err != nil {
		return err
	}
	commentUUID, ok := parseID(commentID)
	if !ok {
		return ErrNotFound
	}
	rec, err := s.store.GetComment(ctx, commentUUID)
	if err != nil {
		return err
	}
	if rec.Deleted {
		return ErrNotFound
	}
	if rec.AuthorID != id && rec.PublicationOwnerID != id {
		if _, err := s.visiblePublication(ctx, id, rec.PublicationID); err != nil {
			return err
		}
		return ErrForbidden
	}
	return s.store.DeleteComment(ctx, rec.ID)
}
