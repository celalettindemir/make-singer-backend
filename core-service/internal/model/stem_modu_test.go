package model

import (
	"errors"
	"testing"
)

func enstruman(i Instrument) *Instrument { return &i }

func TestStemAyarlari(t *testing.T) {
	aranjman := Arrangement{Instruments: []Instrument{InstrumentDrums, InstrumentBass}}
	durumlar := []struct {
		ad        string
		mod       StemMode
		odak      *Instrument
		beklenen  StemMode
		odakBekle *Instrument
		hata      bool
	}{
		{"bos mod varsayilan mix", "", nil, StemModeMix, nil, false},
		{"mix", StemModeMix, nil, StemModeMix, nil, false},
		{"stems", StemModeStems, nil, StemModeStems, nil, false},
		{"focus aranjmandaki enstrumanla", StemModeFocus, enstruman(InstrumentDrums), StemModeFocus, enstruman(InstrumentDrums), false},
		{"focus enstrumansiz", StemModeFocus, nil, "", nil, true},
		{"focus aranjmanda olmayan enstruman", StemModeFocus, enstruman(InstrumentPiano), "", nil, true},
		{"mix ile focusInstrument", StemModeMix, enstruman(InstrumentDrums), "", nil, true},
		{"stems ile focusInstrument", StemModeStems, enstruman(InstrumentDrums), "", nil, true},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			r := &RenderStartRequest{Arrangement: aranjman, StemMode: d.mod, FocusInstrument: d.odak}
			mod, odak, err := r.StemAyarlari()
			if d.hata {
				if !errors.Is(err, ErrGecersizStemAyari) {
					t.Fatalf("ErrGecersizStemAyari bekleniyordu, geldi: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("hata beklenmiyordu: %v", err)
			}
			if mod != d.beklenen {
				t.Errorf("mod = %q, beklenen %q", mod, d.beklenen)
			}
			if (odak == nil) != (d.odakBekle == nil) || (odak != nil && *odak != *d.odakBekle) {
				t.Errorf("odak = %v, beklenen %v", odak, d.odakBekle)
			}
		})
	}
}
