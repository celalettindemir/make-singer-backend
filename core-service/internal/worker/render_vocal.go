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

// WithGuideAudio turns on the vocal analysis (Plus): audio-service aligns
// the vocal, makes its note strip and fits the key to the user's range.
// Without it a vocal render still runs, without the analysis.
func (w *RenderWorker) WithGuideAudio(a client.GuideAudio) *RenderWorker {
	w.guideAudio = a
	return w
}

// vocalSeparator returns the Suno separation calls for a vocal payload, or
// an error when the vocal render cannot run. Checked BEFORE the paid call.
// A missing audio-service is not an error: the analysis is skipped.
func (w *RenderWorker) vocalSeparator(payload *model.RenderJobPayload) (client.VocalSeparator, error) {
	if payload.Vocal == nil {
		return nil, nil
	}
	v, ok := w.music.(client.VocalSeparator)
	if !ok {
		return nil, errors.New("the vocal render is not configured")
	}
	if _, err := guideKey(payload, "x", ".mp3"); err != nil {
		return nil, err
	}
	return v, nil
}

// analyzeVocal reports whether the vocal render gets the analysis and key
// shift: the user's plan includes it and audio-service is configured.
func (w *RenderWorker) analyzeVocal(payload *model.RenderJobPayload) bool {
	return payload.Vocal != nil && payload.GuideAnalysis && w.guideAudio != nil
}

// guideKey is guides/<owner>/<project>/<id><suffix>.
func guideKey(payload *model.RenderJobPayload, id, suffix string) (string, error) {
	return client.UserKey("guides", payload.OwnerID, payload.ProjectID, id+suffix)
}

// separate starts a separate_vocal task, waits for it and requires both
// halves. Every error is on the paid path.
func (w *RenderWorker) separate(ctx context.Context, vs client.VocalSeparator, start func() (string, error), source string) (*client.SeparatedVocal, error) {
	sepTask, err := start()
	if err != nil {
		return nil, fmt.Errorf("separate vocal: %w", err)
	}
	log.Printf("Render: vocal separation task %s (%s)", sepTask, source)
	sep, err := vs.WaitSeparated(ctx, sepTask, w.pollInterval, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("separate vocal: %w", err)
	}
	if sep.InstrumentalURL == "" {
		return nil, errors.New("separate vocal: no instrumental")
	}
	return sep, nil
}

// vocalTake is the vocal stem as stored under guides/, before analysis.
type vocalTake struct {
	id  string
	key string
	// instKey is Suno's full track, stored in the stems tier for the
	// analysis: one instrument is a poor alignment reference. Empty
	// otherwise.
	instKey string
}

// storeVocal stores the vocal at guides/<owner>/<project>/<id>.mp3 and, when
// fullURL is set, Suno's full track at <id>-inst.mp3. The owner is checked
// before every write.
func (w *RenderWorker) storeVocal(ctx context.Context, payload *model.RenderJobPayload, vocalURL, fullURL string) (*vocalTake, error) {
	take := &vocalTake{id: uuid.New().String()}
	var err error
	if take.key, err = guideKey(payload, take.id, ".mp3"); err != nil {
		return nil, err
	}
	files := []struct{ url, key string }{{vocalURL, take.key}}
	if fullURL != "" {
		if take.instKey, err = guideKey(payload, take.id, "-inst.mp3"); err != nil {
			return nil, err
		}
		files = append(files, struct{ url, key string }{fullURL, take.instKey})
	}
	for _, f := range files {
		if err := ownerCanWrite(ctx, w.deletionChecker, payload.OwnerID); err != nil {
			return nil, err
		}
		if err := w.transferStem(ctx, f.url, f.key); err != nil {
			return nil, fmt.Errorf("could not store the vocal: %w", err)
		}
	}
	return take, nil
}

// finishVocal aligns and analyzes the stored vocal (stems[0]) against ref
// and, when the user's range asks for it, moves every stem (the vocal too)
// and the notes by the same semitones. Nothing here fails the render:
// without analysis the raw vocal stays, without a shift the render keeps its
// key. The stems use the shifted keys only when every file was shifted.
func (w *RenderWorker) finishVocal(ctx context.Context, payload *model.RenderJobPayload, take *vocalTake, ref string, words []client.AlignedWord, stems []model.StemResultRecord, bpm int, progress func(int, string)) (*model.GuideRecord, int) {
	progress(92, "Analyzing vocal...")
	alignedKey, err := guideKey(payload, take.id, "-aligned.mp3")
	if err != nil {
		return &model.GuideRecord{}, 0
	}
	notesKey, err := guideKey(payload, take.id, "-notes.json")
	if err != nil {
		return &model.GuideRecord{}, 0
	}
	gw := make([]client.GuideWord, 0, len(words))
	for _, aw := range words {
		gw = append(gw, client.GuideWord(aw))
	}
	sections := make([]client.GuideSectionInput, 0, len(payload.Vocal.Sections))
	for _, s := range payload.Vocal.Sections {
		lines := s.Lines
		if lines == nil {
			lines = []string{}
		}
		sections = append(sections, client.GuideSectionInput{ID: s.ID, Type: s.Type, Lines: lines})
	}
	res, err := w.guideAudio.AnalyzeGuide(ctx, &client.GuideAnalyzeRequest{
		GuideKey:        take.key,
		ReferenceKey:    ref,
		InstrumentalKey: ref,
		Words:           gw,
		Sections:        sections,
		VocalGender:     payload.Vocal.Voice.SunoGender(),
		BPM:             float64(bpm),
		AlignedKey:      alignedKey,
		NotesKey:        notesKey,
	})
	if err != nil {
		log.Printf("Vocal %s: analysis failed, keeping the raw vocal: %v", take.id, err)
		return &model.GuideRecord{}, 0
	}
	stems[0].FileKey = alignedKey
	rec := &model.GuideRecord{NotesKey: &notesKey}

	n := ShiftSemitones(payload.Brief, res.RangeMidi, payload.Vocal.Range)
	if n == 0 {
		return rec, 0
	}
	progress(94, "Fitting key to your voice...")
	items := make([]client.PitchShiftItem, 0, len(stems))
	for _, st := range stems {
		items = append(items, client.PitchShiftItem{InputKey: st.FileKey, OutputKey: shiftedKey(st.FileKey, n)})
	}
	notes := &client.PitchShiftNotes{InputKey: notesKey, OutputKey: shiftedKey(notesKey, n)}
	if err := ownerCanWrite(ctx, w.deletionChecker, payload.OwnerID); err != nil {
		log.Printf("Vocal %s: no key shift: %v", take.id, err)
		return rec, 0
	}
	if err := w.guideAudio.PitchShift(ctx, items, n, notes); err != nil {
		log.Printf("Vocal %s: key shift failed, keeping the original key: %v", take.id, err)
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
