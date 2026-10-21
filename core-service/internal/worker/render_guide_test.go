package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// guideSuno is fakeSuno plus the guide calls, with the call order recorded.
type guideSuno struct {
	fakeSuno
	calls        []string
	guideTrack   *client.Track
	addReq       *client.AddVocalsRequest
	addErr       error
	separateArgs [2]string
	separated    *client.SeparatedVocal
	separateErr  error
	words        []client.AlignedWord
	wordsArgs    [2]string
}

func (s *guideSuno) GenerateMusic(ctx context.Context, r *client.GenerateMusicRequest) (string, error) {
	s.calls = append(s.calls, "GenerateMusic")
	return s.fakeSuno.GenerateMusic(ctx, r)
}
func (s *guideSuno) WaitMusic(ctx context.Context, taskID string, i, m time.Duration) (*client.Track, error) {
	s.calls = append(s.calls, "WaitMusic")
	if taskID == "av1" {
		return s.guideTrack, nil
	}
	return s.fakeSuno.WaitMusic(ctx, taskID, i, m)
}
func (s *guideSuno) AddVocals(_ context.Context, r *client.AddVocalsRequest) (string, error) {
	s.calls = append(s.calls, "AddVocals")
	s.addReq = r
	return "av1", s.addErr
}
func (s *guideSuno) SeparateVocal(_ context.Context, taskID, audioID string) (string, error) {
	s.calls = append(s.calls, "SeparateVocal")
	s.separateArgs = [2]string{taskID, audioID}
	return "sv1", nil
}
func (s *guideSuno) WaitSeparated(context.Context, string, time.Duration, time.Duration) (*client.SeparatedVocal, error) {
	s.calls = append(s.calls, "WaitSeparated")
	return s.separated, s.separateErr
}
func (s *guideSuno) TimestampedLyrics(_ context.Context, taskID, audioID string) ([]client.AlignedWord, error) {
	s.calls = append(s.calls, "TimestampedLyrics")
	s.wordsArgs = [2]string{taskID, audioID}
	return s.words, nil
}

type fakeGuideAudio struct {
	analyzeReq *client.GuideAnalyzeRequest
	result     *client.GuideAnalyzeResult
	analyzeErr error
	shiftItems []client.PitchShiftItem
	shiftN     int
	shiftNotes *client.PitchShiftNotes
	shiftErr   error
}

func (a *fakeGuideAudio) AnalyzeGuide(_ context.Context, r *client.GuideAnalyzeRequest) (*client.GuideAnalyzeResult, error) {
	a.analyzeReq = r
	return a.result, a.analyzeErr
}
func (a *fakeGuideAudio) PitchShift(_ context.Context, items []client.PitchShiftItem, n int, notes *client.PitchShiftNotes) error {
	a.shiftItems, a.shiftN, a.shiftNotes = items, n, notes
	return a.shiftErr
}

func guidePayload() *model.RenderJobPayload {
	p := testPayload()
	p.StemMode = model.StemModeMix
	tonic, scale := model.TonicA, model.ScaleMinor
	p.Brief.Key = model.KeyConfig{Mode: model.KeyModeAuto, Tonic: &tonic, Scale: &scale}
	p.Guide = &model.GuideRequest{
		VocalGender: model.VocalGenderFemale,
		Sections:    []model.GuideSection{{ID: "s1", Type: "verse", Lines: []string{"la si"}}},
	}
	return p
}

func guideSetup(t *testing.T) (*guideSuno, *fakeGuideAudio, *fakeStorage, *RenderWorker) {
	t.Helper()
	srv := audioServer(t)
	s := &guideSuno{
		fakeSuno:   fakeSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 140}},
		guideTrack: &client.Track{ID: "g1", AudioURL: srv.URL + "/rehberli.mp3", Duration: 139},
		separated:  &client.SeparatedVocal{VocalURL: srv.URL + "/vokal.mp3", InstrumentalURL: srv.URL + "/ref.mp3"},
		words:      []client.AlignedWord{{Word: "[Verse]\nla ", StartS: 1, EndS: 1.5, Success: true}},
	}
	a := &fakeGuideAudio{result: &client.GuideAnalyzeResult{OffsetMs: 40, RangeMidi: &[2]int{60, 67}, NotesURL: "n"}}
	d := &fakeStorage{}
	w := testWorker(&s.fakeSuno, d)
	w.music = s
	w.WithGuideAudio(a)
	return s, a, d, w
}

func uploadedKeys(d *fakeStorage) []string {
	var keys []string
	for _, u := range d.uploads {
		keys = append(keys, u.key)
	}
	return keys
}

func TestShiftSemitones_ManualKeyIsZero(t *testing.T) {
	b := model.Brief{Key: model.KeyConfig{Mode: model.KeyModeManual}}
	if n := ShiftSemitones(b, &[2]int{60, 67}, &model.VocalRange{Low: 45, High: 52}); n != 0 {
		t.Fatalf("manual key shift = %d, want 0", n)
	}
}

func TestShiftSemitones_InsideRangeIsZero(t *testing.T) {
	b := model.Brief{Key: model.KeyConfig{Mode: model.KeyModeAuto}}
	if n := ShiftSemitones(b, &[2]int{60, 67}, &model.VocalRange{Low: 55, High: 70}); n != 0 {
		t.Fatalf("inside shift = %d, want 0", n)
	}
}

func TestShiftSemitones_ClampedToFour(t *testing.T) {
	b := model.Brief{Key: model.KeyConfig{Mode: model.KeyModeAuto}}
	if n := ShiftSemitones(b, &[2]int{60, 67}, &model.VocalRange{Low: 45, High: 52}); n != -4 {
		t.Fatalf("shift = %d, want -4", n)
	}
	if n := ShiftSemitones(b, &[2]int{60, 67}, &model.VocalRange{Low: 62, High: 70}); n != 3 {
		t.Fatalf("small shift = %d, want 3 (round(66 - 63.5), half away from zero)", n)
	}
	if n := ShiftSemitones(b, nil, &model.VocalRange{Low: 45, High: 52}); n != 0 {
		t.Fatalf("no guide range shift = %d, want 0", n)
	}
	if n := ShiftSemitones(b, &[2]int{60, 67}, nil); n != 0 {
		t.Fatalf("no user range shift = %d, want 0", n)
	}
}

func TestTransposeKey(t *testing.T) {
	cases := []struct {
		in   model.Tonic
		n    int
		want model.Tonic
	}{{model.TonicA, -3, model.TonicFSharp}, {model.TonicB, 2, model.TonicCSharp}, {model.TonicC, -1, model.TonicB}, {model.TonicE, 0, model.TonicE}}
	for _, c := range cases {
		if got := TransposeKey(c.in, c.n); got != c.want {
			t.Errorf("TransposeKey(%s, %d) = %s, want %s", c.in, c.n, got, c.want)
		}
	}
}

func TestGuided_HappyPath(t *testing.T) {
	s, a, d, w := guideSetup(t)
	res, err := w.generateWithSuno(context.Background(), guidePayload(), noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	want := []string{"GenerateMusic", "WaitMusic", "AddVocals", "WaitMusic", "SeparateVocal", "WaitSeparated", "TimestampedLyrics"}
	if strings.Join(s.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", s.calls, want)
	}
	if s.addReq.UploadURL != s.track.AudioURL || s.addReq.Lyrics != "[Verse]\nla si" || s.addReq.VocalGender != "f" {
		t.Errorf("add-vocals request = %+v", s.addReq)
	}
	if s.separateArgs != [2]string{"av1", "g1"} || s.wordsArgs != [2]string{"av1", "g1"} {
		t.Errorf("separate %v / words %v, want av1 g1", s.separateArgs, s.wordsArgs)
	}
	keys := uploadedKeys(d)
	var raw, ref string
	for _, k := range keys {
		switch {
		case strings.HasPrefix(k, "guides/kullanici-a/p1/") && strings.HasSuffix(k, "-ham.mp3"):
			raw = k
		case strings.HasPrefix(k, "guides/kullanici-a/p1/") && strings.HasSuffix(k, "-ref.mp3"):
			ref = k
		}
	}
	if raw == "" || ref == "" {
		t.Fatalf("uploads = %v, want the raw guide and the reference", keys)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(raw, "guides/kullanici-a/p1/"), "-ham.mp3")
	if a.analyzeReq.GuideKey != raw || a.analyzeReq.ReferenceKey != ref || a.analyzeReq.InstrumentalKey != res.Stems[0].FileKey {
		t.Errorf("analyze request = %+v", a.analyzeReq)
	}
	if a.analyzeReq.AlignedKey != "guides/kullanici-a/p1/"+id+".mp3" || a.analyzeReq.NotesKey != "guides/kullanici-a/p1/"+id+"-notes.json" {
		t.Errorf("analyze output keys = %s / %s", a.analyzeReq.AlignedKey, a.analyzeReq.NotesKey)
	}
	if len(a.analyzeReq.Words) != 1 || a.analyzeReq.Words[0].StartS != 1 || len(a.analyzeReq.Sections) != 1 {
		t.Errorf("analyze words/sections = %+v", a.analyzeReq)
	}
	g := res.Guide
	if g == nil || g.FileKey != a.analyzeReq.AlignedKey || g.NotesKey == nil || *g.NotesKey != a.analyzeReq.NotesKey || g.OffsetMs != 40 || g.PitchShiftSemitones != 0 {
		t.Errorf("guide record = %+v", g)
	}
	if a.shiftItems != nil {
		t.Errorf("no user range: PitchShift must not be called, got %+v", a.shiftItems)
	}
}

func TestGuided_SeparationFails_RefundsBoth(t *testing.T) {
	cs := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	srv := audioServer(t)
	s := &guideSuno{
		fakeSuno:    fakeSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 140}},
		guideTrack:  &client.Track{ID: "g1", AudioURL: srv.URL + "/rehberli.mp3"},
		separateErr: errors.New("separation failed"),
	}
	cs.w.music = s
	cs.w.WithGuideAudio(&fakeGuideAudio{})
	if err := cs.w.ProcessTask(context.Background(), guideTask(t)); err == nil {
		t.Fatal("want an error")
	}
	if st := jobStatus(t, cs.svc); st != model.JobStatusFailed {
		t.Errorf("status = %s, want failed", st)
	}
	var render, guide bool
	for _, r := range cs.fake.Refunds {
		render = render || (r.Op == credit.OpRenderMix && r.ID == "is1")
		guide = guide || (r.Op == credit.OpRenderGuide && r.ID == "is1:rehber")
	}
	if !render || !guide {
		t.Errorf("refunds = %+v, want render_mix is1 and render_guide is1:rehber", cs.fake.Refunds)
	}
}

func TestGuided_AnalysisFails_StillSucceeds(t *testing.T) {
	_, a, _, w := guideSetup(t)
	a.analyzeErr = errors.New("audio-service down")
	res, err := w.generateWithSuno(context.Background(), guidePayload(), noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if res.Guide == nil || res.Guide.NotesKey != nil || !strings.HasSuffix(res.Guide.FileKey, "-ham.mp3") || res.Guide.OffsetMs != 0 {
		t.Errorf("guide = %+v, want the raw guide without notes", res.Guide)
	}
}

func TestGuided_ShiftsStemsAndKey(t *testing.T) {
	_, a, _, w := guideSetup(t)
	p := guidePayload()
	p.Guide.Range = &model.VocalRange{Low: 45, High: 52}
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if a.shiftN != -4 || len(a.shiftItems) != 2 {
		t.Fatalf("shift = %d %+v, want -4 over the mix stem and the guide", a.shiftN, a.shiftItems)
	}
	if !strings.HasSuffix(a.shiftItems[0].OutputKey, "-km4.mp3") || !strings.HasSuffix(a.shiftItems[1].OutputKey, "-km4.mp3") {
		t.Errorf("shift outputs = %+v", a.shiftItems)
	}
	if res.Stems[0].FileKey != a.shiftItems[0].OutputKey || res.Guide.FileKey != a.shiftItems[1].OutputKey {
		t.Errorf("result keys stem %s guide %s, want the shifted ones", res.Stems[0].FileKey, res.Guide.FileKey)
	}
	if a.shiftNotes == nil || res.Guide.NotesKey == nil || *res.Guide.NotesKey != a.shiftNotes.OutputKey || !strings.HasSuffix(a.shiftNotes.OutputKey, "-notes-km4.json") {
		t.Errorf("notes = %+v / %v", a.shiftNotes, res.Guide.NotesKey)
	}
	if res.Guide.PitchShiftSemitones != -4 || res.Key.Tonic != model.TonicF {
		t.Errorf("shift %d key %s, want -4 and F (A - 4)", res.Guide.PitchShiftSemitones, res.Key.Tonic)
	}
}

func TestGuided_ShiftFailureKeepsOriginals(t *testing.T) {
	_, a, _, w := guideSetup(t)
	a.shiftErr = errors.New("rubberband missing")
	p := guidePayload()
	p.Guide.Range = &model.VocalRange{Low: 45, High: 52}
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if res.Guide.PitchShiftSemitones != 0 || res.Key.Tonic != model.TonicA || strings.Contains(res.Stems[0].FileKey, "-km4") {
		t.Errorf("result = %+v / %+v, want the unshifted render", res.Guide, res.Key)
	}
}

func TestGuided_DeletedOwnerBeforeGuideUpload(t *testing.T) {
	s, _, d, _ := guideSetup(t)
	checker := &afterNChecker{allow: 1}
	w := workerWithChecker(&s.fakeSuno, d, checker)
	w.music = s
	w.WithGuideAudio(&fakeGuideAudio{})
	if _, err := w.generateWithSuno(context.Background(), guidePayload(), noProgress, claimOK); err == nil {
		t.Fatal("want an error for a deleted owner")
	}
	for _, k := range uploadedKeys(d) {
		if strings.HasPrefix(k, "guides/") {
			t.Errorf("uploaded %s for a deleted owner", k)
		}
	}
}

func TestGuided_NotConfiguredFailsBeforeSuno(t *testing.T) {
	s, _, d, _ := guideSetup(t)
	w := testWorker(&s.fakeSuno, d)
	w.music = s // no guide audio
	if _, err := w.generateWithSuno(context.Background(), guidePayload(), noProgress, claimOK); err == nil {
		t.Fatal("want an error")
	}
	if len(s.calls) != 0 {
		t.Errorf("Suno calls = %v, want none", s.calls)
	}
}

func TestNoGuide_Unchanged(t *testing.T) {
	s, a, _, w := guideSetup(t)
	p := guidePayload()
	p.Guide = nil
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if s.addReq != nil || a.analyzeReq != nil || res.Guide != nil {
		t.Errorf("guide work without a guide: add %+v analyze %+v guide %+v", s.addReq, a.analyzeReq, res.Guide)
	}
}

// afterNChecker lets the first allow checks through, then reports deleted.
type afterNChecker struct{ allow, n int }

func (c *afterNChecker) IsDeleted(context.Context, string) (bool, error) {
	c.n++
	return c.n > c.allow, nil
}

func guideTask(t *testing.T) *asynq.Task {
	t.Helper()
	pb, _ := json.Marshal(guidePayload())
	data, _ := json.Marshal(map[string]any{"jobId": "is1", "payload": json.RawMessage(pb)})
	return asynq.NewTask(service.TaskTypeRender, data)
}

// A job whose payload cannot be read may have a guide debit; the final
// failure refunds it too (review I-4).
func TestRenderWorker_InvalidPayloadRefundsGuide(t *testing.T) {
	cs := newCreditSetup(t, model.JobStatusQueued, &fakeStorage{})
	data, _ := json.Marshal(map[string]any{"jobId": "is1", "payload": []int{1}})
	_ = cs.w.ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeRender, data))
	found := false
	for _, r := range cs.fake.Refunds {
		found = found || (r.Op == credit.OpRenderGuide && r.ID == "is1:rehber")
	}
	if !found {
		t.Errorf("refunds = %+v, want the guide refund", cs.fake.Refunds)
	}
}

// In the stems tier the first stem is one instrument; the guide is aligned
// to Suno's full track instead (review I-6).
func TestGuided_StemsModeAlignsToFullTrack(t *testing.T) {
	s, a, d, w := guideSetup(t)
	srv := audioServer(t)
	s.stems = []client.Stem{
		{Instrument: model.InstrumentBass, URL: srv.URL + "/bas.mp3"},
		{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
	}
	p := guidePayload()
	p.StemMode = model.StemModeStems
	if _, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK); err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if !strings.HasSuffix(a.analyzeReq.InstrumentalKey, "-inst.mp3") || !strings.HasPrefix(a.analyzeReq.InstrumentalKey, "guides/kullanici-a/p1/") {
		t.Fatalf("instrumental key = %s, want the stored full track", a.analyzeReq.InstrumentalKey)
	}
	var stored bool
	for _, u := range d.uploads {
		stored = stored || (u.key == a.analyzeReq.InstrumentalKey && strings.HasSuffix(string(u.data), "/tam.mp3"))
	}
	if !stored {
		t.Errorf("the full track was not stored at %s", a.analyzeReq.InstrumentalKey)
	}
}
