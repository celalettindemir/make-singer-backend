package model

import (
	"slices"
	"testing"
)

func TestLyricsJobRequest_GenerateForCarriesHumLines(t *testing.T) {
	r := &LyricsJobRequest{HumLines: []string{"Oy şeftaşım", "Sarı sarı"}}
	song := []LyricsSongSection{{Type: SectionVerse, Lyrics: "x"}}
	sec := LyricsJobSection{Index: 0}
	if got := r.GenerateFor(sec, song).HumLines; !slices.Equal(got, r.HumLines) {
		t.Errorf("GenerateFor HumLines = %v", got)
	}
	if got := r.RewriteFor(sec, song).HumLines; !slices.Equal(got, r.HumLines) {
		t.Errorf("RewriteFor HumLines = %v", got)
	}
}
