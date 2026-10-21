package worker

import (
	"math"
	"strings"
	"unicode"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

const (
	// timelineSearchWords is how far past the cursor a line's first token is
	// looked for.
	timelineSearchWords = 40
	// timelineWindowSlack is added to a line's token count to get the window
	// its tokens must be found in.
	timelineWindowSlack = 3
	// timelineMinMatchPercent is the share of a line's tokens that must be
	// found, in order, for the line to count as matched.
	timelineMinMatchPercent = 60
)

type timelineWord struct {
	token        string
	startS, endS float64
}

type timelineLine struct {
	section int
	index   int
	tokens  []string
}

// normalizeToken lower-cases with Turkish rules (İ→i, I→ı) and drops every
// character that is not a letter or digit.
func normalizeToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'İ':
			r = 'i'
		case 'I':
			r = 'ı'
		default:
			r = unicode.ToLower(r)
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// timelineWords keeps Suno's successfully timed, non-empty, non-tag words.
func timelineWords(words []client.AlignedWord) []timelineWord {
	var out []timelineWord
	for _, w := range words {
		if !w.Success || strings.HasPrefix(strings.TrimSpace(w.Word), "[") {
			continue
		}
		if tok := normalizeToken(w.Word); tok != "" {
			out = append(out, timelineWord{tok, w.StartS, w.EndS})
		}
	}
	return out
}

func timelineMs(s float64) int { return int(math.Round(s * 1000)) }

// matchLine looks for the line in words[cursor:]. It returns the first and
// last matched word indexes, or ok=false.
func matchLine(tokens []string, words []timelineWord, cursor int) (first, last int, ok bool) {
	searchEnd := min(cursor+timelineSearchWords, len(words))
	start := -1
	for i := cursor; i < searchEnd; i++ {
		if words[i].token == tokens[0] {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	windowEnd := min(start+len(tokens)+timelineWindowSlack, len(words))
	need := (len(tokens)*timelineMinMatchPercent + 99) / 100 // ceil
	found, pos := 0, start
	for _, tok := range tokens {
		for j := pos; j < windowEnd; j++ {
			if words[j].token == tok {
				found++
				last = j
				pos = j + 1
				break
			}
		}
	}
	return start, last, found >= need
}

// BuildLyricTimeline maps Suno's word timings to the user's sections and
// lines. Words are consumed in song order, so repeats Suno added that are
// not in the lyrics (an extra chorus) are left over and ignored. Lines Suno
// did not sing keep nil times; sections with no matched line are dropped.
// It returns nil when the mapping is unreliable (fewer than half of the
// sections with lyrics matched, or no usable words).
func BuildLyricTimeline(sections []model.GuideSection, words []client.AlignedWord, durationS float64) []model.LyricTimelineSection {
	tw := timelineWords(words)
	if len(tw) == 0 {
		return nil
	}

	var lines []timelineLine
	withLines := 0
	for si, sec := range sections {
		has := false
		for li, text := range sec.Lines {
			var tokens []string
			for _, f := range strings.Fields(text) {
				if tok := normalizeToken(f); tok != "" {
					tokens = append(tokens, tok)
				}
			}
			if len(tokens) > 0 {
				lines = append(lines, timelineLine{si, li, tokens})
				has = true
			}
		}
		if has {
			withLines++
		}
	}

	perSection := make([][]model.LyricTimelineLine, len(sections))
	cursor := 0
	for _, ln := range lines {
		tl := model.LyricTimelineLine{Index: ln.index}
		if first, last, ok := matchLine(ln.tokens, tw, cursor); ok {
			start, end := timelineMs(tw[first].startS), timelineMs(tw[last].endS)
			tl.StartMs, tl.EndMs = &start, &end
			cursor = last + 1
		}
		perSection[ln.section] = append(perSection[ln.section], tl)
	}

	var out []model.LyricTimelineSection
	for si, tls := range perSection {
		start := -1
		for _, tl := range tls {
			if tl.StartMs != nil {
				start = *tl.StartMs
				break
			}
		}
		if start < 0 {
			continue
		}
		out = append(out, model.LyricTimelineSection{SectionID: sections[si].ID, StartMs: start, Lines: tls})
	}
	if len(out) == 0 || len(out)*2 < withLines {
		return nil
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].EndMs = out[i+1].StartMs
		} else {
			out[i].EndMs = timelineMs(durationS)
		}
	}
	return out
}
