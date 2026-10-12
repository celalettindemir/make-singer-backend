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
		{"bos mod varsayilan mix", "", nil, StemModeMix, nil, false},
		{"mix", StemModeMix, nil, StemModeMix, nil, false},
		{"stems", StemModeStems, nil, StemModeStems, nil, false},
		{"focus aranjmandaki enstrumanla", StemModeFocus, instrumentPtr(InstrumentDrums), StemModeFocus, instrumentPtr(InstrumentDrums), false},
		{"focus enstrumansiz", StemModeFocus, nil, "", nil, true},
		{"focus aranjmanda olmayan enstruman", StemModeFocus, instrumentPtr(InstrumentPiano), "", nil, true},
		{"mix ile focusInstrument", StemModeMix, instrumentPtr(InstrumentDrums), "", nil, true},
		{"stems ile focusInstrument", StemModeStems, instrumentPtr(InstrumentDrums), "", nil, true},
	}
	for _, d := range cases {
		t.Run(d.name, func(t *testing.T) {
			r := &RenderStartRequest{Arrangement: arrangement, StemMode: d.mode, FocusInstrument: d.focus}
			gotMode, gotFocus, err := r.StemSettings()
			if d.wantErr {
				if !errors.Is(err, ErrInvalidStemSettings) {
					t.Fatalf("ErrGecersizStemAyari bekleniyordu, geldi: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("hata beklenmiyordu: %v", err)
			}
			if gotMode != d.want {
				t.Errorf("mod = %q, beklenen %q", gotMode, d.want)
			}
			if (gotFocus == nil) != (d.wantFocus == nil) || (gotFocus != nil && *gotFocus != *d.wantFocus) {
				t.Errorf("odak = %v, beklenen %v", gotFocus, d.wantFocus)
			}
		})
	}
}
