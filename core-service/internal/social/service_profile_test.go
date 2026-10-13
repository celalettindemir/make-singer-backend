package social

import (
	"context"
	"errors"
	"testing"
)

const (
	userA = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	userB = "7f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func TestService_MyProfileWithoutProfileReturnsNoProfile(t *testing.T) {
	s, _ := newTestService(newFakeStore())
	if _, err := s.MyProfile(context.Background(), userA); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("want ErrNoProfile: %v", err)
	}
}

func TestService_InvalidIdentity(t *testing.T) {
	s, _ := newTestService(newFakeStore())
	if _, err := s.MyProfile(context.Background(), "kullanici-a"); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("want ErrInvalidIdentity: %v", err)
	}
}

func TestService_SaveProfileAndMyProfile(t *testing.T) {
	store := newFakeStore()
	store.counts = ProfileCounts{Stats: Stats{Songs: 2, Followers: 5}}
	s, _ := newTestService(store)
	p, err := s.SaveProfile(context.Background(), userA, ProfileInput{Username: "ali", DisplayName: " Ali ", Bio: "selam"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if p.Username != "ali" || p.DisplayName != "Ali" || !p.IsMe || p.FollowedByMe || p.Stats.Songs != 2 {
		t.Fatalf("profile = %+v", p)
	}
	if p.AvatarColor != AvatarColor(userA) {
		t.Fatalf("avatar color = %d", p.AvatarColor)
	}
	me, err := s.MyProfile(context.Background(), userA)
	if err != nil || me.Username != "ali" {
		t.Fatalf("me = %+v, %v", me, err)
	}
}

func TestService_SaveProfileErrors(t *testing.T) {
	store := newFakeStore()
	s, _ := newTestService(store)
	ctx := context.Background()
	if _, err := s.SaveProfile(ctx, userA, ProfileInput{Username: ".ali", DisplayName: "A"}); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("invalid username: %v", err)
	}
	if _, err := s.SaveProfile(ctx, userA, ProfileInput{Username: "ali", DisplayName: ""}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty display name: %v", err)
	}
	if _, err := s.SaveProfile(ctx, userA, ProfileInput{Username: "ali", DisplayName: "A"}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := s.SaveProfile(ctx, userB, ProfileInput{Username: "ali", DisplayName: "B"}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("taken username: %v", err)
	}
}

func TestService_CheckUsername(t *testing.T) {
	store := newFakeStore()
	store.profiles[userA] = &ProfileRecord{UserID: userA, Username: "ali"}
	store.profiles[userB] = &ProfileRecord{UserID: userB, Username: "ali2"}
	s, _ := newTestService(store)
	ctx := context.Background()

	empty, err := s.CheckUsername(ctx, userB, "veli")
	if err != nil || !empty.Available || empty.Suggestion != nil {
		t.Fatalf("empty username = %+v, %v", empty, err)
	}
	own, err := s.CheckUsername(ctx, userA, "ali")
	if err != nil || !own.Available {
		t.Fatalf("your own username must be available: %+v, %v", own, err)
	}
	// ali (A) and ali2 (B) are taken: the suggestion for C is ali3.
	taken, err := s.CheckUsername(ctx, "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", "ali")
	if err != nil || taken.Available || taken.Suggestion == nil || *taken.Suggestion != "ali3" {
		t.Fatalf("taken username = %+v, %v", taken, err)
	}
	// For B, ali2 is their own name, so the suggestion is ali2.
	forB, err := s.CheckUsername(ctx, userB, "ali")
	if err != nil || forB.Suggestion == nil || *forB.Suggestion != "ali2" {
		t.Fatalf("suggestion for B = %+v, %v", forB, err)
	}
	if _, err := s.CheckUsername(ctx, userA, "A!"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("invalid username: %v", err)
	}
}

func TestService_Profile(t *testing.T) {
	store := newFakeStore()
	store.profiles[userA] = &ProfileRecord{UserID: userA, Username: "ali", DisplayName: "Ali"}
	store.counts = ProfileCounts{FollowedByMe: true}
	s, _ := newTestService(store)
	ctx := context.Background()
	p, err := s.Profile(ctx, userB, "ali")
	if err != nil || p.IsMe || !p.FollowedByMe {
		t.Fatalf("profile = %+v, %v", p, err)
	}
	if _, err := s.Profile(ctx, userB, "yok"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile: %v", err)
	}
	if _, err := s.Profile(ctx, userB, "X"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed username: %v", err)
	}
}
