package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
)

// TestMasterService_Preview_ExpiresAtConfigTTL verifies that
// MasterPreviewResponse.ExpiresAt comes from r2Cfg.PresignTTL. r2Cfg.PresignTTL
// is given a value (2 hours) DIFFERENT from the known 1-hour default: if the
// code reverts to the fixed "1 hour" value this test breaks.
func TestMasterService_Preview_ExpiresAtConfigTTL(t *testing.T) {
	r2Cfg := &config.R2Config{PresignTTL: 2 * time.Hour}
	s := NewMasterService(nil, nil, nil, r2Cfg)

	before := time.Now()
	resp, err := s.Preview(context.Background(), "kullanici-a", &model.MasterPreviewRequest{})
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}

	if resp.ExpiresAt == nil {
		t.Fatal("ExpiresAt is nil, it must be set for a timed link")
	}

	diff := resp.ExpiresAt.Sub(before)
	// If it reverted to the fixed 1-hour value the difference would be ~1h; it
	// must be close to the 2h in the config (with some tolerance).
	if diff < 90*time.Minute || diff > 130*time.Minute {
		t.Errorf("ExpiresAt difference = %v, it must be close to the 2h PresignTTL in the config (a fixed 1h may have been used)", diff)
	}
}

func masterFinalRequest(t *testing.T, user string) *model.MasterFinalRequest {
	t.Helper()
	key, err := client.UserKey("stems", user, "p1", "a.mp3")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return &model.MasterFinalRequest{
		ProjectID: "p1",
		Profile:   model.MasterProfile("clean"),
		StemURLs:  []string{client.UnsignedURL(key, testR2Cfg())},
	}
}

func TestMasterService_FinalDebitsBeforeSave(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Bal: 81}
	s := NewMasterService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)

	resp, err := s.StartFinal(context.Background(), "kullanici-a", masterFinalRequest(t, "kullanici-a"))
	if err != nil {
		t.Fatalf("StartFinal: %v", err)
	}
	if len(fake.Spends) != 1 {
		t.Fatalf("spends = %d, want 1", len(fake.Spends))
	}
	got := fake.Spends[0]
	if got.Op != credit.OpMaster || got.ID != resp.JobID || got.User != "kullanici-a" || got.Project != "p1" {
		t.Errorf("spend = %+v, want master ref %s project p1", got, resp.JobID)
	}
	if resp.Balance != 81 {
		t.Errorf("balance = %v, want 81", resp.Balance)
	}
	if len(fake.Refunds) != 0 {
		t.Errorf("unexpected refund %+v", fake.Refunds)
	}

	// Insufficient: no job record, no enqueue (nil asynq client would panic).
	n := jobKeyCount(t, rdb)
	fake2 := &credit.FakeSpender{IsEnforced: true, Insufficient: true, Required: 3, Bal: 1}
	s = NewMasterService(rdb, nil, nil, testR2Cfg()).WithCredits(fake2)
	_, err = s.StartFinal(context.Background(), "kullanici-a", masterFinalRequest(t, "kullanici-a"))
	var insufficient *credit.InsufficientError
	if !errors.As(err, &insufficient) || insufficient.Required != 3 || insufficient.Balance != 1 {
		t.Fatalf("err = %v, want InsufficientError{3,1}", err)
	}
	if after := jobKeyCount(t, rdb); after != n {
		t.Errorf("job records = %d, want %d", after, n)
	}
}

func TestMasterService_NoCreditsBalanceIsZero(t *testing.T) {
	rdb := liveRedis(t)
	s := NewMasterService(rdb, creditAsynqClient(t), nil, testR2Cfg())
	resp, err := s.StartFinal(context.Background(), "kullanici-a", masterFinalRequest(t, "kullanici-a"))
	if err != nil || resp.Balance != 0 {
		t.Fatalf("resp=%+v err=%v, want zero balance", resp, err)
	}
}

func TestMasterService_RefundsWhenEnqueueFails(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true}
	closed := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	_ = closed.Close()
	s := NewMasterService(rdb, closed, nil, testR2Cfg()).WithCredits(fake)

	if _, err := s.StartFinal(context.Background(), "kullanici-a", masterFinalRequest(t, "kullanici-a")); err == nil {
		t.Fatal("want an enqueue error")
	}
	if len(fake.Spends) != 1 || len(fake.Refunds) != 1 {
		t.Fatalf("spends=%d refunds=%d, want 1 and 1", len(fake.Spends), len(fake.Refunds))
	}
	if r := fake.Refunds[0]; r.Op != credit.OpMaster || r.ID != fake.Spends[0].ID {
		t.Errorf("refund = %+v, want master with ref %s", r, fake.Spends[0].ID)
	}
}

func masterKeyURL(t *testing.T, kind, user, file string) string {
	t.Helper()
	key, err := client.UserKey(kind, user, "p1", file)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return client.UnsignedURL(key, testR2Cfg())
}

func TestMaster_GuidesKeyAsStem_NeedsPlus(t *testing.T) {
	rdb := liveRedis(t)
	for _, enforced := range []bool{true, false} {
		fake := &credit.FakeSpender{IsEnforced: enforced}
		s := NewMasterService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
		req := masterFinalRequest(t, "kullanici-a")
		req.StemURLs = append(req.StemURLs, masterKeyURL(t, "guides", "kullanici-a", "ai.mp3"))
		n := jobKeyCount(t, rdb)
		if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, ErrPlusRequired) {
			t.Fatalf("enforced=%v err = %v, want ErrPlusRequired", enforced, err)
		}
		if len(fake.Spends) != 0 || jobKeyCount(t, rdb) != n {
			t.Errorf("enforced=%v: spends=%v, job records changed", enforced, fake.Spends)
		}
	}
}

func TestMaster_GuidesKeyAsTake_NeedsPlus(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true}
	s := NewMasterService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	req := masterFinalRequest(t, "kullanici-a")
	req.VocalTakes = []model.VocalTake{{SectionID: "s1", TakeID: "t1", FileURL: masterKeyURL(t, "guides", "kullanici-a", "ai.mp3")}}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, ErrPlusRequired) {
		t.Fatalf("err = %v, want ErrPlusRequired", err)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %v, want none", fake.Spends)
	}
}

func TestMaster_PlanReadError_NoSpend(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Err: errors.New("db down")}
	s := NewMasterService(rdb, nil, nil, testR2Cfg()).WithCredits(fake)
	req := masterFinalRequest(t, "kullanici-a")
	req.StemURLs = append(req.StemURLs, masterKeyURL(t, "guides", "kullanici-a", "ai.mp3"))
	err := func() error { _, err := s.StartFinal(context.Background(), "kullanici-a", req); return err }()
	if err == nil || errors.Is(err, ErrPlusRequired) {
		t.Fatalf("err = %v, want a plain plan read error", err)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %v, want none", fake.Spends)
	}
}

func TestMaster_GuidesKey_PlusUser_OK(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true, Plus: true}
	s := NewMasterService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	req := masterFinalRequest(t, "kullanici-a")
	req.StemURLs = append(req.StemURLs, masterKeyURL(t, "guides", "kullanici-a", "ai.mp3"))
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartFinal: %v", err)
	}
	if len(fake.Spends) != 1 {
		t.Errorf("spends = %d, want 1", len(fake.Spends))
	}
}

func TestMaster_OwnTakes_NoPlan(t *testing.T) {
	rdb := liveRedis(t)
	fake := &credit.FakeSpender{IsEnforced: true}
	s := NewMasterService(rdb, creditAsynqClient(t), nil, testR2Cfg()).WithCredits(fake)
	req := masterFinalRequest(t, "kullanici-a")
	req.VocalTakes = []model.VocalTake{{SectionID: "s1", TakeID: "t1", FileURL: masterKeyURL(t, "vocals", "kullanici-a", "t.wav")}}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartFinal: %v", err)
	}
}

func TestMasterPreview_GuidesKey_NeedsPlus(t *testing.T) {
	fake := &credit.FakeSpender{}
	s := NewMasterService(nil, nil, nil, testR2Cfg()).WithCredits(fake)
	req := &model.MasterPreviewRequest{StemURLs: []string{masterKeyURL(t, "guides", "kullanici-a", "ai.mp3")}}
	if _, err := s.Preview(context.Background(), "kullanici-a", req); !errors.Is(err, ErrPlusRequired) {
		t.Fatalf("err = %v, want ErrPlusRequired", err)
	}
	plus := &credit.FakeSpender{Plus: true}
	s = NewMasterService(nil, nil, nil, testR2Cfg()).WithCredits(plus)
	if _, err := s.Preview(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("plus Preview: %v", err)
	}
}

func TestUsesAppVocal(t *testing.T) {
	if usesAppVocal([]string{"stems/u/p/a.mp3", "vocals/u/p/t.wav"}) {
		t.Error("own keys must not count")
	}
	if !usesAppVocal([]string{"stems/u/p/a.mp3", "guides/u/p/ai.mp3"}) {
		t.Error("guides key must count")
	}
}
