package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// maxGuideShift caps the key change toward the user's range (spec 9 §3.4).
const maxGuideShift = 4

// WithGuideAudio turns on the guided render: audio-service aligns the guide
// and makes its note strip. Without it a guided job fails before Suno.
func (w *RenderWorker) WithGuideAudio(a client.GuideAudio) *RenderWorker {
	w.guideAudio = a
	return w
}

// guideGenerator returns the Suno guide calls for a guided payload, or an
// error when the guided render is not configured. Checked BEFORE the paid
// call.
func (w *RenderWorker) guideGenerator(payload *model.RenderJobPayload) (client.GuideGenerator, error) {
	if payload.Vocal == nil {
		return nil, nil
	}
	g, ok := w.music.(client.GuideGenerator)
	if !ok || w.guideAudio == nil {
		return nil, errors.New("the guided render is not configured")
	}
	if _, err := client.UserKey("guides", payload.OwnerID, payload.ProjectID, "x.mp3"); err != nil {
		return nil, err
	}
	return g, nil
}

// rawGuide is the guide as Suno sang it, stored but not yet analyzed.
type rawGuide struct {
	id     string
	rawKey string
	refKey string
	// instKey is Suno's full instrumental, stored when the stems are split
	// (focus, stems): one instrument is a poor alignment reference. Empty in
	// the mix tier, where the single stem is that track.
	instKey string
	words   []client.AlignedWord
}

// guideKey is guides/<owner>/<project>/<id><suffix>.
func guideKey(payload *model.RenderJobPayload, id, suffix string) (string, error) {
	return client.UserKey("guides", payload.OwnerID, payload.ProjectID, id+suffix)
}

// makeGuide has Suno sing the lyrics over the instrumental, separates the
// vocal, times its words and stores the vocal and the separated
// instrumental (the alignment reference). Every error is on the paid path.
func (w *RenderWorker) makeGuide(ctx context.Context, g client.GuideGenerator, payload *model.RenderJobPayload, mode model.StemMode, track *client.Track, progress func(int, string)) (*rawGuide, error) {
	progress(35, "Adding guide vocals...")
	addTask, err := g.AddVocals(ctx, &client.AddVocalsRequest{
		UploadURL:   track.AudioURL,
		Style:       buildMusicStyle(payload),
		Title:       fmt.Sprintf("%s guide", payload.Brief.Genre),
		Lyrics:      client.SunoLyrics(payload.Vocal.Sections),
		VocalGender: payload.Vocal.Voice.SunoGender(),
	})
	if err != nil {
		return nil, fmt.Errorf("add vocals: %w", err)
	}
	log.Printf("Guide: add-vocals task %s", addTask)
	sung, err := g.WaitMusic(ctx, addTask, w.pollInterval, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("add vocals: %w", err)
	}

	progress(45, "Separating guide vocals...")
	sepTask, err := g.SeparateVocal(ctx, addTask, sung.ID)
	if err != nil {
		return nil, fmt.Errorf("separate vocal: %w", err)
	}
	log.Printf("Guide: separation task %s (add-vocals %s, audio %s)", sepTask, addTask, sung.ID)
	sep, err := g.WaitSeparated(ctx, sepTask, w.pollInterval, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("separate vocal: %w", err)
	}
	if sep.InstrumentalURL == "" {
		return nil, errors.New("separate vocal: no instrumental to align with")
	}

	progress(50, "Timing guide lyrics...")
	words, err := g.TimestampedLyrics(ctx, addTask, sung.ID)
	if err != nil {
		return nil, fmt.Errorf("word timings: %w", err)
	}

	progress(55, "Saving guide...")
	raw := &rawGuide{id: uuid.New().String(), words: words}
	if raw.rawKey, err = guideKey(payload, raw.id, "-ham.mp3"); err != nil {
		return nil, err
	}
	if raw.refKey, err = guideKey(payload, raw.id, "-ref.mp3"); err != nil {
		return nil, err
	}
	files := []struct{ url, key string }{{sep.VocalURL, raw.rawKey}, {sep.InstrumentalURL, raw.refKey}}
	if mode != model.StemModeMix {
		if raw.instKey, err = guideKey(payload, raw.id, "-inst.mp3"); err != nil {
			return nil, err
		}
		files = append(files, struct{ url, key string }{track.AudioURL, raw.instKey})
	}
	for _, f := range files {
		if err := ownerCanWrite(ctx, w.deletionChecker, payload.OwnerID); err != nil {
			return nil, err
		}
		if err := w.transferStem(ctx, f.url, f.key); err != nil {
			return nil, fmt.Errorf("could not store the guide: %w", err)
		}
	}
	return raw, nil
}

// finishGuide aligns and analyzes the stored guide and, when the user's
// range asks for it, moves the stems, the guide and its notes by the same
// semitones. Nothing here fails the render: without analysis the raw guide
// is returned, without a shift the render keeps its key. The stems and the
// returned record use the shifted keys only when every file was shifted.
func (w *RenderWorker) finishGuide(ctx context.Context, payload *model.RenderJobPayload, raw *rawGuide, stems []model.StemResultRecord, bpm int, progress func(int, string)) (*model.GuideRecord, int) {
	progress(92, "Analyzing guide...")
	alignedKey, err := guideKey(payload, raw.id, ".mp3")
	if err != nil {
		return &model.GuideRecord{}, 0
	}
	notesKey, err := guideKey(payload, raw.id, "-notes.json")
	if err != nil {
		return &model.GuideRecord{}, 0
	}
	words := make([]client.GuideWord, 0, len(raw.words))
	for _, aw := range raw.words {
		words = append(words, client.GuideWord{Word: aw.Word, StartS: aw.StartS, EndS: aw.EndS, Success: aw.Success})
	}
	sections := make([]client.GuideSectionInput, 0, len(payload.Vocal.Sections))
	for _, s := range payload.Vocal.Sections {
		lines := s.Lines
		if lines == nil {
			lines = []string{}
		}
		sections = append(sections, client.GuideSectionInput{ID: s.ID, Type: s.Type, Lines: lines})
	}
	instrumentalKey := raw.instKey
	if instrumentalKey == "" {
		instrumentalKey = stems[0].FileKey
	}
	res, err := w.guideAudio.AnalyzeGuide(ctx, &client.GuideAnalyzeRequest{
		GuideKey:        raw.rawKey,
		ReferenceKey:    raw.refKey,
		InstrumentalKey: instrumentalKey,
		Words:           words,
		Sections:        sections,
		VocalGender:     payload.Vocal.Voice.SunoGender(),
		BPM:             float64(bpm),
		AlignedKey:      alignedKey,
		NotesKey:        notesKey,
	})
	if err != nil {
		log.Printf("Guide %s: analysis failed, keeping the raw guide: %v", raw.id, err)
		return &model.GuideRecord{}, 0
	}
	rec := &model.GuideRecord{NotesKey: &notesKey}

	n := ShiftSemitones(payload.Brief, res.RangeMidi, payload.Vocal.Range)
	if n == 0 {
		return rec, 0
	}
	progress(94, "Fitting key to your voice...")
	items := make([]client.PitchShiftItem, 0, len(stems)+1)
	for _, st := range stems {
		items = append(items, client.PitchShiftItem{InputKey: st.FileKey, OutputKey: shiftedKey(st.FileKey, n)})
	}
	items = append(items, client.PitchShiftItem{InputKey: alignedKey, OutputKey: shiftedKey(alignedKey, n)})
	notes := &client.PitchShiftNotes{InputKey: notesKey, OutputKey: shiftedKey(notesKey, n)}
	if err := ownerCanWrite(ctx, w.deletionChecker, payload.OwnerID); err != nil {
		log.Printf("Guide %s: no key shift: %v", raw.id, err)
		return rec, 0
	}
	if err := w.guideAudio.PitchShift(ctx, items, n, notes); err != nil {
		log.Printf("Guide %s: key shift failed, keeping the original key: %v", raw.id, err)
		return rec, 0
	}
	for i := range stems {
		stems[i].FileKey = items[i].OutputKey
	}
	rec.NotesKey = &notes.OutputKey
	rec.PitchShiftSemitones = n
	return rec, n
}

// ShiftSemitones is the key change that brings the guide into the user's
// comfortable range: 0 for a manual key (the user's choice is kept), without
// either range, or when the guide already fits; otherwise the distance
// between the range centres, at most ±4.
func ShiftSemitones(brief model.Brief, guideRange *[2]int, user *model.VocalRange) int {
	if brief.Key.Mode == model.KeyModeManual || guideRange == nil || user == nil {
		return 0
	}
	if guideRange[0] >= user.Low && guideRange[1] <= user.High {
		return 0
	}
	diff := float64(user.Low+user.High)/2 - float64(guideRange[0]+guideRange[1])/2
	n := int(math.Round(diff))
	return max(-maxGuideShift, min(maxGuideShift, n))
}

var tonics = []model.Tonic{
	model.TonicC, model.TonicCSharp, model.TonicD, model.TonicDSharp, model.TonicE, model.TonicF,
	model.TonicFSharp, model.TonicG, model.TonicGSharp, model.TonicA, model.TonicASharp, model.TonicB,
}

// TransposeKey moves a tonic by semitones; an unknown tonic is unchanged.
func TransposeKey(tonic model.Tonic, semitones int) model.Tonic {
	for i, t := range tonics {
		if t == tonic {
			return tonics[((i+semitones)%12+12)%12]
		}
	}
	return tonic
}

// shiftedKey inserts -k<n> (-km<n> for a negative n) before the extension:
// stems/u/p/a.mp3 → stems/u/p/a-km3.mp3. One dot only, as UserKey requires.
func shiftedKey(key string, n int) string {
	tag := fmt.Sprintf("-k%d", n)
	if n < 0 {
		tag = fmt.Sprintf("-km%d", -n)
	}
	dot := strings.LastIndex(key, ".")
	if dot < strings.LastIndex(key, "/") {
		return key + tag
	}
	return key[:dot] + tag + key[dot:]
}
