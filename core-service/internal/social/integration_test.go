package social

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// jsonFields marshals the value to JSON and returns its top-level fields.
func jsonFields(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json: %v", err)
	}
	return m
}

// Publication lifecycle with the real Service + PostgresStore: profile, publish,
// preparation result, detail, feed, like, patch, delete. Instead of the job (worker),
// store.Publish is called directly.
func TestIntegration_PublicationLifecycle(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	q := &fakeQueue{}
	s := NewService(store, q, nil, testR2())
	ctx := context.Background()
	owner, listener := uuid.NewString(), uuid.NewString()

	if _, err := s.CreatePublication(ctx, owner, PublicationRequest{}); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("publication without a profile: %v", err)
	}
	if _, err := s.SaveProfile(ctx, owner, ProfileInput{Username: "sahip", DisplayName: "Sahip"}); err != nil {
		t.Fatalf("profile: %v", err)
	}
	if _, err := s.SaveProfile(ctx, listener, ProfileInput{Username: "dinleyici", DisplayName: "D"}); err != nil {
		t.Fatalf("profile: %v", err)
	}

	req := validRequest()
	req.MasterURL = "masters/" + owner + "/p1/m1.wav"
	req.Stems[0].URL = "stems/" + owner + "/p1/drums.mp3"
	resp, err := s.CreatePublication(ctx, owner, req)
	if err != nil {
		t.Fatalf("CreatePublication: %v", err)
	}
	if len(q.tasks) != 1 {
		t.Fatalf("the prepare job must be enqueued")
	}

	// While preparing, visible only to its owner.
	if pub, err := s.Publication(ctx, owner, resp.ID); err != nil || pub.Status != StatusPreparing {
		t.Fatalf("the owner must see the publication in preparation: %+v, %v", pub, err)
	}
	if _, err := s.Publication(ctx, listener, resp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a listener must not see the publication in preparation: %v", err)
	}

	prep, err := store.GetPreparation(ctx, resp.ID)
	if err != nil || prep.Preparation.MasterKey != req.MasterURL {
		t.Fatalf("preparation = %+v, %v", prep, err)
	}
	master, _ := MasterDestKey(resp.ID, prep.Preparation.MasterKey)
	waveform := make([]float64, 64)
	if err := store.Publish(ctx, resp.ID, PreparationResult{MasterKey: master, Waveform: waveform}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	detail, err := s.PublicationBySlug(ctx, listener, resp.Slug)
	if err != nil {
		t.Fatalf("PublicationBySlug: %v", err)
	}
	m := jsonFields(t, detail)
	for _, field := range []string{"id", "slug", "baslik", "sahip", "tur", "bpm", "ton", "sureSn", "dalgaFormu",
		"begeniSayisi", "yorumSayisi", "coverSayisi", "begendimMi", "altyapiAcik", "kaynak", "olusturma",
		"aciklama", "yorumAcik", "sozler", "yapi", "durum", "benimMi", "gorunurluk"} {
		if _, ok := m[field]; !ok {
			t.Errorf("PublicationDetail.%s is missing", field)
		}
	}
	if m["kaynak"] != nil || m["durum"] != "yayinda" || m["benimMi"] != false || len(m["dalgaFormu"].([]any)) != 64 {
		t.Fatalf("detail = %v", m)
	}
	if ownerJSON := m["sahip"].(map[string]any); ownerJSON["kullaniciAdi"] != "sahip" {
		t.Fatalf("owner = %v", ownerJSON)
	}

	feed, err := s.Feed(ctx, listener, TabForYou, "", 20)
	if err != nil || len(feed.Items) != 1 || feed.NextCursor != nil {
		t.Fatalf("for you = %+v, %v", feed, err)
	}
	if own, _ := s.Feed(ctx, owner, TabForYou, "", 20); len(own.Items) != 0 {
		t.Fatalf("my own publication must not appear in for-you: %+v", own.Items)
	}
	if b, err := s.SetLike(ctx, listener, resp.ID, true); err != nil || b.LikeCount != 1 || !b.LikedByMe {
		t.Fatalf("like = %+v, %v", b, err)
	}

	files, err := s.Files(ctx, listener, resp.ID)
	if err != nil || files.Master.URL == "" {
		t.Fatalf("files = %+v, %v", files, err)
	}

	followersOnly := VisibilityFollowers
	if _, err := s.UpdatePublication(ctx, owner, resp.ID, PublicationPatch{Visibility: &followersOnly}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if _, err := s.Publication(ctx, listener, resp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a followers-only publication must not be visible to a non-follower: %v", err)
	}
	p, err := s.Profile(ctx, listener, "sahip")
	if err != nil || p.Stats.Songs != 0 {
		t.Fatalf("the song counter must be 0 for a non-follower: %+v, %v", p, err)
	}
	me, _ := s.MyProfile(ctx, owner)
	if me.Stats.Songs != 1 || !me.IsMe {
		t.Fatalf("owner counter = %+v", me)
	}

	if err := s.DeletePublication(ctx, owner, resp.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeletePublication(ctx, owner, resp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if len(q.tasks) != 2 || q.tasks[1].Type() != TaskTypeDeleteFiles {
		t.Fatalf("the file delete job must be enqueued: %d", len(q.tasks))
	}
}

func TestIntegration_UsernameAvailability(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	s := NewService(store, &fakeQueue{}, nil, testR2())
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	for i, username := range []string{"ali", "ali2"} {
		id := []string{a, b}[i]
		if _, err := s.SaveProfile(ctx, id, ProfileInput{Username: username, DisplayName: "x"}); err != nil {
			t.Fatalf("profile: %v", err)
		}
	}
	avail, err := s.CheckUsername(ctx, uuid.NewString(), "ali")
	if err != nil || avail.Available || avail.Suggestion == nil || *avail.Suggestion != "ali3" {
		t.Fatalf("availability = %+v, %v", avail, err)
	}
	if _, err := s.SaveProfile(ctx, b, ProfileInput{Username: "ali", DisplayName: "x"}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("taken username: %v", err)
	}
}
