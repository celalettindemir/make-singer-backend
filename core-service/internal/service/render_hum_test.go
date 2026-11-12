package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

const testHumID = "0b8f2c1e-1111-4222-8333-444455556666"

// existsStorage is a fakeStorage that can answer Exists.
type existsStorage struct {
	fakeStorage
	exists bool
	err    error
	keys   []string
}

func (e *existsStorage) Exists(_ context.Context, key string) (bool, error) {
	e.keys = append(e.keys, key)
	return e.exists, e.err
}

func humRenderRequest(withVocal bool) *model.RenderStartRequest {
	req := creditRenderRequest(model.StemModeMix)
	req.Hum = &model.HumRequest{HumID: testHumID, Fidelity: model.HumFree}
	if withVocal {
		req.Vocal = &model.VocalRequest{
			Voice:    model.VoiceFemale,
			Sections: []model.GuideSection{{ID: "s1", Type: "verse", Lines: []string{"gece yolunda"}}},
		}
	}
	return req
}

func TestStartRender_HumMissing_NotFoundNoDebit(t *testing.T) {
	repo := jobs.NewMemoryStore(nil)
	fake := &credit.FakeSpender{Bal: 100, Pro: true}
	st := &existsStorage{exists: false}
	s := NewRenderService(repo, nil, st, testR2Cfg()).WithCredits(fake)
	_, err := s.StartRender(context.Background(), "u1", humRenderRequest(true))
	if !errors.Is(err, ErrHumNotFound) {
		t.Fatalf("err = %v, want ErrHumNotFound", err)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %d, want 0", len(fake.Spends))
	}
	if list, _ := repo.List(context.Background(), "u1", time.Time{}, 10); len(list) != 0 {
		t.Errorf("jobs = %d, want 0", len(list))
	}
	if len(st.keys) != 1 || st.keys[0] != "hums/u1/p1/"+testHumID+".mp3" {
		t.Errorf("checked keys = %v", st.keys)
	}
}

func TestStartRender_HumWithoutVocal_Invalid(t *testing.T) {
	fake := &credit.FakeSpender{Bal: 100, Pro: true}
	st := &existsStorage{exists: true}
	s := NewRenderService(jobs.NewMemoryStore(nil), nil, st, testR2Cfg()).WithCredits(fake)
	_, err := s.StartRender(context.Background(), "u1", humRenderRequest(false))
	if !errors.Is(err, ErrHumNeedsVocal) {
		t.Fatalf("err = %v, want ErrHumNeedsVocal", err)
	}
	if len(st.keys) != 0 || len(fake.Spends) != 0 {
		t.Errorf("Exists calls %v, spends %d; want none", st.keys, len(fake.Spends))
	}
}

func TestStartRender_HumCheckError_NoDebit(t *testing.T) {
	fake := &credit.FakeSpender{Bal: 100, Pro: true}
	st := &existsStorage{err: errors.New("r2 down")}
	s := NewRenderService(jobs.NewMemoryStore(nil), nil, st, testR2Cfg()).WithCredits(fake)
	_, err := s.StartRender(context.Background(), "u1", humRenderRequest(true))
	if err == nil || errors.Is(err, ErrHumNotFound) {
		t.Fatalf("err = %v, want a non-NotFound error", err)
	}
	if len(fake.Spends) != 0 {
		t.Errorf("spends = %d, want 0", len(fake.Spends))
	}
}

func TestStartRender_Hum_PayloadCarriesKeyAndFidelity(t *testing.T) {
	liveRedis(t)
	repo := jobs.NewMemoryStore(nil)
	fake := &credit.FakeSpender{Bal: 100, Pro: true}
	st := &existsStorage{exists: true}
	s := NewRenderService(repo, creditAsynqClient(t), st, testR2Cfg()).WithCredits(fake)
	if _, err := s.StartRender(context.Background(), "u1", humRenderRequest(true)); err != nil {
		t.Fatal(err)
	}
	list, _ := repo.List(context.Background(), "u1", time.Time{}, 10)
	if len(list) != 1 {
		t.Fatalf("jobs = %d", len(list))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(list[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if got, want := string(payload["hum"]), `{"key":"hums/u1/p1/`+testHumID+`.mp3","fidelity":"free"}`; got != want {
		t.Errorf("hum = %s, want %s", got, want)
	}
	if len(fake.Spends) != 1 || fake.Spends[0].Op != RenderOperation(model.StemModeMix) {
		t.Errorf("spends = %+v", fake.Spends)
	}
}
