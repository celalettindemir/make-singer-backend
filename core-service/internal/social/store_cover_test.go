package social

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresStore_ListCoversVisibilityAndOrder(t *testing.T) {
	pool, store, me, followee, stranger := setupFeed(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	source := testInsertPublication(t, pool, testPublication{OwnerID: stranger, BackingEnabled: true})
	other := testInsertPublication(t, pool, testPublication{OwnerID: stranger, BackingEnabled: true})

	c1 := testInsertPublication(t, pool, testPublication{OwnerID: stranger, SourceID: source, CreatedAt: t0.Add(4 * time.Minute)})
	c2 := testInsertPublication(t, pool, testPublication{OwnerID: followee, SourceID: source, Visibility: VisibilityFollowers, CreatedAt: t0.Add(3 * time.Minute)})
	c3 := testInsertPublication(t, pool, testPublication{OwnerID: me, SourceID: source, Visibility: VisibilityLink, CreatedAt: t0.Add(2 * time.Minute)})
	c4 := testInsertPublication(t, pool, testPublication{OwnerID: stranger, SourceID: source, CreatedAt: t0.Add(time.Minute)})
	// Not included: the stranger's followers-only and link covers, one still preparing
	// (even to its owner), a deleted one, a cover of another source.
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, SourceID: source, Visibility: VisibilityFollowers})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, SourceID: source, Visibility: VisibilityLink})
	testInsertPublication(t, pool, testPublication{OwnerID: me, SourceID: source, Status: StatusPreparing})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, SourceID: source, Deleted: true})
	testInsertPublication(t, pool, testPublication{OwnerID: stranger, SourceID: other})

	fetch := func(viewer string) func(*TimeCursor) ([]PublicationSummary, error) {
		return func(cur *TimeCursor) ([]PublicationSummary, error) {
			return store.ListCovers(ctx, source, viewer, cur, 2)
		}
	}
	assertOrder(t, "ben", allTimePages(t, fetch(me), 2), []string{c1, c2, c3, c4})
	assertOrder(t, "yabanci izleyici", allTimePages(t, fetch(uuid.NewString()), 2), []string{c1, c4})

	// The source summary is filled in on covers.
	items, err := store.ListCovers(ctx, source, me, nil, 1)
	if err != nil || items[0].Source == nil || items[0].Source.PublicationID != source || items[0].Source.Deleted {
		t.Fatalf("kaynak ozeti = %+v, %v", items, err)
	}
}

func TestPostgresStore_ListStems(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	id := testInsertPublication(t, pool, testPublication{OwnerID: uuid.NewString(), BackingEnabled: true})

	empty, err := store.ListStems(ctx, id)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("bos liste = %#v, %v", empty, err)
	}
	want := []StemRecord{{ID: "drums", Instrument: "Davul", Duration: 12.5, Key: "sosyal/" + id + "/stems/drums.mp3"}}
	if _, err := pool.Exec(ctx, `UPDATE sosyal.yayin SET stem_listesi = $2 WHERE id = $1`, id, want); err != nil {
		t.Fatal(err)
	}
	stems, err := store.ListStems(ctx, id)
	if err != nil || len(stems) != 1 || stems[0] != want[0] {
		t.Fatalf("stemler = %+v, %v", stems, err)
	}
	if _, err := store.ListStems(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan yayin: %v", err)
	}
}

// A cover by an owner with no profile is not listed (summary-profile join).
func TestPostgresStore_ListCoversHidesOwnerWithoutProfile(t *testing.T) {
	pool, store, me, _, stranger := setupFeed(t)
	ctx := context.Background()
	source := testInsertPublication(t, pool, testPublication{OwnerID: stranger, BackingEnabled: true})
	visible := testInsertPublication(t, pool, testPublication{OwnerID: stranger, SourceID: source})
	testInsertPublication(t, pool, testPublication{OwnerID: uuid.NewString(), SourceID: source})

	items, err := store.ListCovers(ctx, source, me, nil, 10)
	if err != nil || len(items) != 1 || items[0].ID != visible {
		t.Fatalf("coverlar = %+v, %v; beklenen yalnizca %s", items, err, visible)
	}
}
