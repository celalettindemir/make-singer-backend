package social

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testInsertLikeAt writes a like at the given time.
func testInsertLikeAt(t *testing.T, pool *pgxpool.Pool, user, pub string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id, olusturma) VALUES ($1, $2, $3)`,
		user, pub, at); err != nil {
		t.Fatalf("begeni eklenemedi: %v", err)
	}
}

func TestPostgresStore_FollowIdempotent(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()

	for i := 0; i < 2; i++ {
		if err := store.Follow(ctx, a, b); err != nil {
			t.Fatalf("TakipEt %d: %v", i, err)
		}
	}
	if err := store.Follow(ctx, c, b); err != nil {
		t.Fatal(err)
	}
	status, err := store.FollowStatus(ctx, a, b)
	if err != nil || status.Followers != 2 || !status.FollowedByMe {
		t.Fatalf("durum = %+v, %v", status, err)
	}
	reverse, err := store.FollowStatus(ctx, b, a)
	if err != nil || reverse.Followers != 0 || reverse.FollowedByMe {
		t.Fatalf("ters yon = %+v, %v", reverse, err)
	}
	for i := 0; i < 2; i++ {
		if err := store.Unfollow(ctx, a, b); err != nil {
			t.Fatalf("TakibiBirak %d: %v", i, err)
		}
	}
	status, err = store.FollowStatus(ctx, a, b)
	if err != nil || status.Followers != 1 || status.FollowedByMe {
		t.Fatalf("birakmadan sonra = %+v, %v", status, err)
	}
	if err := store.Follow(ctx, a, a); err == nil {
		t.Fatal("kendini takip CHECK kisitina takilmali")
	}
}

func TestPostgresStore_Suggestions(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	now := time.Now()
	day := 24 * time.Hour

	profile := func(name string) string {
		id := uuid.NewString()
		testInsertProfile(t, store, id, name)
		return id
	}
	addLike := func(pub string, n int, at time.Time) {
		for i := 0; i < n; i++ {
			testInsertLikeAt(t, pool, uuid.NewString(), pub, at)
		}
	}

	me := profile("ben")
	followee := profile("takip_edilen")
	mostLiked := profile("cok_begenilen")
	oldLikes := profile("eski_begeniler")
	zeroLikes := profile("begenisiz")
	withFollowers := profile("takipcili")
	linkOnly := profile("yalniz_link")
	deletedPub := profile("silinmis_yayin")
	preparing := profile("hazirlanan")
	profile("yayinsiz")

	testInsertFollow(t, pool, me, followee)
	addLike(testInsertPublication(t, pool, testPublication{OwnerID: followee}), 9, now)
	addLike(testInsertPublication(t, pool, testPublication{OwnerID: me}), 9, now)

	row := testInsertPublication(t, pool, testPublication{OwnerID: mostLiked, CreatedAt: now.Add(-40 * day)})
	addLike(row, 5, now.Add(-29*day))
	testInsertLikeAt(t, pool, mostLiked, row, now) // their own like is not counted

	row = testInsertPublication(t, pool, testPublication{OwnerID: oldLikes, CreatedAt: now.Add(-40 * day)})
	addLike(row, 10, now.Add(-31*day))
	addLike(row, 1, now)

	testInsertPublication(t, pool, testPublication{OwnerID: zeroLikes, CreatedAt: now.Add(-time.Hour)})
	addLike(testInsertPublication(t, pool, testPublication{OwnerID: withFollowers, Visibility: VisibilityFollowers}), 3, now)
	addLike(testInsertPublication(t, pool, testPublication{OwnerID: linkOnly, Visibility: VisibilityLink}), 20, now)
	addLike(testInsertPublication(t, pool, testPublication{OwnerID: deletedPub, Deleted: true}), 20, now)
	addLike(testInsertPublication(t, pool, testPublication{OwnerID: preparing, Status: StatusPreparing}), 20, now)

	suggestions, err := store.Suggestions(ctx, me, 10)
	if err != nil {
		t.Fatalf("Oneriler: %v", err)
	}
	var names []string
	for _, out := range suggestions {
		names = append(names, out.Username)
	}
	assertOrder(t, "oneriler", names, []string{"cok_begenilen", "takipcili", "eski_begeniler", "begenisiz"})

	first, err := store.Suggestions(ctx, me, 2)
	if err != nil || len(first) != 2 || first[0].Username != "cok_begenilen" {
		t.Fatalf("limit 2 = %+v, %v", first, err)
	}
}

func TestPostgresStore_SuggestionsTieBreaksOnNewestPublication(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	now := time.Now()
	me := uuid.NewString()
	var want []string
	for i := 0; i < 12; i++ {
		id, name := uuid.NewString(), fmt.Sprintf("yayinci_%02d", i)
		testInsertProfile(t, store, id, name)
		testInsertPublication(t, pool, testPublication{OwnerID: id, CreatedAt: now.Add(-time.Duration(i) * time.Minute)})
		if i < 10 {
			want = append(want, name)
		}
	}
	suggestions, err := store.Suggestions(context.Background(), me, 10)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, out := range suggestions {
		names = append(names, out.Username)
	}
	assertOrder(t, "esitlik", names, want)
}
