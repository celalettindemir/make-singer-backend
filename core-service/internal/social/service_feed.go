package social

import (
	"context"
	"github.com/makeasinger/api/internal/i18n"
	"time"
)

// Feed tabs (GET /akis?sekme=).
const (
	TabForYou    = "senin-icin"
	TabFollowing = "takip"
	TabNew       = "yeni"
)

// LikeStatus is the PUT/DELETE /yayinlar/{id}/begeni response.
type LikeStatus struct {
	LikedByMe bool `json:"begendimMi"`
	LikeCount int  `json:"begeniSayisi"`
}

// buildTimePage builds the page and the next cursor from limit+1 items.
func buildTimePage(items []PublicationSummary, limit int) *Page[PublicationSummary] {
	page := &Page[PublicationSummary]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(TimeCursor{T: last.CreatedAt, ID: last.ID})
	}
	if page.Items == nil {
		page.Items = []PublicationSummary{}
	}
	return page
}

// Feed is GET /akis. An invalid tab or cursor is ErrValidation.
func (s *Service) Feed(ctx context.Context, userID, tab, rawCursor string, limit int) (*Page[PublicationSummary], error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	switch tab {
	case TabNew, TabFollowing:
		cursor, err := decodeTimeCursor(rawCursor)
		if err != nil {
			return nil, err
		}
		fetch := s.store.FeedNew
		if tab == TabFollowing {
			fetch = s.store.FeedFollowing
		}
		items, err := fetch(ctx, id, cursor, limit)
		if err != nil {
			return nil, err
		}
		return buildTimePage(items, limit), nil
	case TabForYou:
		return s.forYou(ctx, id, rawCursor, limit)
	}
	return nil, invalid(i18n.KeyFeedTabInvalid)
}

// referenceSkew is the allowance for the difference between the application and database clocks.
const referenceSkew = time.Second

// forYou fetches the scored feed. The first page takes "now" as the
// reference time; later pages use the same reference from the cursor.
func (s *Service) forYou(ctx context.Context, id, rawCursor string, limit int) (*Page[PublicationSummary], error) {
	cursor, err := decodeScoreCursor(rawCursor)
	if err != nil {
		return nil, err
	}
	// The reference is moved forward by a clock allowance and rounded up to
	// the millisecond: the publication's creation time comes from the database
	// clock, so a new publication must not drop off the first page if the
	// application clock runs slightly behind or truncation discards the
	// sub-millisecond part.
	now := s.now().Add(referenceSkew).Truncate(time.Millisecond)
	reference := now
	if cursor != nil {
		reference = time.UnixMilli(cursor.RefMs)
	}
	// Clamped to now so that a future reference does not shift the scoring.
	if reference.After(now) {
		reference = now
	}
	items, err := s.store.FeedForYou(ctx, id, reference, cursor, limit)
	if err != nil {
		return nil, err
	}
	page := &Page[PublicationSummary]{Items: make([]PublicationSummary, 0, len(items))}
	for i, item := range items {
		if i == limit {
			last := items[limit-1]
			page.NextCursor = encodeCursor(ScoreCursor{Score: last.Score, ID: last.ID, RefMs: reference.UnixMilli()})
			break
		}
		page.Items = append(page.Items, item.PublicationSummary)
	}
	return page, nil
}

// ProfilePublications is GET /profiller/{ad}/yayinlar?tur=sarkilar|coverlar.
// An empty tur means sarkilar.
func (s *Service) ProfilePublications(ctx context.Context, viewerID, username, kind, rawCursor string, limit int) (*Page[PublicationSummary], error) {
	id, err := parseUserID(viewerID)
	if err != nil {
		return nil, err
	}
	var covers bool
	switch kind {
	case "", "sarkilar":
	case "coverlar":
		covers = true
	default:
		return nil, invalid(i18n.KeyProfileFeedTypeInvalid)
	}
	cursor, err := decodeTimeCursor(rawCursor)
	if err != nil {
		return nil, err
	}
	if !ValidUsername(username) {
		return nil, ErrNotFound
	}
	p, err := s.store.GetProfileByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListProfilePublications(ctx, p.UserID, id, covers, cursor, limit)
	if err != nil {
		return nil, err
	}
	return buildTimePage(items, limit), nil
}

// SetLike is PUT (like=true) and DELETE (false) /yayinlar/{id}/begeni.
// Repeating is safe; only a visible publication can be liked.
func (s *Service) SetLike(ctx context.Context, userID, pubID string, like bool) (*LikeStatus, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	rec, err := s.visiblePublication(ctx, id, pubID)
	if err != nil {
		return nil, err
	}
	if like {
		err = s.store.Like(ctx, id, rec.ID)
	} else {
		err = s.store.Unlike(ctx, id, rec.ID)
	}
	if err != nil {
		return nil, err
	}
	updated, err := s.store.GetPublication(ctx, rec.ID, id)
	if err != nil {
		return nil, err
	}
	return &LikeStatus{LikedByMe: updated.LikedByMe, LikeCount: updated.LikeCount}, nil
}
