package social

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresStore_UpsertAndGetProfile(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()
	id := uuid.NewString()

	if _, err := store.GetProfile(ctx, id); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("want ErrNoProfile when there is no profile: %v", err)
	}
	rec, err := store.UpsertProfile(ctx, ProfileRecord{UserID: id, Username: "ali", DisplayName: "Ali", Bio: "b"})
	if err != nil || rec.Username != "ali" || rec.CreatedAt.IsZero() {
		t.Fatalf("save = %+v, %v", rec, err)
	}
	// The second write updates (PUT both creates and updates).
	if _, err := store.UpsertProfile(ctx, ProfileRecord{UserID: id, Username: "ali2", DisplayName: "Ali Veli", Bio: ""}); err != nil {
		t.Fatalf("update: %v", err)
	}
	rec, err = store.GetProfile(ctx, id)
	if err != nil || rec.Username != "ali2" || rec.DisplayName != "Ali Veli" || rec.Bio != "" {
		t.Fatalf("get = %+v, %v", rec, err)
	}
	if _, err := store.GetProfileByUsername(ctx, "ali"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old username must not be found: %v", err)
	}
	if rec, err := store.GetProfileByUsername(ctx, "ali2"); err != nil || rec.UserID != id {
		t.Fatalf("get by username = %+v, %v", rec, err)
	}
}

func TestPostgresStore_UsernameTaken(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, a, "ayse")
	_, err := store.UpsertProfile(ctx, ProfileRecord{UserID: b, Username: "ayse", DisplayName: "Ayse"})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("want ErrUsernameTaken: %v", err)
	}
	owners, err := store.UsernameOwners(ctx, []string{"ayse", "ayse2", "yok"})
	if err != nil || len(owners) != 1 || owners["ayse"] != a {
		t.Fatalf("UsernameOwners = %v, %v", owners, err)
	}
	if empty, err := store.UsernameOwners(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty list = %v, %v", empty, err)
	}
}

// The song/cover counter applies the visibility rule and status=published.
func TestPostgresStore_ProfileCounts(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner, follower, stranger := uuid.NewString(), uuid.NewString(), uuid.NewString()

	original := testInsertPublication(t, pool, testPublication{OwnerID: owner, Visibility: VisibilityEveryone})
	testInsertPublication(t, pool, testPublication{OwnerID: owner, Visibility: VisibilityFollowers})
	testInsertPublication(t, pool, testPublication{OwnerID: owner, Visibility: VisibilityLink})
	testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusPreparing})
	testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusFailed})
	testInsertPublication(t, pool, testPublication{OwnerID: owner, Deleted: true})
	testInsertPublication(t, pool, testPublication{OwnerID: owner, SourceID: original})
	testInsertFollow(t, pool, follower, owner)
	testInsertFollow(t, pool, owner, stranger)

	cases := []struct {
		name         string
		viewer       string
		songs        int
		cover        int
		followedByMe bool
	}{
		{"owner", owner, 3, 1, false},
		{"follower", follower, 2, 1, true},
		{"stranger", stranger, 1, 1, false},
	}
	for _, tc := range cases {
		s, err := store.ProfileCounts(ctx, owner, tc.viewer)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if s.Stats.Songs != tc.songs || s.Stats.Covers != tc.cover || s.FollowedByMe != tc.followedByMe {
			t.Errorf("%s: %+v", tc.name, s)
		}
		if s.Stats.Followers != 1 || s.Stats.Following != 1 {
			t.Errorf("%s follow counters: %+v", tc.name, s.Stats)
		}
	}
}
