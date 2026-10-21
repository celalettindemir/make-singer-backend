package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

func instrumentPtr(i Instrument) *Instrument { return &i }

func TestRenderStartRequest_StemSettings(t *testing.T) {
	arrangement := Arrangement{Instruments: []Instrument{InstrumentDrums, InstrumentBass}}
	cases := []struct {
		name      string
		mode      StemMode
		focus     *Instrument
		want      StemMode
		wantFocus *Instrument
		wantErr   bool
	}{
		{"empty mode defaults to mix", "", nil, StemModeMix, nil, false},
		{"mix", StemModeMix, nil, StemModeMix, nil, false},
		{"stems", StemModeStems, nil, StemModeStems, nil, false},
		{"focus with an instrument in the arrangement", StemModeFocus, instrumentPtr(InstrumentDrums), StemModeFocus, instrumentPtr(InstrumentDrums), false},
		{"focus without an instrument", StemModeFocus, nil, "", nil, true},
		{"focus with an instrument not in the arrangement", StemModeFocus, instrumentPtr(InstrumentPiano), "", nil, true},
		{"focusInstrument with mix", StemModeMix, instrumentPtr(InstrumentDrums), "", nil, true},
		{"focusInstrument with stems", StemModeStems, instrumentPtr(InstrumentDrums), "", nil, true},
	}
	for _, d := range cases {
		t.Run(d.name, func(t *testing.T) {
			r := &RenderStartRequest{Arrangement: arrangement, StemMode: d.mode, FocusInstrument: d.focus}
			gotMode, gotFocus, err := r.StemSettings()
			if d.wantErr {
				if !errors.Is(err, ErrInvalidStemSettings) {
					t.Fatalf("expected ErrInvalidStemSettings, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMode != d.want {
				t.Errorf("mode = %q, want %q", gotMode, d.want)
			}
			if (gotFocus == nil) != (d.wantFocus == nil) || (gotFocus != nil && *gotFocus != *d.wantFocus) {
				t.Errorf("focus = %v, want %v", gotFocus, d.wantFocus)
			}
		})
	}
}

func validVocal() *VocalRequest {
	return &VocalRequest{
		Voice:    VoiceFemale,
		Title:    "Song",
		Sections: []GuideSection{{ID: "s1", Type: "verse", Lines: []string{"a b"}}},
	}
}

func TestRenderStartRequest_VocalOptional(t *testing.T) {
	var r RenderStartRequest
	if err := json.Unmarshal([]byte(`{"projectId":"11111111-1111-4111-8111-111111111111","brief":{},"arrangement":{}}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Vocal != nil {
		t.Fatalf("Vocal = %+v, want nil", r.Vocal)
	}
	if err := validator.New().Var(r.Vocal, "omitempty"); err != nil {
		t.Fatalf("nil vocal invalid: %v", err)
	}
}

func TestVocalRequest_Validation(t *testing.T) {
	v := validator.New()
	if err := v.Struct(validVocal()); err != nil {
		t.Fatalf("valid vocal rejected: %v", err)
	}
	cases := map[string]func(g *VocalRequest){
		"own voice":      func(g *VocalRequest) { g.Voice = "own" },
		"no sections":    func(g *VocalRequest) { g.Sections = nil },
		"33 sections":    func(g *VocalRequest) { g.Sections = make([]GuideSection, 33) },
		"201 char line":  func(g *VocalRequest) { g.Sections[0].Lines = []string{strings.Repeat("a", 201)} },
		"81 char title":  func(g *VocalRequest) { g.Title = strings.Repeat("a", 81) },
		"range inverted": func(g *VocalRequest) { g.Range = &VocalRange{Low: 60, High: 60} },
		"range too low":  func(g *VocalRequest) { g.Range = &VocalRange{Low: 10, High: 60} },
		"section no id":  func(g *VocalRequest) { g.Sections[0].ID = "" },
	}
	for name, mutate := range cases {
		g := validVocal()
		mutate(g)
		if err := v.Struct(g); err == nil {
			t.Errorf("%s: accepted, want a validation error", name)
		}
	}
	g := validVocal()
	g.Voice = VoiceMale
	g.Range = &VocalRange{Low: 48, High: 67}
	if err := v.Struct(g); err != nil {
		t.Errorf("valid range rejected: %v", err)
	}
}

func TestRenderStartRequest_VocalIsValidatedInside(t *testing.T) {
	r := RenderStartRequest{ProjectID: "11111111-1111-4111-8111-111111111111", Vocal: validVocal()}
	r.Vocal.Voice = "own"
	err := validator.New().Struct(r)
	if err == nil || !strings.Contains(err.Error(), "Voice") {
		t.Fatalf("err = %v, want a Voice error", err)
	}
}

func TestVoice_SunoGender(t *testing.T) {
	if VoiceFemale.SunoGender() != "f" || VoiceMale.SunoGender() != "m" {
		t.Errorf("SunoGender = %q / %q, want f / m", VoiceFemale.SunoGender(), VoiceMale.SunoGender())
	}
}

func TestVocalRequest_CheckLyrics(t *testing.T) {
	g := validVocal()
	if err := g.CheckLyrics(); err != nil {
		t.Fatalf("valid lyrics rejected: %v", err)
	}
	blank := validVocal()
	blank.Sections = []GuideSection{{ID: "s1", Type: "verse", Lines: []string{"  ", ""}}, {ID: "s2", Type: "chorus"}}
	if err := blank.CheckLyrics(); !errors.Is(err, ErrInvalidLyrics) {
		t.Errorf("blank lyrics: err = %v, want ErrInvalidLyrics", err)
	}
	// Each line counts its runes plus a newline: 24 lines of 199 = 4800, and
	// a last line of total-4801 runes brings the count to exactly total.
	withChars := func(total int) *VocalRequest {
		v := validVocal()
		v.Sections = nil
		for i := 0; i < 24; i++ {
			v.Sections = append(v.Sections, GuideSection{ID: "s", Type: "verse", Lines: []string{strings.Repeat("a", 199)}})
		}
		v.Sections = append(v.Sections, GuideSection{ID: "s", Type: "verse", Lines: []string{strings.Repeat("a", total-4801)}})
		return v
	}
	if err := withChars(5001).CheckLyrics(); !errors.Is(err, ErrInvalidLyrics) {
		t.Errorf("5001 chars: err = %v, want ErrInvalidLyrics (limit %d)", err, MaxGuideLyricsChars)
	}
	if err := withChars(5000).CheckLyrics(); err != nil {
		t.Errorf("5000 chars: err = %v, want ok", err)
	}
}

func TestRenderResult_NullTimelineAndGuide(t *testing.T) {
	b, err := json.Marshal(RenderResultResponse{Stems: []StemResult{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"lyricTimeline":null`) || !strings.Contains(string(b), `"guide":null`) {
		t.Errorf("json = %s, want lyricTimeline null and guide null", b)
	}
	b, _ = json.Marshal(LyricTimelineLine{Index: 1})
	if string(b) != `{"index":1,"startMs":null,"endMs":null}` {
		t.Errorf("line json = %s", b)
	}
}
