package service

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/client"
)

func seg(start, end float64, text string) client.TimedSegment {
	return client.TimedSegment{Start: start, End: end, Text: text, AvgLogprob: -0.2, NoSpeechProb: 0.05, CompressionRatio: 1.2}
}

func TestImportLines_DropsHallucinations(t *testing.T) {
	bad1 := seg(8, 9, "hayalet")
	bad1.NoSpeechProb = 0.8
	bad2 := seg(10, 11, "hayalet")
	bad2.AvgLogprob = -1.4
	bad3 := seg(12, 13, "x")
	bad3.CompressionRatio = 3
	tr := &client.TimedTranscription{Segments: []client.TimedSegment{
		seg(0, 2, "Birinci satır,"), bad1, bad2, bad3,
		seg(14, 15, "Altyazı M.K."), seg(16, 17, "Thank you for watching!"),
		seg(18, 20, "İkinci satır"),
	}}
	got := ImportLines(tr)
	if len(got) != 2 || got[0].Text != "Birinci satır" || got[1].Text != "İkinci satır" {
		t.Fatalf("got %+v", got)
	}
	if got[0].StartMs != 0 || got[0].EndMs != 2000 {
		t.Fatalf("times %+v", got[0])
	}
	if ImportLines(nil) != nil {
		t.Fatal("nil input must give nil")
	}
}

func longText() (string, []client.TimedWord) {
	var words []client.TimedWord
	var parts []string
	for i := 0; i < 30; i++ {
		w := "kelime"
		parts = append(parts, w)
		words = append(words, client.TimedWord{Word: w, Start: 10 + float64(i)*0.5, End: 10 + float64(i)*0.5 + 0.4})
	}
	return strings.Join(parts, " "), words // 30*6+29 = 209 runes
}

func TestImportLines_SplitsLongSegmentByWordTimes(t *testing.T) {
	text, words := longText()
	tr := &client.TimedTranscription{
		Segments: []client.TimedSegment{seg(10, 25, text)},
		Words:    words,
	}
	got := ImportLines(tr)
	if len(got) < 3 {
		t.Fatalf("want >=3 lines, got %d", len(got))
	}
	total := 0
	for _, l := range got {
		if utf8.RuneCountInString(l.Text) > maxImportLineRunes {
			t.Fatalf("line too long: %q", l.Text)
		}
		if len(l.Words) == 0 {
			t.Fatalf("line without words: %+v", l)
		}
		if want := int(l.Words[0].Start*1000 + 0.5); l.StartMs != want {
			t.Fatalf("StartMs %d, first word at %d", l.StartMs, want)
		}
		if want := int(l.Words[len(l.Words)-1].End*1000 + 0.5); l.EndMs != want {
			t.Fatalf("EndMs %d, last word end %d", l.EndMs, want)
		}
		total += len(strings.Fields(l.Text))
	}
	if total != 30 {
		t.Fatalf("words lost: %d", total)
	}
}

func TestImportLines_SplitWithoutWordsUsesCharRatio(t *testing.T) {
	text, _ := longText()
	tr := &client.TimedTranscription{Segments: []client.TimedSegment{seg(10, 20, text)}}
	got := ImportLines(tr)
	if len(got) < 3 {
		t.Fatalf("got %d lines", len(got))
	}
	if got[0].StartMs != 10000 || got[len(got)-1].EndMs != 20000 {
		t.Fatalf("span %d..%d", got[0].StartMs, got[len(got)-1].EndMs)
	}
	for i := 1; i < len(got); i++ {
		if got[i].StartMs < got[i-1].EndMs || got[i].StartMs > got[i-1].EndMs+100 {
			t.Fatalf("lines not adjacent at %d: %+v %+v", i, got[i-1], got[i])
		}
		if len(got[i].Words) != 0 {
			t.Fatal("no words expected")
		}
	}
}

func il(text string, s, e int) ImportLine { return ImportLine{Text: text, StartMs: s, EndMs: e} }

func TestBuildImportLyrics_IntroVerseOutro(t *testing.T) {
	r := BuildImportLyrics([]ImportLine{il("a", 5000, 8000), il("b", 9000, 12000)}, 20000, nil)
	if len(r.Structure) != 3 || len(r.Lyrics) != 3 || len(r.Timeline) != 3 {
		t.Fatalf("%+v", r)
	}
	types := []string{"intro", "verse", "outro"}
	spans := [][2]int{{0, 5000}, {5000, 12000}, {12000, 20000}}
	for i := range types {
		if r.Structure[i].Type != types[i] || r.Structure[i].ID != "s"+string(rune('1'+i)) {
			t.Fatalf("structure %+v", r.Structure)
		}
		tl := r.Timeline[i]
		if tl.StartMs != spans[i][0] || tl.EndMs != spans[i][1] || tl.SectionID != r.Structure[i].ID {
			t.Fatalf("timeline %+v", tl)
		}
	}
	if len(r.Lyrics[0].Lines) != 0 || r.Lyrics[0].Lines == nil || len(r.Lyrics[1].Lines) != 2 {
		t.Fatalf("lyrics %+v", r.Lyrics)
	}
	if r.Timeline[1].Lines[1].Index != 1 || *r.Timeline[1].Lines[1].StartMs != 9000 {
		t.Fatalf("lines %+v", r.Timeline[1].Lines)
	}
	if len(r.SectionLines) != 3 || len(r.SectionLines[1]) != 2 {
		t.Fatalf("sectionLines %+v", r.SectionLines)
	}
}

func TestBuildImportLyrics_LongGapStartsNewVerse(t *testing.T) {
	r := BuildImportLyrics([]ImportLine{il("a", 0, 3000), il("b", 12000, 15000)}, 16000, nil)
	if len(r.Structure) != 2 || r.Structure[0].Type != "verse" || r.Structure[1].Type != "verse" {
		t.Fatalf("%+v", r.Structure)
	}
	if r.Timeline[0].EndMs != 12000 || r.Timeline[1].StartMs != 12000 || r.Timeline[1].EndMs != 16000 {
		t.Fatalf("%+v", r.Timeline)
	}
}

func TestBuildImportLyrics_ShortTailExtendsLastSection(t *testing.T) {
	r := BuildImportLyrics([]ImportLine{il("a", 500, 18500)}, 20000, nil)
	if len(r.Structure) != 1 || r.Timeline[0].StartMs != 0 || r.Timeline[0].EndMs != 20000 {
		t.Fatalf("%+v %+v", r.Structure, r.Timeline)
	}
}

func TestBuildImportLyrics_NoBPMUses100(t *testing.T) {
	r := BuildImportLyrics([]ImportLine{il("a", 0, 12000)}, 12000, nil)
	if r.Structure[0].Bars != 5 {
		t.Fatalf("bars %d", r.Structure[0].Bars)
	}
	bpm := 120.0
	r = BuildImportLyrics([]ImportLine{il("a", 0, 12000)}, 12000, &bpm)
	if r.Structure[0].Bars != 6 {
		t.Fatalf("bars %d", r.Structure[0].Bars)
	}
}

func TestBuildImportLyrics_NoLines(t *testing.T) {
	r := BuildImportLyrics(nil, 30000, nil)
	if len(r.Structure) != 1 || r.Structure[0].Type != "verse" || r.Structure[0].ID != "s1" {
		t.Fatalf("%+v", r.Structure)
	}
	if r.Lyrics[0].Lines == nil || len(r.Lyrics[0].Lines) != 0 {
		t.Fatalf("%+v", r.Lyrics)
	}
	if r.Timeline != nil {
		t.Fatal("timeline must be nil")
	}
}

func TestBuildImportLyrics_ContiguousCover(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 20; n++ {
		duration := 30000 + rng.Intn(200000)
		var lines []ImportLine
		pos := rng.Intn(15000)
		for pos < duration-1000 && len(lines) < 12 {
			end := pos + 500 + rng.Intn(6000)
			if end > duration {
				end = duration
			}
			lines = append(lines, il("x", pos, end))
			pos = end + rng.Intn(14000)
		}
		r := BuildImportLyrics(lines, duration, nil)
		if len(r.Timeline) != len(r.Structure) && r.Timeline != nil {
			t.Fatalf("case %d: timeline len", n)
		}
		prev := 0
		secs := r.Timeline
		if secs == nil {
			continue
		}
		for i, s := range secs {
			if s.StartMs != prev || s.EndMs < s.StartMs {
				t.Fatalf("case %d section %d: %+v prev %d", n, i, s, prev)
			}
			prev = s.EndMs
		}
		if prev != duration {
			t.Fatalf("case %d: ends at %d want %d", n, prev, duration)
		}
	}
}
