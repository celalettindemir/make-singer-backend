package service

import (
	"reflect"
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func TestParseCueLine_TurkishMarks(t *testing.T) {
	l, ok := parseCueLine("^Gece boyunca | şehir ışıkları~", model.LanguageTR)
	if !ok || l.Text != "Gece boyunca şehir ışıkları" {
		t.Fatalf("got %q ok=%v", l.Text, ok)
	}
	want := []model.LyricCue{
		{SyllableIndex: 0, Kind: model.CueRise, At: 1},
		{SyllableIndex: 4, Kind: model.CueBreath, At: 11},
		{SyllableIndex: 10, Kind: model.CueHold, At: 26},
	}
	if !reflect.DeepEqual(l.Cues, want) {
		t.Fatalf("cues %+v", l.Cues)
	}
}

func TestParseCueLine_EnglishSilentE(t *testing.T) {
	l, _ := parseCueLine("Feeling like we own the night~", model.LanguageEN)
	// nuclei: ee(1) i(4) i(9) e(14) o(16) e(22) i(25): "like" has no final-e syllable
	if !reflect.DeepEqual(l.Cues, []model.LyricCue{{SyllableIndex: 6, Kind: model.CueHold, At: 25}}) {
		t.Fatal(l.Cues)
	}
}

func TestParseCueLine_FrenchMuteE(t *testing.T) {
	l, _ := parseCueLine("Je t'aime encore~", model.LanguageFR)
	if l.Text != "Je t'aime encore" ||
		!reflect.DeepEqual(l.Cues, []model.LyricCue{{SyllableIndex: 3, Kind: model.CueHold, At: 13}}) {
		t.Fatal(l)
	}
}

func TestParseCueLine_TurkishCapitalsAndApostrophe(t *testing.T) {
	l, _ := parseCueLine("İstanbul'un ışıkları~", model.LanguageTR)
	// İ(0) a(3) u(6) u(9) ı(12) ı(14) a(17) ı(19): scalar indices, not bytes
	if !reflect.DeepEqual(l.Cues, []model.LyricCue{{SyllableIndex: 7, Kind: model.CueHold, At: 19}}) {
		t.Fatal(l.Cues)
	}
}

func TestParseCueLine_NoMarksEmptyCues(t *testing.T) {
	l, ok := parseCueLine("Sen yanımdayken her yol bize çıkar", model.LanguageTR)
	if !ok || l.Cues == nil || len(l.Cues) != 0 {
		t.Fatal(l)
	}
}

func TestParseCueLine_MarkOnlyLineIsDropped(t *testing.T) {
	for _, raw := range []string{"|", "~", "^ ~ |"} {
		if _, ok := parseCueLine(raw, model.LanguageTR); ok {
			t.Fatalf("%q kept", raw)
		}
	}
}

func TestParseCueLine_BreathAtLineEndDropped(t *testing.T) {
	l, _ := parseCueLine("seni sevdim |", model.LanguageTR)
	if l.Text != "seni sevdim" || len(l.Cues) != 0 {
		t.Fatal(l)
	}
}

func TestParseCueLine_CapsAtThreeInSyllableOrder(t *testing.T) {
	l, _ := parseCueLine("^bir~ ^iki~ üç~ dört", model.LanguageTR)
	want := []model.LyricCue{
		{SyllableIndex: 0, Kind: model.CueHold, At: 1},
		{SyllableIndex: 0, Kind: model.CueRise, At: 1},
		{SyllableIndex: 1, Kind: model.CueRise, At: 4},
	}
	if !reflect.DeepEqual(l.Cues, want) {
		t.Fatal(l.Cues)
	}
}

func TestParseCueLine_DuplicateAndVowellessMarksDropped(t *testing.T) {
	l, _ := parseCueLine("sevdim~~ ^ç gel", model.LanguageTR) // "ç" has no vowel
	if l.Text != "sevdim ç gel" || len(l.Cues) != 1 || l.Cues[0].Kind != model.CueHold {
		t.Fatal(l)
	}
}

func TestParseCueLine_SpacedAndLeadingMarks(t *testing.T) {
	cases := []struct {
		raw  string
		want []model.LyricCue
	}{
		{"ışıkları ~", []model.LyricCue{{SyllableIndex: 3, Kind: model.CueHold, At: 7}}},
		{"^ Gece boyunca", []model.LyricCue{{SyllableIndex: 0, Kind: model.CueRise, At: 1}}},
		{"|Seni sevdim", []model.LyricCue{}},
	}
	for _, c := range cases {
		l, ok := parseCueLine(c.raw, model.LanguageTR)
		if !ok || !reflect.DeepEqual(l.Cues, c.want) {
			t.Errorf("%q: ok=%v cues=%+v want %+v", c.raw, ok, l.Cues, c.want)
		}
	}
	l, _ := parseCueLine("Gece |boyunca| şehir", model.LanguageTR)
	kinds := map[int]bool{}
	for _, c := range l.Cues {
		if c.Kind == model.CueBreath {
			kinds[c.SyllableIndex] = true
		}
	}
	if !kinds[1] || !kinds[4] {
		t.Errorf("|boyunca| should breathe after Gece (1) and boyunca (4): %+v", l.Cues)
	}
}

func TestParseCueLine_MarkedSectionLabelDropped(t *testing.T) {
	for _, raw := range []string{"Nakarat~", "^Verse", "Chorus |"} {
		if l, ok := parseCueLine(raw, model.LanguageTR); ok {
			t.Errorf("%q kept as %q", raw, l.Text)
		}
	}
}
