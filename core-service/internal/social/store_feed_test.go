package social

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// allTimePages walks a time list to the end in pages of limit and
// returns the ids in order.
func allTimePages(t *testing.T, fetch func(*TimeCursor) ([]PublicationSummary, error), limit int) []string {
	t.Helper()
	var (
		ids    []string
		cursor *TimeCursor
	)
	for pageNo := 0; pageNo < 20; pageNo++ {
		items, err := fetch(cursor)
		if err != nil {
			t.Fatalf("sayfa %d: %v", pageNo, err)
		}
		done := len(items) <= limit
		if !done {
			items = items[:limit]
		}
		for _, out := range items {
			ids = append(ids, out.ID)
		}
		if done {
			return ids
		}
		last := items[len(items)-1]
		cursor = &TimeCursor{T: last.CreatedAt, ID: last.ID}
	}
	t.Fatal("sayfalama bitmedi")
	return nil
}

func assertOrder(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d oge, beklenen %d (%v)", name, len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: sira %d = %s, beklenen %s", name, i, got[i], want[i])
		}
	}
}

func setupFeed(t *testing.T) (*pgxpool.Pool, *PostgresStore, string, string, string) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	me, followee, stranger := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, me, "ben")
	testInsertProfile(t, store, followee, "takip_edilen")
	testInsertProfile(t, store, stranger, "yabanci")
	testInsertFollow(t, pool, me, followee)
	return pool, store, me, followee, stranger
}

func TestPostgresStore_FeedNewPaging(t *testing.T) {
	pool, store, me, followee, stranger := setupFeed(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	sameInstant := t0.Add(10 * time.Minute)
	// Expected order: newest to oldest; those created at the same instant by id descending.
	a := testInsertPublication(t, pool, testPublication{OwnerID: stranger, CreatedAt: t0.Add(30 * time.Minute)})
	b := testInsertPublication(t, pool, testPublication{OwnerID: me, CreatedAt: t0.Add(20 * time.Minute)})
	c1 := testInsertPublication(t, pool, testPublication{ID: "ffffffff-0000-4000-8000-000000000001", OwnerID: followee, CreatedAt: sameInstant})
	c2 := testInsertPublication(t, pool, testPublication{ID: "00000000-0000-4000-8000-000000000001", OwnerID: followee, CreatedAt: sameInstant})
	tied := testInsertPublication(t, pool, testPublication{OwnerID: stranger, CreatedAt: t0})
	// Not in the feed:
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, Visibility: VisibilityFollowers})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, Visibility: VisibilityLink})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, Status: StatusPreparing})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, Deleted: true})

	want := []string{a, b, c1, c2, tied}
	for _, limit := range []int{1, 2, 5, 50} {
		got := allTimePages(t, func(cur *TimeCursor) ([]PublicationSummary, error) { return store.FeedNew(ctx, me, cur, limit) }, limit)
		assertOrder(t, "yeni", got, want)
	}
}

func TestPostgresStore_FeedFollowing(t *testing.T) {
	pool, store, me, followee, stranger := setupFeed(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	everyone := testInsertPublication(t, pool, testPublication{OwnerID: followee, CreatedAt: t0.Add(2 * time.Minute)})
	followersOnly := testInsertPublication(t, pool, testPublication{OwnerID: followee, Visibility: VisibilityFollowers, CreatedAt: t0.Add(time.Minute)})
	testInsertPublication(t, pool, testPublication{OwnerID: followee, Visibility: VisibilityLink})
	testInsertPublication(t, pool, testPublication{OwnerID: followee, Status: StatusFailed})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger})
	testInsertPublication(t, pool, testPublication{OwnerID: me})

	got := allTimePages(t, func(cur *TimeCursor) ([]PublicationSummary, error) { return store.FeedFollowing(ctx, me, cur, 20) }, 20)
	assertOrder(t, "takip", got, []string{everyone, followersOnly})
}

func TestPostgresStore_ListProfilePublications(t *testing.T) {
	pool, store, me, followee, stranger := setupFeed(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	everyone := testInsertPublication(t, pool, testPublication{OwnerID: followee, CreatedAt: t0.Add(5 * time.Minute)})
	followersOnly := testInsertPublication(t, pool, testPublication{OwnerID: followee, Visibility: VisibilityFollowers, CreatedAt: t0.Add(4 * time.Minute)})
	link := testInsertPublication(t, pool, testPublication{OwnerID: followee, Visibility: VisibilityLink, CreatedAt: t0.Add(3 * time.Minute)})
	preparing := testInsertPublication(t, pool, testPublication{OwnerID: followee, Status: StatusPreparing, CreatedAt: t0.Add(2 * time.Minute)})
	testInsertPublication(t, pool, testPublication{OwnerID: followee, Deleted: true})
	cover := testInsertPublication(t, pool, testPublication{OwnerID: followee, SourceID: everyone, CreatedAt: t0.Add(time.Minute)})

	listFor := func(viewer string, coversOnly bool) []string {
		return allTimePages(t, func(cur *TimeCursor) ([]PublicationSummary, error) {
			return store.ListProfilePublications(ctx, followee, viewer, coversOnly, cur, 2)
		}, 2)
	}
	assertOrder(t, "sahip", listFor(followee, false), []string{everyone, followersOnly, link, preparing})
	assertOrder(t, "takipci", listFor(me, false), []string{everyone, followersOnly})
	assertOrder(t, "yabanci", listFor(stranger, false), []string{everyone})
	assertOrder(t, "coverlar", listFor(stranger, true), []string{cover})

	items, _ := store.ListProfilePublications(ctx, followee, stranger, true, nil, 20)
	if items[0].Source == nil || items[0].Source.PublicationID != everyone || items[0].Owner.Username != "takip_edilen" {
		t.Fatalf("cover ozeti = %+v", items[0])
	}
}

func insertLikes(t *testing.T, pool *pgxpool.Pool, pubID string, count int, at time.Time) {
	t.Helper()
	for i := 0; i < count; i++ {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO sosyal.begeni (kullanici_id, yayin_id, olusturma) VALUES ($1, $2, $3)`,
			uuid.NewString(), pubID, at); err != nil {
			t.Fatalf("begeni: %v", err)
		}
	}
}

func TestPostgresStore_FeedForYouScoreAndPaging(t *testing.T) {
	pool, store, me, followee, stranger := setupFeed(t)
	ctx := context.Background()
	refTime := time.Now().Truncate(time.Millisecond)

	// Same age: more likes ranks higher. The old publication ranks lower.
	popular := testInsertPublication(t, pool, testPublication{OwnerID: stranger, CreatedAt: refTime.Add(-2 * time.Hour)})
	insertLikes(t, pool, popular, 5, refTime.Add(-time.Hour))
	quiet := testInsertPublication(t, pool, testPublication{OwnerID: followee, CreatedAt: refTime.Add(-2 * time.Hour)})
	oldPub := testInsertPublication(t, pool, testPublication{OwnerID: stranger, CreatedAt: refTime.Add(-72 * time.Hour)})
	// Likes older than 7 days and likes after the reference are not counted.
	insertLikes(t, pool, oldPub, 50, refTime.Add(-8*24*time.Hour))
	insertLikes(t, pool, quiet, 50, refTime.Add(time.Hour))
	// Not in the feed: my own publication, one created after the reference, a non-everyone one.
	testInsertPublication(t, pool, testPublication{OwnerID: me, CreatedAt: refTime.Add(-time.Hour)})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, CreatedAt: refTime.Add(time.Minute)})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, Visibility: VisibilityLink, CreatedAt: refTime.Add(-time.Hour)})

	all, err := store.FeedForYou(ctx, me, refTime, nil, 50)
	if err != nil {
		t.Fatalf("AkisSeninIcin: %v", err)
	}
	var ids []string
	for _, out := range all {
		ids = append(ids, out.ID)
	}
	assertOrder(t, "senin icin", ids, []string{popular, quiet, oldPub})
	// (1+5)/(2+2)^1.5 = 0.75
	if p := all[0].Score; p < 0.7499 || p > 0.7501 {
		t.Fatalf("populer puani = %v, beklenen 0.75", p)
	}

	// Same order with single-item pages, no repeats.
	var (
		visited []string
		cursor  *ScoreCursor
	)
	for i := 0; i < 5; i++ {
		items, err := store.FeedForYou(ctx, me, refTime, cursor, 1)
		if err != nil {
			t.Fatalf("sayfa: %v", err)
		}
		visited = append(visited, items[0].ID)
		if len(items) == 1 {
			break
		}
		cursor = &ScoreCursor{Score: items[0].Score, ID: items[0].ID, RefMs: refTime.UnixMilli()}
	}
	assertOrder(t, "senin icin sayfalari", visited, []string{popular, quiet, oldPub})
}

func TestPostgresStore_Like(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner, me := uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	id := testInsertPublication(t, pool, testPublication{OwnerID: owner})
	for i := 0; i < 2; i++ {
		if err := store.Like(ctx, me, id); err != nil {
			t.Fatalf("Begen %d: %v", i, err)
		}
	}
	rec, _ := store.GetPublication(ctx, id, me)
	if rec.LikeCount != 1 || !rec.LikedByMe {
		t.Fatalf("begeni tekrari guvenli olmali: %d %v", rec.LikeCount, rec.LikedByMe)
	}
	for i := 0; i < 2; i++ {
		if err := store.Unlike(ctx, me, id); err != nil {
			t.Fatalf("Kaldir %d: %v", i, err)
		}
	}
	rec, _ = store.GetPublication(ctx, id, me)
	if rec.LikeCount != 0 || rec.LikedByMe {
		t.Fatalf("begeni kaldirilmali: %d %v", rec.LikeCount, rec.LikedByMe)
	}
}
