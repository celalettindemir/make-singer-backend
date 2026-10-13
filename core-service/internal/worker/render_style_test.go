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
