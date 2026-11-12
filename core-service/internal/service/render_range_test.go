package service

import (
	"context"
	"errors"
	"testing"

	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

type fakeRangeReader struct {
	rec   *model.VocalRangeRecord
	err   error
	calls int
}

func (f *fakeRangeReader) GetVocalRange(context.Context, string) (*model.VocalRangeRecord, error) {
	f.calls++
	return f.rec, f.err
}

func TestStartRender_UsesStoredRangeWhenRequestHasNone(t *testing.T) {
	liveRedis(t)
	reader := &fakeRangeReader{rec: &model.VocalRangeRecord{Low: 45, High: 64, ComfortCenter: 55}}
	s := NewRenderService(jobs.NewMemoryStore(nil), creditAsynqClient(t), nil, testR2Cfg()).
		WithCredits(&credit.FakeSpender{Bal: 500}).WithVocalRanges(reader)
	resp, err := s.StartRender(context.Background(), "kullanici-a", vocalRenderRequest(model.StemModeMix))
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	p := storedPayload(t, resp.JobID)
	if p.Vocal == nil || p.Vocal.Range == nil || *p.Vocal.Range != (model.VocalRange{Low: 45, High: 64}) {
		t.Errorf("range = %+v, want 45..64", p.Vocal)
	}
}

func TestStartRender_RequestRangeWins(t *testing.T) {
	liveRedis(t)
	reader := &fakeRangeReader{rec: &model.VocalRangeRecord{Low: 45, High: 64}}
	s := NewRenderService(jobs.NewMemoryStore(nil), creditAsynqClient(t), nil, testR2Cfg()).
		WithCredits(&credit.FakeSpender{Bal: 500}).WithVocalRanges(reader)
	req := vocalRenderRequest(model.StemModeMix)
	req.Vocal.Range = &model.VocalRange{Low: 50, High: 70}
	resp, err := s.StartRender(context.Background(), "kullanici-a", req)
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	p := storedPayload(t, resp.JobID)
	if p.Vocal.Range == nil || *p.Vocal.Range != (model.VocalRange{Low: 50, High: 70}) {
		t.Errorf("range = %+v, want 50..70", p.Vocal.Range)
	}
	if reader.calls != 0 {
		t.Errorf("reader called %d times, want 0", reader.calls)
	}
}

func TestStartRender_RangeReadErrorDoesNotBlock(t *testing.T) {
	liveRedis(t)
	reader := &fakeRangeReader{err: errors.New("db down")}
	fake := &credit.FakeSpender{Bal: 500}
	s := NewRenderService(jobs.NewMemoryStore(nil), creditAsynqClient(t), nil, testR2Cfg()).
		WithCredits(fake).WithVocalRanges(reader)
	resp, err := s.StartRender(context.Background(), "kullanici-a", vocalRenderRequest(model.StemModeMix))
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	if len(fake.Spends) != 1 {
		t.Errorf("spends = %d, want 1", len(fake.Spends))
	}
	if p := storedPayload(t, resp.JobID); p.Vocal.Range != nil {
		t.Errorf("range = %+v, want nil", p.Vocal.Range)
	}
}
