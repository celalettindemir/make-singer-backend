package service

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// Thresholds for dropping hallucinated whisper segments and shaping lines.
const (
	maxImportNoSpeechProb   = 0.6
	minImportAvgLogprob     = -1.0
	maxImportCompression    = 2.4
	maxImportLineRunes      = 80
	importWordToleranceSec  = 0.05
	importIntroThresholdMs  = 1000
	importSectionGapMs      = 8000
	importOutroTailMs       = 2000
	defaultImportBPM        = 100.0
	importBeatsPerBarDivide = 240.0 // bars = seconds * bpm / (60 * 4)
)

// importHallucinationPhrases are typical whisper inventions on silence or
// music, matched on foldTR-lowercased text.
var importHallucinationPhrases = func() []string {
	raw := []string{
		"altyazı", "abone ol", "subtitles by", "sous-titres", "sous-titrage",
		"thank you for watching", "merci d'avoir regardé", "izlediğiniz için teşekkür",
	}
	out := make([]string, len(raw))
	for i, p := range raw {
		out[i] = foldTR(p)
	}
	return out
}()

// ImportLine is one lyric line with its time span in the source song.
type ImportLine struct {
	Text    string
	StartMs int
	EndMs   int
	Words   []client.TimedWord
}

// ImportLyrics is the structure, lyrics and timeline built from lines.
// SectionLines holds the lines of each section, parallel to Structure.
type ImportLyrics struct {
	Structure    []model.ImportSection
	Lyrics       []model.ImportLyricsSection
	Timeline     []model.LyricTimelineSection
	SectionLines [][]ImportLine
}

func isImportHallucination(seg client.TimedSegment) bool {
	if seg.NoSpeechProb > maxImportNoSpeechProb || seg.AvgLogprob < minImportAvgLogprob ||
		seg.CompressionRatio > maxImportCompression {
		return true
	}
	folded := foldTR(seg.Text)
	for _, p := range importHallucinationPhrases {
		if strings.Contains(folded, p) {
			return true
		}
	}
	return false
}

func toMs(sec float64) int { return int(math.Round(sec * 1000)) }

func trimTrailingPunct(s string) string {
	return strings.TrimRightFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
}

// ImportLines turns a timed transcript into lyric lines: hallucinated
// segments are dropped and segments longer than maxImportLineRunes are split
// at word boundaries.
func ImportLines(t *client.TimedTranscription) []ImportLine {
	if t == nil {
		return nil
	}
	var out []ImportLine
	for _, seg := range t.Segments {
		if isImportHallucination(seg) {
			continue
		}
		text, ok := cleanLine(seg.Text)
		if !ok {
			continue
		}
		text = trimTrailingPunct(text)
		if text == "" {
			continue
		}
		var words []client.TimedWord
		for _, w := range t.Words {
			if w.Start >= seg.Start-importWordToleranceSec && w.Start < seg.End+importWordToleranceSec {
				words = append(words, w)
			}
		}
		out = append(out, splitImportSegment(seg, text, words)...)
	}
	return out
}

func splitImportSegment(seg client.TimedSegment, text string, words []client.TimedWord) []ImportLine {
	tokens := strings.Fields(text)
	if utf8.RuneCountInString(text) <= maxImportLineRunes || len(tokens) < 2 {
		return []ImportLine{{Text: text, StartMs: toMs(seg.Start), EndMs: max(toMs(seg.Start), toMs(seg.End)), Words: words}}
	}
	aligned := len(words) == len(tokens)
	var ranges [][2]int
	splitTokens(tokens, 0, len(tokens), &ranges)

	total := utf8.RuneCountInString(strings.Join(tokens, " "))
	var out []ImportLine
	offset := 0
	for _, r := range ranges {
		part := strings.Join(tokens[r[0]:r[1]], " ")
		partRunes := utf8.RuneCountInString(part)
		line := ImportLine{Text: trimTrailingPunct(part)}
		if aligned {
			line.Words = words[r[0]:r[1]]
			line.StartMs = toMs(line.Words[0].Start)
			line.EndMs = toMs(line.Words[len(line.Words)-1].End)
		} else {
			span := seg.End - seg.Start
			s := seg.Start + span*float64(offset)/float64(total)
			e := seg.Start + span*float64(offset+partRunes)/float64(total)
			line.StartMs, line.EndMs = toMs(s), toMs(e)
			for _, w := range words {
				if w.Start >= s && (w.Start < e || (r[1] == len(tokens) && w.Start <= e)) {
					line.Words = append(line.Words, w)
				}
			}
		}
		line.EndMs = max(line.StartMs, line.EndMs)
		offset += partRunes + 1
		if line.Text != "" {
			out = append(out, line)
		}
	}
	return out
}

// splitTokens splits tokens[lo:hi] at the word boundary nearest the rune
// midpoint until every part fits, appending token ranges to ranges.
func splitTokens(tokens []string, lo, hi int, ranges *[][2]int) {
	part := strings.Join(tokens[lo:hi], " ")
	total := utf8.RuneCountInString(part)
	if total <= maxImportLineRunes || hi-lo < 2 {
		*ranges = append(*ranges, [2]int{lo, hi})
		return
	}
	best, bestDiff, left := lo+1, math.MaxInt, 0
	for k := lo + 1; k < hi; k++ {
		left += utf8.RuneCountInString(tokens[k-1])
		if k > lo+1 {
			left++
		}
		d := left - total/2
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			best, bestDiff = k, d
		}
	}
	splitTokens(tokens, lo, best, ranges)
	splitTokens(tokens, best, hi, ranges)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// BuildImportLyrics groups lines into contiguous intro/verse/outro sections
// covering [0, durationMs]. bpm nil or non-positive means 100.
func BuildImportLyrics(lines []ImportLine, durationMs int, bpm *float64) ImportLyrics {
	tempo := defaultImportBPM
	if bpm != nil && *bpm > 0 {
		tempo = *bpm
	}
	type span struct {
		typ        string
		start, end int
		lines      []ImportLine
	}
	var spans []span
	if len(lines) == 0 {
		spans = []span{{typ: "verse", start: 0, end: durationMs, lines: []ImportLine{}}}
	} else {
		var groups [][]ImportLine
		cur := []ImportLine{lines[0]}
		for i := 1; i < len(lines); i++ {
			if lines[i].StartMs-lines[i-1].EndMs > importSectionGapMs {
				groups = append(groups, cur)
				cur = nil
			}
			cur = append(cur, lines[i])
		}
		groups = append(groups, cur)

		pos := 0
		if first := lines[0].StartMs; first > importIntroThresholdMs {
			pos = clampInt(first, 0, durationMs)
			spans = append(spans, span{typ: "intro", start: 0, end: pos, lines: []ImportLine{}})
		}
		for gi, g := range groups {
			start := pos
			if gi > 0 {
				start = clampInt(g[0].StartMs, pos, durationMs)
			}
			spans = append(spans, span{typ: "verse", start: start, lines: g})
		}
		// Close each verse at the next section's start.
		for i := range spans {
			if spans[i].typ == "verse" && i+1 < len(spans) {
				spans[i].end = spans[i+1].start
			}
			if spans[i].typ == "intro" {
				spans[i].end = spans[i+1].start
			}
		}
		last := &spans[len(spans)-1]
		lastEnd := clampInt(lines[len(lines)-1].EndMs, last.start, durationMs)
		if durationMs-lastEnd > importOutroTailMs {
			last.end = lastEnd
			spans = append(spans, span{typ: "outro", start: lastEnd, end: durationMs, lines: []ImportLine{}})
		} else {
			last.end = durationMs
		}
	}

	res := ImportLyrics{}
	for i, sp := range spans {
		id := "s" + strconv.Itoa(i+1)
		bars := int(math.Round(float64(sp.end-sp.start) / 1000 * tempo / importBeatsPerBarDivide))
		res.Structure = append(res.Structure, model.ImportSection{ID: id, Type: sp.typ, Bars: max(1, bars)})
		texts := make([]string, len(sp.lines))
		tl := model.LyricTimelineSection{SectionID: id, StartMs: sp.start, EndMs: sp.end, Lines: []model.LyricTimelineLine{}}
		for j, l := range sp.lines {
			texts[j] = l.Text
			s, e := l.StartMs, l.EndMs
			tl.Lines = append(tl.Lines, model.LyricTimelineLine{Index: j, StartMs: &s, EndMs: &e})
		}
		res.Lyrics = append(res.Lyrics, model.ImportLyricsSection{SectionID: id, Lines: texts})
		res.SectionLines = append(res.SectionLines, sp.lines)
		if len(lines) > 0 {
			res.Timeline = append(res.Timeline, tl)
		}
	}
	return res
}
