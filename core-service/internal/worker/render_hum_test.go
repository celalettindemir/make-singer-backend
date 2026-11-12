package worker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
)

// coverSuno is vocalSuno plus UploadCover. GenerateMusic must not be called
// on a hum render.
type coverSuno struct {
	vocalSuno
	t         *testing.T
	coverReq  *client.UploadCoverRequest
	coverErr  error
	coverCall int
}

func (s *coverSuno) GenerateMusic(ctx context.Context, r *client.GenerateMusicRequest) (string, error) {
	s.t.Error("GenerateMusic was called on a hum render")
	return s.vocalSuno.GenerateMusic(ctx, r)
}

func (s *coverSuno) UploadCover(_ context.Context, r *client.UploadCoverRequest) (string, error) {
	s.coverCall++
	s.coverReq = r
	s.calls = append(s.calls, "UploadCover")
	if s.coverErr != nil {
		return "", s.coverErr
	}
	return "c1", nil
}

// signStorage is fakeStorage with a recording GetSignedURL.
type signStorage struct {
	fakeStorage
	keys []string
	ttl  time.Duration
	err  error
}

func (d *signStorage) GetSignedURL(_ context.Context, key string, ttl time.Duration) (string, error) {
	d.keys = append(d.keys, key)
	d.ttl = ttl
	if d.err != nil {
		return "", d.err
	}
	return "https://imzali/h.mp3", nil
}

func humPayload(fid model.HumFidelity) *model.RenderJobPayload {
	p := vocalPayload()
	p.Hum = &model.HumJob{Key: "hums/u/p/h.mp3", Fidelity: fid}
	return p
}

func coverSetup(t *testing.T) (*coverSuno, *signStorage, *RenderWorker) {
	t.Helper()
	base, _, _, w := vocalSetup(t)
	s := &coverSuno{vocalSuno: *base, t: t}
	d := &signStorage{}
	w.music = s
	w.r2Client = d
	w.WithCoverWeights(CoverWeights{AudioFaithful: 0.9, AudioFree: 0.6, StyleFaithful: 0.5, StyleFree: 0.6})
	return s, d, w
}

func TestCover_UsesUploadCoverWithSignedURLAndWeights(t *testing.T) {
	s, d, w := coverSetup(t)
	p := humPayload(model.HumFree)
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if !slices.Equal(d.keys, []string{"hums/u/p/h.mp3"}) || d.ttl < time.Hour {
		t.Errorf("signed keys %v ttl %v", d.keys, d.ttl)
	}
	r := s.coverReq
	if r == nil || r.UploadURL != "https://imzali/h.mp3" || r.AudioWeight != 0.6 || r.StyleWeight != 0.6 ||
		r.Lyrics != client.SunoLyrics(p.Vocal.Sections) || r.VocalGender != "f" {
		t.Errorf("cover request = %+v", r)
	}
	if got := instruments(res.Stems); !slices.Equal(got, []model.Instrument{model.InstrumentVocal, model.InstrumentMix}) {
		t.Errorf("stems = %v, want [vocal mix]", got)
	}
}

func TestCover_FaithfulWeights(t *testing.T) {
	s, _, w := coverSetup(t)
	if _, err := w.generateWithSuno(context.Background(), humPayload(model.HumFaithful), noProgress, claimOK); err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if s.coverReq.AudioWeight != 0.9 || s.coverReq.StyleWeight != 0.5 {
		t.Errorf("weights = %v / %v", s.coverReq.AudioWeight, s.coverReq.StyleWeight)
	}
}

func TestCover_StemsModeSplitsCoverTask(t *testing.T) {
	s, _, w := coverSetup(t)
	srv := audioServer(t)
	s.stems = []client.Stem{{Instrument: model.InstrumentBass, URL: srv.URL + "/bas.mp3"}}
	s.stemsVocalURL = srv.URL + "/split-vokal.mp3"
	p := humPayload(model.HumFaithful)
	p.StemMode = model.StemModeStems
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if s.splitArgs != [2]string{"c1", "a1"} {
		t.Errorf("split args = %v, want the cover task [c1 a1]", s.splitArgs)
	}
	if got := instruments(res.Stems); !slices.Equal(got, []model.Instrument{model.InstrumentVocal, model.InstrumentBass}) {
		t.Errorf("stems = %v", got)
	}
}

func TestCover_NoCoverCapability_SkipRetryBeforeCharge(t *testing.T) {
	base, _, _, w := vocalSetup(t)
	w.r2Client = &signStorage{}
	_, err := w.generateWithSuno(context.Background(), humPayload(model.HumFree), noProgress, claimOK)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got %v", err)
	}
	if base.generateCalls != 0 || slices.Contains(base.calls, "GenerateMusic") {
		t.Errorf("GenerateMusic was called: %v", base.calls)
	}
}

func TestCover_WithoutVocal_SkipRetryBeforeCharge(t *testing.T) {
	s, _, w := coverSetup(t)
	p := humPayload(model.HumFree)
	p.Vocal = nil
	_, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if !errors.Is(err, asynq.SkipRetry) || s.coverCall != 0 {
		t.Fatalf("err = %v, cover calls %d", err, s.coverCall)
	}
}

func TestCover_SignFails_NotCharged(t *testing.T) {
	s, d, w := coverSetup(t)
	d.err = errors.New("signer down")
	_, err := w.generateWithSuno(context.Background(), humPayload(model.HumFree), noProgress, claimOK)
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want a retryable error, got %v", err)
	}
	if s.coverCall != 0 {
		t.Errorf("UploadCover was called %d times", s.coverCall)
	}
}

func TestCover_StyleHasNoBPMOrKey(t *testing.T) {
	s, _, w := coverSetup(t)
	if _, err := w.generateWithSuno(context.Background(), humPayload(model.HumFree), noProgress, claimOK); err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if got := s.coverReq.Style; got == "" || strings.Contains(got, "BPM") {
		t.Errorf("style = %q", got)
	}
}

func TestRenderWorker_CoverRejected_FinalAttemptRefunds(t *testing.T) {
	cs := newCreditSetup(t, model.JobStatusQueued, &signStorage{})
	base := &vocalSuno{}
	s := &coverSuno{vocalSuno: *base, t: t, coverErr: errors.New("suno 422")}
	cs.w.music = s
	cs.w.isLastAttempt = func(context.Context) bool { return true }
	if err := cs.w.ProcessTask(context.Background(), vocalTask(t, humPayload(model.HumFree))); err == nil {
		t.Fatal("want the error returned")
	}
	if st := jobStatus(t, cs.svc); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	if got := renderRefunds(cs.fake); len(got) != 1 || got[0].ID != testJobID || got[0].Op != credit.OpRenderMix {
		t.Errorf("refunds = %+v, want one render_mix", got)
	}
}
