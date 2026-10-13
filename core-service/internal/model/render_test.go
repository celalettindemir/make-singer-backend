package model

import (
	"errors"
	"testing"
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
