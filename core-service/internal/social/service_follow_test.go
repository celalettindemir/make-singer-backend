package social

import (
	"context"
	"errors"
	"testing"
)

func followStore() *fakeStore {
	store := storeWithProfile()
	store.profiles[userB] = &ProfileRecord{UserID: userB, Username: "berk", DisplayName: "Berk"}
	return store
}

func TestService_FollowAndUnfollow(t *testing.T) {
	store := followStore()
	s, _ := newTestService(store)
	ctx := context.Background()

	for i := 0; i < 2; i++ { // repeating is safe
		status, err := s.SetFollow(ctx, userA, "berk", true)
		if err != nil || status.Followers != 1 || !status.FollowedByMe {
			t.Fatalf("takip %d = %+v, %v", i, status, err)
		}
	}
	if !store.follows[[2]string{userA, userB}] {
		t.Fatal("takip A -> B yazilmali")
	}
	for i := 0; i < 2; i++ {
		status, err := s.SetFollow(ctx, userA, "berk", false)
		if err != nil || status.Followers != 0 || status.FollowedByMe {
			t.Fatalf("birakma %d = %+v, %v", i, status, err)
		}
	}
}

func TestService_FollowErrors(t *testing.T) {
	store := followStore()
	s, _ := newTestService(store)
	ctx := context.Background()
	cases := []struct {
		name, user string
		follow     bool
		want       error
	}{
		{"ali", userA, true, ErrSelfFollow},
		{"ali", userA, false, ErrSelfFollow},
		{"yok_biri", userA, true, ErrNotFound},
		{".kotu", userA, true, ErrNotFound},
		{"berk", "uuid-degil", true, ErrInvalidIdentity},
	}
	for _, c := range cases {
		if _, err := s.SetFollow(ctx, c.user, c.name, c.follow); !errors.Is(err, c.want) {
			t.Errorf("%s/%v: %v, beklenen %v", c.name, c.follow, err, c.want)
		}
	}
	if len(store.follows) != 0 {
		t.Fatalf("hatali isteklerde takip yazilmamali: %v", store.follows)
	}
}

func TestService_SuggestionsSummaryAndLimit(t *testing.T) {
	store := followStore()
	s, _ := newTestService(store)
	ctx := context.Background()

	empty, err := s.Suggestions(ctx, userA)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("bos oneriler [] olmali: %#v, %v", empty, err)
	}
	if store.lastSuggestion != [2]any{userA, 10} {
		t.Fatalf("depo cagrisi = %v, beklenen {A, 10}", store.lastSuggestion)
	}

	store.suggestionRecords = []ProfileRecord{*store.profiles[userB]}
	suggested, err := s.Suggestions(ctx, userA)
	if err != nil || len(suggested) != 1 {
		t.Fatalf("oneriler = %+v, %v", suggested, err)
	}
	if suggested[0] != (ProfileSummary{Username: "berk", DisplayName: "Berk", AvatarColor: AvatarColor(userB)}) {
		t.Fatalf("ozet = %+v", suggested[0])
	}
}
