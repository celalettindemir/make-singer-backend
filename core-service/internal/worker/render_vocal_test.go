package worker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// vocalSuno is fakeSuno plus the vocal separation and word timing calls,
// with the call order recorded.
type vocalSuno struct {
	fakeSuno
	calls         []string
	separateArgs  [2]string
	separateURL   string
	separated     *client.SeparatedVocal
	separateErr   error
	stemsVocalURL string
	words         []client.AlignedWord
	wordsErr      error
	wordsArgs     [2]string
}

func (s *vocalSuno) GenerateMusic(ctx context.Context, r *client.GenerateMusicRequest) (string, error) {
	s.calls = append(s.calls, "GenerateMusic")
	return s.fakeSuno.GenerateMusic(ctx, r)
}
func (s *vocalSuno) WaitMusic(ctx context.Context, taskID string, i, m time.Duration) (*client.Track, error) {
	s.calls = append(s.calls, "WaitMusic")
	return s.fakeSuno.WaitMusic(ctx, taskID, i, m)
}
func (s *vocalSuno) SplitStems(ctx context.Context, taskID, audioID string) (string, error) {
	s.calls = append(s.calls, "SplitStems")
	return s.fakeSuno.SplitStems(ctx, taskID, audioID)
}
func (s *vocalSuno) WaitStems(ctx context.Context, id string, i, m time.Duration) ([]client.Stem, string, error) {
	s.calls = append(s.calls, "WaitStems")
	stems, _, err := s.fakeSuno.WaitStems(ctx, id, i, m)
	return stems, s.stemsVocalURL, err
}
func (s *vocalSuno) IsolateStem(ctx context.Context, taskID, audioID, stemName string) (string, error) {
	s.calls = append(s.calls, "IsolateStem")
	return s.fakeSuno.IsolateStem(ctx, taskID, audioID, stemName)
}
func (s *vocalSuno) SeparateVocal(_ context.Context, taskID, audioID string) (string, error) {
	s.calls = append(s.calls, "SeparateVocal")
	s.separateArgs = [2]string{taskID, audioID}
	return "sv1", nil
}
func (s *vocalSuno) SeparateVocalURL(_ context.Context, audioURL string) (string, error) {
	s.calls = append(s.calls, "SeparateVocalURL")
	s.separateURL = audioURL
	return "sv2", nil
}
func (s *vocalSuno) WaitSeparated(context.Context, string, time.Duration, time.Duration) (*client.SeparatedVocal, error) {
	s.calls = append(s.calls, "WaitSeparated")
	return s.separated, s.separateErr
}
func (s *vocalSuno) TimestampedLyrics(_ context.Context, taskID, audioID string) ([]client.AlignedWord, error) {
	s.calls = append(s.calls, "TimestampedLyrics")
	s.wordsArgs = [2]string{taskID, audioID}
	return s.words, s.wordsErr
}

type fakeGuideAudio struct {
	analyzeCalls int
	analyzeReq   *client.GuideAnalyzeRequest
	result       *client.GuideAnalyzeResult
	analyzeErr   error
	shiftItems   []client.PitchShiftItem
	shiftN       int
	shiftNotes   *client.PitchShiftNotes
	shiftErr     error
}

func (a *fakeGuideAudio) AnalyzeGuide(_ context.Context, r *client.GuideAnalyzeRequest) (*client.GuideAnalyzeResult, error) {
	a.analyzeCalls++
	a.analyzeReq = r
	return a.result, a.analyzeErr
}
func (a *fakeGuideAudio) PitchShift(_ context.Context, items []client.PitchShiftItem, n int, notes *client.PitchShiftNotes) error {
	a.shiftItems, a.shiftN, a.shiftNotes = items, n, notes
	return a.shiftErr
}

func vocalPayload() *model.RenderJobPayload {
	p := testPayload()
	p.StemMode = model.StemModeMix
	tonic, scale := model.TonicA, model.ScaleMinor
	p.Brief.Key = model.KeyConfig{Mode: model.KeyModeAuto, Tonic: &tonic, Scale: &scale}
	p.Vocal = &model.VocalRequest{
		Voice:    model.VoiceFemale,
		Sections: []model.GuideSection{{ID: "s1", Type: "verse", Lines: []string{"la si"}}},
	}
	return p
}

func vocalSetup(t *testing.T) (*vocalSuno, *fakeGuideAudio, *fakeStorage, *RenderWorker) {
	t.Helper()
	srv := audioServer(t)
	s := &vocalSuno{
		fakeSuno:  fakeSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 140}},
		separated: &client.SeparatedVocal{VocalURL: srv.URL + "/vokal.mp3", InstrumentalURL: srv.URL + "/inst.mp3"},
		words: []client.AlignedWord{
			{Word: "[Verse]", StartS: 0.9, EndS: 0.9, Success: true},
			{Word: "la ", StartS: 1, EndS: 1.5, Success: true},
			{Word: "si", StartS: 1.6, EndS: 2, Success: true},
		},
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

// uploadedFrom is the key the file served at path was stored under.
func uploadedFrom(d *fakeStorage, path string) string {
	for _, u := range d.uploads {
		if string(u.data) == "ses:"+path {
			return u.key
		}
	}
	return ""
}

func instruments(stems []model.StemResultRecord) []model.Instrument {
	var out []model.Instrument
	for _, s := range stems {
		out = append(out, s.Instrument)
	}
	return out
}

const guidePrefix = "guides/kullanici-a/p1/"

func TestVocal_Mix_VocalFirstAndInstrumental(t *testing.T) {
	s, _, d, w := vocalSetup(t)
	p := vocalPayload()
	p.Vocal.Title = "Yaz Yağmuru"
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	r := s.generateReq
	if r.Instrumental || r.Lyrics != "[Verse]\nla si" || r.VocalGender != "f" || r.Title != "Yaz Yağmuru" {
		t.Errorf("generate request = %+v", r)
	}
	if strings.Contains(r.Style, "instrumental") {
		t.Errorf("vocal style %q has the instrumental word", r.Style)
	}
	if n := strings.Count(strings.Join(s.calls, ","), "SeparateVocal"); n != 1 || slices.Contains(s.calls, "SeparateVocalURL") {
		t.Errorf("calls = %v, want SeparateVocal once", s.calls)
	}
	if slices.Contains(s.calls, "AddVocals") || s.separateArgs != [2]string{"t1", "a1"} {
		t.Errorf("calls %v, separate args %v", s.calls, s.separateArgs)
	}
	if got := instruments(res.Stems); !slices.Equal(got, []model.Instrument{model.InstrumentVocal, model.InstrumentMix}) {
		t.Fatalf("stems = %v, want [vocal mix]", got)
	}
	v, m := res.Stems[0], res.Stems[1]
	if !strings.HasPrefix(v.FileKey, guidePrefix) || v.FileKey != guidePrefix+v.ID+".mp3" || uploadedFrom(d, "/vokal.mp3") != v.FileKey {
		t.Errorf("vocal stem = %+v, uploads %v", v, uploadedKeys(d))
	}
	if !strings.HasPrefix(m.FileKey, "stems/kullanici-a/p1/") || uploadedFrom(d, "/inst.mp3") != m.FileKey {
		t.Errorf("mix stem = %+v, uploads %v", m, uploadedKeys(d))
	}
	if v.Duration != 140 || len(v.WaveformData) == 0 {
		t.Errorf("vocal stem duration/waveform = %v / %d", v.Duration, len(v.WaveformData))
	}
	if len(res.LyricTimeline) != 1 || res.LyricTimeline[0].SectionID != "s1" {
		t.Errorf("lyric timeline = %+v", res.LyricTimeline)
	}
	if res.Guide != nil {
		t.Errorf("guide = %+v, want nil without analysis", res.Guide)
	}
}

func TestVocal_DefaultTitleIsGenreSong(t *testing.T) {
	s, _, _, w := vocalSetup(t)
	if _, err := w.generateWithSuno(context.Background(), vocalPayload(), noProgress, claimOK); err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if s.generateReq.Title != "pop song" {
		t.Errorf("title = %q, want %q", s.generateReq.Title, "pop song")
	}
}

func TestVocal_Mix_NoInstrumentalFails(t *testing.T) {
	s, _, _, w := vocalSetup(t)
	s.separated.InstrumentalURL = ""
	_, err := w.generateWithSuno(context.Background(), vocalPayload(), noProgress, claimOK)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got %v", err)
	}
}

func TestVocal_SeparationFails_SkipRetry(t *testing.T) {
	s, _, _, w := vocalSetup(t)
	s.separateErr = errors.New("separation failed")
	_, err := w.generateWithSuno(context.Background(), vocalPayload(), noProgress, claimOK)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got %v", err)
	}
}

func TestVocal_Focus_RestIsSeparatedByURL(t *testing.T) {
	s, _, d, w := vocalSetup(t)
	srv := audioServer(t)
	s.isolated = &client.IsolatedStem{ExtractURL: srv.URL + "/gitar.mp3", RemoveURL: srv.URL + "/kalan.mp3"}
	p := vocalPayload()
	p.StemMode = model.StemModeFocus
	guitar := model.InstrumentGuitar
	p.FocusInstrument = &guitar
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if s.separateURL != s.isolated.RemoveURL || slices.Contains(s.calls, "SeparateVocal") {
		t.Errorf("separate url %q, calls %v", s.separateURL, s.calls)
	}
	want := []model.Instrument{model.InstrumentVocal, model.InstrumentGuitar, model.InstrumentRest}
	if got := instruments(res.Stems); !slices.Equal(got, want) {
		t.Fatalf("stems = %v, want %v", got, want)
	}
	if uploadedFrom(d, "/gitar.mp3") != res.Stems[1].FileKey || uploadedFrom(d, "/inst.mp3") != res.Stems[2].FileKey {
		t.Errorf("focus/rest stems = %+v, uploads %v", res.Stems, uploadedKeys(d))
	}
	if uploadedFrom(d, "/kalan.mp3") != "" {
		t.Errorf("the unseparated rest was stored: %v", uploadedKeys(d))
	}
}

func TestVocal_Stems_UsesSplitVocal(t *testing.T) {
	s, _, d, w := vocalSetup(t)
	srv := audioServer(t)
	s.stems = []client.Stem{
		{Instrument: model.InstrumentBass, URL: srv.URL + "/bas.mp3"},
		{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
	}
	s.stemsVocalURL = srv.URL + "/split-vokal.mp3"
	p := vocalPayload()
	p.StemMode = model.StemModeStems
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if slices.Contains(s.calls, "SeparateVocal") || slices.Contains(s.calls, "SeparateVocalURL") {
		t.Errorf("calls = %v, want no separation", s.calls)
	}
	want := []model.Instrument{model.InstrumentVocal, model.InstrumentBass, model.InstrumentDrums}
	if got := instruments(res.Stems); !slices.Equal(got, want) {
		t.Fatalf("stems = %v, want %v", got, want)
	}
	if uploadedFrom(d, "/split-vokal.mp3") != res.Stems[0].FileKey || !strings.HasPrefix(res.Stems[0].FileKey, guidePrefix) {
		t.Errorf("vocal stem %+v, uploads %v", res.Stems[0], uploadedKeys(d))
	}
	// No analysis: Suno's full track is not stored.
	if uploadedFrom(d, "/tam.mp3") != "" {
		t.Errorf("full track stored without analysis: %v", uploadedKeys(d))
	}
}

func TestVocal_TimingFails_StillSucceeds(t *testing.T) {
	s, _, _, w := vocalSetup(t)
	s.wordsErr = errors.New("timestamps 500")
	res, err := w.generateWithSuno(context.Background(), vocalPayload(), noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if res.LyricTimeline != nil {
		t.Errorf("lyric timeline = %+v, want nil", res.LyricTimeline)
	}
	if s.wordsArgs != [2]string{"t1", "a1"} {
		t.Errorf("timestamps args = %v", s.wordsArgs)
	}
}

func TestVocal_NoPlan_NoAnalysis(t *testing.T) {
	_, a, _, w := vocalSetup(t)
	p := vocalPayload()
	p.Vocal.Range = &model.VocalRange{Low: 45, High: 52}
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if a.analyzeCalls != 0 || a.shiftItems != nil || res.Guide != nil {
		t.Errorf("analyze %d shift %+v guide %+v, want none", a.analyzeCalls, a.shiftItems, res.Guide)
	}
}

func TestVocal_Analysis_UsesVocalAndMix(t *testing.T) {
	_, a, _, w := vocalSetup(t)
	p := vocalPayload()
	p.GuideAnalysis = true
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	v, m := res.Stems[0], res.Stems[1]
	raw := guidePrefix + v.ID + ".mp3"
	r := a.analyzeReq
	if r == nil || r.GuideKey != raw || r.ReferenceKey != m.FileKey || r.InstrumentalKey != m.FileKey {
		t.Fatalf("analyze request = %+v", r)
	}
	if !strings.HasPrefix(r.AlignedKey, guidePrefix) || r.AlignedKey == raw || r.NotesKey != guidePrefix+v.ID+"-notes.json" {
		t.Errorf("analyze output keys = %s / %s", r.AlignedKey, r.NotesKey)
	}
	if len(r.Words) != 3 || len(r.Sections) != 1 || r.VocalGender != "f" || r.BPM != 120 {
		t.Errorf("analyze words/sections = %+v", r)
	}
	if v.FileKey != r.AlignedKey {
		t.Errorf("vocal stem key %s, want the aligned %s", v.FileKey, r.AlignedKey)
	}
	if g := res.Guide; g == nil || g.NotesKey == nil || *g.NotesKey != r.NotesKey || g.PitchShiftSemitones != 0 {
		t.Errorf("guide record = %+v", res.Guide)
	}
	if a.shiftItems != nil {
		t.Errorf("no user range: PitchShift must not be called, got %+v", a.shiftItems)
	}
}

func TestVocal_Analysis_FocusUsesRest(t *testing.T) {
	s, a, _, w := vocalSetup(t)
	srv := audioServer(t)
	s.isolated = &client.IsolatedStem{ExtractURL: srv.URL + "/gitar.mp3", RemoveURL: srv.URL + "/kalan.mp3"}
	p := vocalPayload()
	p.GuideAnalysis = true
	p.StemMode = model.StemModeFocus
	guitar := model.InstrumentGuitar
	p.FocusInstrument = &guitar
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	rest := res.Stems[2].FileKey
	if a.analyzeReq.ReferenceKey != rest || a.analyzeReq.InstrumentalKey != rest {
		t.Errorf("analyze ref/inst = %s / %s, want the rest stem %s", a.analyzeReq.ReferenceKey, a.analyzeReq.InstrumentalKey, rest)
	}
}

// In the stems tier one instrument is a poor reference; the vocal is aligned
// to Suno's full track instead.
func TestVocal_Analysis_StemsAlignsToFullTrack(t *testing.T) {
	s, a, d, w := vocalSetup(t)
	srv := audioServer(t)
	s.stems = []client.Stem{{Instrument: model.InstrumentBass, URL: srv.URL + "/bas.mp3"}}
	s.stemsVocalURL = srv.URL + "/split-vokal.mp3"
	p := vocalPayload()
	p.GuideAnalysis = true
	p.StemMode = model.StemModeStems
	if _, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK); err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	inst := uploadedFrom(d, "/tam.mp3")
	if !strings.HasPrefix(inst, guidePrefix) || !strings.HasSuffix(inst, "-inst.mp3") {
		t.Fatalf("full track key = %q, uploads %v", inst, uploadedKeys(d))
	}
	if a.analyzeReq.ReferenceKey != inst || a.analyzeReq.InstrumentalKey != inst {
		t.Errorf("analyze ref/inst = %s / %s, want %s", a.analyzeReq.ReferenceKey, a.analyzeReq.InstrumentalKey, inst)
	}
}

func TestVocal_AnalysisWithoutAudioService_Skipped(t *testing.T) {
	s, _, d, _ := vocalSetup(t)
	w := testWorker(&s.fakeSuno, d)
	w.music = s // no guide audio
	p := vocalPayload()
	p.GuideAnalysis = true
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if res.Guide != nil || res.Stems[0].Instrument != model.InstrumentVocal {
		t.Errorf("result = %+v", res)
	}
}

func TestVocal_AnalysisFails_KeepsRawVocal(t *testing.T) {
	_, a, _, w := vocalSetup(t)
	a.analyzeErr = errors.New("audio-service down")
	p := vocalPayload()
	p.GuideAnalysis = true
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	v := res.Stems[0]
	if v.FileKey != guidePrefix+v.ID+".mp3" {
		t.Errorf("vocal key = %s, want the raw key", v.FileKey)
	}
	if res.Guide == nil || res.Guide.NotesKey != nil {
		t.Errorf("guide = %+v, want a record without notes", res.Guide)
	}
}

func TestVocal_ShiftMovesVocalWithStems(t *testing.T) {
	_, a, _, w := vocalSetup(t)
	p := vocalPayload()
	p.GuideAnalysis = true
	p.Vocal.Range = &model.VocalRange{Low: 45, High: 52}
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if a.shiftN != -4 || len(a.shiftItems) != 2 {
		t.Fatalf("shift = %d %+v, want -4 over the vocal and the mix", a.shiftN, a.shiftItems)
	}
	if a.shiftItems[0].InputKey != a.analyzeReq.AlignedKey {
		t.Errorf("first shift input %s, want the aligned vocal %s", a.shiftItems[0].InputKey, a.analyzeReq.AlignedKey)
	}
	for i, st := range res.Stems {
		if st.FileKey != a.shiftItems[i].OutputKey || !strings.HasSuffix(st.FileKey, "-km4.mp3") {
			t.Errorf("stem %d key %s, want the shifted %s", i, st.FileKey, a.shiftItems[i].OutputKey)
		}
	}
	if a.shiftNotes == nil || res.Guide.NotesKey == nil || *res.Guide.NotesKey != a.shiftNotes.OutputKey || !strings.HasSuffix(a.shiftNotes.OutputKey, "-notes-km4.json") {
		t.Errorf("notes = %+v / %v", a.shiftNotes, res.Guide.NotesKey)
	}
	if res.Guide.PitchShiftSemitones != -4 || res.Key.Tonic != model.TonicF {
		t.Errorf("shift %d key %s, want -4 and F (A - 4)", res.Guide.PitchShiftSemitones, res.Key.Tonic)
	}
}

func TestVocal_ShiftFailureKeepsOriginals(t *testing.T) {
	_, a, _, w := vocalSetup(t)
	a.shiftErr = errors.New("rubberband missing")
	p := vocalPayload()
	p.GuideAnalysis = true
	p.Vocal.Range = &model.VocalRange{Low: 45, High: 52}
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if res.Guide.PitchShiftSemitones != 0 || res.Key.Tonic != model.TonicA {
		t.Errorf("result = %+v / %+v, want the unshifted render", res.Guide, res.Key)
	}
	for _, st := range res.Stems {
		if strings.Contains(st.FileKey, "-km4") {
			t.Errorf("stem key %s shifted after a failed shift", st.FileKey)
		}
	}
}

func TestVocal_DeletedOwnerBeforeVocalUpload(t *testing.T) {
	s, _, d, _ := vocalSetup(t)
	checker := &afterNChecker{allow: 1}
	w := workerWithChecker(&s.fakeSuno, d, checker)
	w.music = s
	_, err := w.generateWithSuno(context.Background(), vocalPayload(), noProgress, claimOK)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry for a deleted owner, got %v", err)
	}
	if len(d.uploads) != 0 {
		t.Errorf("uploaded %v for a deleted owner", uploadedKeys(d))
	}
}

func TestVocal_DeletedOwnerDuringAnalysis(t *testing.T) {
	s, a, d, _ := vocalSetup(t)
	p := vocalPayload()
	p.GuideAnalysis = true
	// Let the checks before the analysis pass (probe the count).
	for allow := 1; allow < 8; allow++ {
		a.analyzeCalls = 0
		w := workerWithChecker(&s.fakeSuno, d, &afterNChecker{allow: allow})
		w.music = s
		w.guideAudio = a
		res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
		if err != nil {
			continue // owner already deleted before the vocal was stored
		}
		if a.analyzeCalls != 0 {
			t.Fatalf("allow %d: AnalyzeGuide called for a deleted owner", allow)
		}
		if res.Stems[0].Instrument != model.InstrumentVocal || strings.Contains(res.Stems[0].FileKey, "-aligned") {
			t.Fatalf("allow %d: want the raw vocal, got %+v", allow, res.Stems[0])
		}
		if res.Guide == nil || res.Guide.NotesKey != nil || res.Guide.PitchShiftSemitones != 0 {
			t.Fatalf("allow %d: guide = %+v, want no notes and no shift", allow, res.Guide)
		}
		return
	}
	t.Fatal("no check count produced a render that skipped the analysis")
}

func TestVocal_NotConfiguredFailsBeforeSuno(t *testing.T) {
	s := &fakeSuno{track: &client.Track{ID: "a1"}}
	w := testWorker(s, &fakeStorage{}) // fakeSuno has no vocal separation
	_, err := w.generateWithSuno(context.Background(), vocalPayload(), noProgress, claimOK)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got %v", err)
	}
	if s.generateCalls != 0 {
		t.Errorf("GenerateMusic called %d times", s.generateCalls)
	}
}

func TestRender_NoVocal_StaysInstrumental(t *testing.T) {
	s, a, d, w := vocalSetup(t)
	p := vocalPayload()
	p.Vocal = nil
	p.GuideAnalysis = true
	res, err := w.generateWithSuno(context.Background(), p, noProgress, claimOK)
	if err != nil {
		t.Fatalf("generateWithSuno: %v", err)
	}
	if !s.generateReq.Instrumental || s.generateReq.Lyrics != "" || s.generateReq.Title != "pop instrumental" {
		t.Errorf("generate request = %+v", s.generateReq)
	}
	if got := instruments(res.Stems); !slices.Equal(got, []model.Instrument{model.InstrumentMix}) {
		t.Errorf("stems = %v, want [mix]", got)
	}
	if uploadedFrom(d, "/tam.mp3") != res.Stems[0].FileKey {
		t.Errorf("mix is not the track: uploads %v", uploadedKeys(d))
	}
	if slices.Contains(s.calls, "SeparateVocal") || slices.Contains(s.calls, "TimestampedLyrics") || a.analyzeCalls != 0 {
		t.Errorf("vocal work without a vocal: calls %v analyze %d", s.calls, a.analyzeCalls)
	}
	if res.LyricTimeline != nil || res.Guide != nil {
		t.Errorf("timeline %+v guide %+v, want nil", res.LyricTimeline, res.Guide)
	}
}

func TestVocal_Mock_AddsVocalStem(t *testing.T) {
	w := &RenderWorker{}
	for _, mode := range []model.StemMode{model.StemModeMix, model.StemModeStems} {
		p := vocalPayload()
		p.StemMode = mode
		p.GuideAnalysis = true
		p.Arrangement.Instruments = []model.Instrument{model.InstrumentDrums}
		rec, err := w.generateMockResult(p)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if len(rec.Stems) < 2 || rec.Stems[0].Instrument != model.InstrumentVocal {
			t.Fatalf("%s: stems = %v, want vocal first", mode, instruments(rec.Stems))
		}
		if k := rec.Stems[0].FileKey; k != "guides/kullanici-a/p1/"+rec.Stems[0].ID+".mp3" {
			t.Errorf("%s: mock vocal key = %s, want a guides/ key (master gate)", mode, k)
		}
		if rec.LyricTimeline != nil || rec.Guide != nil {
			t.Errorf("%s: timeline %+v guide %+v, want nil", mode, rec.LyricTimeline, rec.Guide)
		}
	}
	p := vocalPayload()
	p.Vocal = nil
	rec, _ := w.generateMockResult(p)
	if got := instruments(rec.Stems); !slices.Equal(got, []model.Instrument{model.InstrumentMix}) {
		t.Errorf("no vocal mock stems = %v", got)
	}
}

// afterNChecker lets the first allow checks through, then reports deleted.
type afterNChecker struct{ allow, n int }

func (c *afterNChecker) IsDeleted(context.Context, string) (bool, error) {
	c.n++
	return c.n > c.allow, nil
}

func vocalTask(t *testing.T, p *model.RenderJobPayload) *asynq.Task {
	t.Helper()
	pb, _ := json.Marshal(p)
	data, _ := json.Marshal(map[string]any{"jobId": testJobID, "payload": json.RawMessage(pb)})
	return asynq.NewTask(service.TaskTypeRender, data)
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
