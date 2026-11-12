package worker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/model"
)

func intp(v int) *int { return &v }

func TestBuildMusicStyle(t *testing.T) {
	tonic, scale := model.Tonic("A"), model.Scale("minor")
	full := &model.RenderJobPayload{
		Brief: model.Brief{
			Genre: model.Genre("pop"), Vibes: []string{"huzunlu", " umutlu "},
			BPM: model.BPMConfig{Value: intp(96)},
			Key: model.KeyConfig{Tonic: &tonic, Scale: &scale},
		},
		Arrangement: model.Arrangement{
			Instruments: []model.Instrument{model.InstrumentPiano, model.InstrumentDrums},
			Density:     model.Density("medium"), Groove: model.Groove("half_time"),
		},
	}
	got := buildMusicStyle(full)
	want := "pop, instrumental, 96 BPM, A minor, huzunlu, umutlu, medium arrangement, half time groove, featuring piano, drums"
	if got != want {
		t.Errorf("tam:\n got %q\nwant %q", got, want)
	}

	minimal := &model.RenderJobPayload{Brief: model.Brief{Genre: model.Genre("rock"), Vibes: []string{"x"}}}
	if got := buildMusicStyle(minimal); got != "rock, instrumental, x" {
		t.Errorf("minimal = %q", got)
	}

	long := &model.RenderJobPayload{Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{strings.Repeat("ş", 900)}}}
	got = buildMusicStyle(long)
	if len(got) > 1000 || !utf8.ValidString(got) {
		t.Errorf("long style %d bytes, valid UTF-8: %v", len(got), utf8.ValidString(got))
	}
}

func TestBuildMusicStyle_VocalHasNoInstrumentalWord(t *testing.T) {
	p := &model.RenderJobPayload{
		Brief: model.Brief{Genre: model.Genre("rock"), Vibes: []string{"x"}},
		Vocal: &model.VocalRequest{Voice: model.VoiceMale},
	}
	if got := buildMusicStyle(p); got != "rock, x" {
		t.Errorf("vocal style = %q, want %q", got, "rock, x")
	}
}

func TestBuildMusicStyle_HumDropsBPMAndKey(t *testing.T) {
	v, tonic, scale := 120, model.Tonic("C"), model.Scale("major")
	p := &model.RenderJobPayload{Brief: model.Brief{
		Genre: "pop", Vibes: []string{"dreamy"},
		BPM: model.BPMConfig{Mode: model.BPMModeFixed, Value: &v},
		Key: model.KeyConfig{Tonic: &tonic, Scale: &scale},
	}, Arrangement: model.Arrangement{Instruments: []model.Instrument{model.InstrumentPiano}}}
	if got := buildMusicStyle(p); !strings.Contains(got, "120 BPM") || !strings.Contains(got, "C major") {
		t.Fatalf("control style %q must have BPM and key", got)
	}
	p.Hum = &model.HumJob{Key: "hums/u/p/h.mp3", Fidelity: model.HumFree}
	got := buildMusicStyle(p)
	if strings.Contains(got, "BPM") || strings.Contains(got, "C major") {
		t.Errorf("hum style %q still has BPM or key", got)
	}
	if !strings.Contains(got, "dreamy") || !strings.Contains(got, "featuring piano") {
		t.Errorf("hum style %q lost other parts", got)
	}
}
