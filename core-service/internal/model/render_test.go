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

func validGuide() *GuideRequest {
	return &GuideRequest{
		VocalGender: VocalGenderFemale,
		Sections:    []GuideSection{{ID: "s1", Type: "verse", Lines: []string{"a b"}}},
	}
}

func TestRenderStartRequest_GuideOptional(t *testing.T) {
	var r RenderStartRequest
	if err := json.Unmarshal([]byte(`{"projectId":"11111111-1111-4111-8111-111111111111","brief":{},"arrangement":{}}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Guide != nil {
		t.Fatalf("Guide = %+v, want nil", r.Guide)
	}
	if err := validator.New().Var(r.Guide, "omitempty"); err != nil {
		t.Fatalf("nil guide invalid: %v", err)
	}
}

func TestGuideRequest_Validation(t *testing.T) {
	v := validator.New()
	if err := v.Struct(validGuide()); err != nil {
		t.Fatalf("valid guide rejected: %v", err)
	}
	cases := map[string]func(g *GuideRequest){
		"bad gender":     func(g *GuideRequest) { g.VocalGender = "x" },
		"no sections":    func(g *GuideRequest) { g.Sections = nil },
		"too many":       func(g *GuideRequest) { g.Sections = make([]GuideSection, 33) },
		"range inverted": func(g *GuideRequest) { g.Range = &VocalRange{Low: 60, High: 60} },
		"range too low":  func(g *GuideRequest) { g.Range = &VocalRange{Low: 10, High: 60} },
		"section no id":  func(g *GuideRequest) { g.Sections[0].ID = "" },
	}
	for name, mutate := range cases {
		g := validGuide()
		mutate(g)
		if err := v.Struct(g); err == nil {
			t.Errorf("%s: accepted, want a validation error", name)
		}
	}
	g := validGuide()
	g.Range = &VocalRange{Low: 48, High: 67}
	if err := v.Struct(g); err != nil {
		t.Errorf("valid range rejected: %v", err)
	}
}

func TestRenderStartRequest_GuideIsValidatedInside(t *testing.T) {
	r := RenderStartRequest{ProjectID: "11111111-1111-4111-8111-111111111111", Guide: validGuide()}
	r.Guide.VocalGender = "x"
	err := validator.New().Struct(r)
	if err == nil || !strings.Contains(err.Error(), "VocalGender") {
		t.Fatalf("err = %v, want a VocalGender error", err)
	}
}
