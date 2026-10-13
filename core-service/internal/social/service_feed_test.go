package social

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func sampleSummaries(n int) []PublicationSummary {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	items := make([]PublicationSummary, n)
	for i := range items {
		items[i].ID = fmt.Sprintf("%08d-1b2c-4d3e-8f9a-0b1c2d3e4f5a", i)
		items[i].CreatedAt = t0.Add(-time.Duration(i) * time.Minute)
	}
	return items
}

func TestService_FeedNewPages(t *testing.T) {
	store := newFakeStore()
	store.feedItems = sampleSummaries(3)
	s, _ := newTestService(store)
	ctx := context.Background()

	page, err := s.Feed(ctx, userA, TabNew, "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("ilk sayfa = %+v, %v", page, err)
	}
	if store.lastFeed.kind != "yeni" || store.lastFeed.limit != 2 || store.lastFeed.timeCursor != nil || store.lastFeed.viewer != userA {
		t.Fatalf("depo cagrisi = %+v", store.lastFeed)
	}
	if _, err := s.Feed(ctx, userA, TabNew, *page.NextCursor, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if store.lastFeed.timeCursor == nil || store.lastFeed.timeCursor.ID != store.feedItems[1].ID || !store.lastFeed.timeCursor.T.Equal(store.feedItems[1].CreatedAt) {
		t.Fatalf("imlec son ogeyi gostermeli: %+v", store.lastFeed.timeCursor)
	}

	store.feedItems = sampleSummaries(2)
	last, err := s.Feed(ctx, userA, TabFollowing, "", 2)
	if err != nil || len(last.Items) != 2 || last.NextCursor != nil || store.lastFeed.kind != "takip" {
		t.Fatalf("son sayfa = %+v, %v", last, err)
	}
	store.feedItems = nil
	empty, err := s.Feed(ctx, userA, TabNew, "", 20)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("bos sayfa ogeler [] olmali: %+v, %v", empty, err)
	}
}

func TestService_FeedForYouStableReference(t *testing.T) {
	store := newFakeStore()
	store.feedItems = sampleSummaries(3)
	s, _ := newTestService(store)
	instant := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return instant }
	ctx := context.Background()

	page, err := s.Feed(ctx, userA, TabForYou, "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("ilk sayfa = %+v, %v", page, err)
	}
	ref := instant.Add(referenceSkew)
	if !store.lastFeed.reference.Equal(ref) || store.lastFeed.scoreCursor != nil {
		t.Fatalf("ilk sayfa referansi simdi arti pay olmali: %+v", store.lastFeed)
	}
	s.now = func() time.Time { return instant.Add(time.Hour) }
	if _, err := s.Feed(ctx, userA, TabForYou, *page.NextCursor, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if !store.lastFeed.reference.Equal(ref) {
		t.Fatalf("sonraki sayfa ayni referansi kullanmali: %v", store.lastFeed.reference)
	}
	if store.lastFeed.scoreCursor == nil || store.lastFeed.scoreCursor.Score != 99 || store.lastFeed.scoreCursor.ID != store.feedItems[1].ID {
		t.Fatalf("puan imleci = %+v", store.lastFeed.scoreCursor)
	}
}

// A future reference time is clamped to now plus a margin; the client cannot
// shift scoring and the next cursor carries the clamped time.
func TestService_FeedForYouClampsFutureReference(t *testing.T) {
	store := newFakeStore()
	store.feedItems = sampleSummaries(3)
	s, _ := newTestService(store)
	instant := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return instant }
	future := encodeCursor(ScoreCursor{Score: 5, ID: store.feedItems[0].ID, RefMs: instant.Add(24 * time.Hour).UnixMilli()})
	page, err := s.Feed(context.Background(), userA, TabForYou, *future, 2)
	if err != nil {
		t.Fatalf("gelecek imlec: %v", err)
	}
	ref := instant.Add(referenceSkew)
	if !store.lastFeed.reference.Equal(ref) {
		t.Fatalf("referans simdi arti paya kirpilmali: %v", store.lastFeed.reference)
	}
	next, err := decodeScoreCursor(*page.NextCursor)
	if err != nil || next.RefMs != ref.UnixMilli() {
		t.Fatalf("sonraki imlec kirpilmis zamani tasimali: %+v, %v", next, err)
	}
}

func TestService_FeedErrors(t *testing.T) {
	s, _ := newTestService(newFakeStore())
	ctx := context.Background()
	if _, err := s.Feed(ctx, userA, "populer", "", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("gecersiz sekme: %v", err)
	}
	if _, err := s.Feed(ctx, userA, TabNew, "bozuk!", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("bozuk imlec: %v", err)
	}
	if _, err := s.Feed(ctx, userA, TabForYou, "bozuk!", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("bozuk puan imleci: %v", err)
	}
}

func TestService_ProfilePublications(t *testing.T) {
	store := newFakeStore()
	store.profiles[userB] = &ProfileRecord{UserID: userB, Username: "veli"}
	store.feedItems = sampleSummaries(1)
	s, _ := newTestService(store)
	ctx := context.Background()

	page, err := s.ProfilePublications(ctx, userA, "veli", "coverlar", "", 20)
	if err != nil || len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("sayfa = %+v, %v", page, err)
	}
	if store.lastFeed.owner != userB || store.lastFeed.viewer != userA || !store.lastFeed.covers {
		t.Fatalf("depo cagrisi = %+v", store.lastFeed)
	}
	if _, err := s.ProfilePublications(ctx, userA, "veli", "", "", 20); err != nil || store.lastFeed.covers {
		t.Fatalf("bos tur sarkilar olmali: %v %+v", err, store.lastFeed)
	}
	if _, err := s.ProfilePublications(ctx, userA, "veli", "videolar", "", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("gecersiz tur: %v", err)
	}
	if _, err := s.ProfilePublications(ctx, userA, "yok", "sarkilar", "", 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan profil: %v", err)
	}
}

func TestService_SetLike(t *testing.T) {
	store := newFakeStore()
	store.publications[pubX] = fakePublication(pubX, userB, VisibilityEveryone, StatusPublished)
	store.publications[pubY] = fakePublication(pubY, userB, VisibilityFollowers, StatusPublished)
	s, _ := newTestService(store)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		b, err := s.SetLike(ctx, userA, pubX, true)
		if err != nil || !b.LikedByMe || b.LikeCount != 1 {
			t.Fatalf("begen %d = %+v, %v", i, b, err)
		}
	}
	b, err := s.SetLike(ctx, userA, pubX, false)
	if err != nil || b.LikedByMe || b.LikeCount != 0 {
		t.Fatalf("kaldir = %+v, %v", b, err)
	}
	if _, err := s.SetLike(ctx, userA, pubY, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gorulmeyen yayin begenilemez: %v", err)
	}
}
