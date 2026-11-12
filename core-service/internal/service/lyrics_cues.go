package service

import (
	"sort"
	"strings"
	"unicode"

	"github.com/makeasinger/api/internal/model"
)

// maxCuesPerLine caps the cues kept on one line (first three in syllable order).
const maxCuesPerLine = 3

const (
	vowelsTR = "aeıioöuüâîûAEIİOÖUÜÂÎÛ"
	vowelsEN = "aeiouyAEIOUY"
	vowelsFR = "aeiouyàâäéèêëîïôöùûüÿœæAEIOUYÀÂÄÉÈÊËÎÏÔÖÙÛÜŸŒÆ"
)

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || r == '\'' || r == '’'
}

// wordNuclei returns the first-vowel indices (relative to the word) of a word's
// syllables for the given language.
func wordNuclei(word []rune, lang model.Language) []int {
	if lang == model.LanguageTR {
		var out []int
		for i, r := range word {
			if strings.ContainsRune(vowelsTR, r) {
				out = append(out, i)
			}
		}
		return out
	}
	vowels := vowelsEN
	if lang == model.LanguageFR {
		vowels = vowelsFR
	}
	var out []int
	for i := 0; i < len(word); i++ {
		if !strings.ContainsRune(vowels, word[i]) {
			continue
		}
		out = append(out, i)
		for i+1 < len(word) && strings.ContainsRune(vowels, word[i+1]) {
			i++
		}
	}
	if len(out) < 2 {
		return out
	}
	// Silent final e (en) / mute final e or es (fr): a lone e right after a consonant.
	last := out[len(out)-1]
	n := len(word)
	isE := func(r rune) bool { return r == 'e' || r == 'E' }
	isCons := func(r rune) bool { return unicode.IsLetter(r) && !strings.ContainsRune(vowels, r) }
	silent := false
	switch {
	case last == n-1 && isE(word[last]) && isCons(word[last-1]):
		silent = true
	case lang == model.LanguageFR && last == n-2 && isE(word[last]) &&
		(word[n-1] == 's' || word[n-1] == 'S') && last > 0 && isCons(word[last-1]):
		silent = true
	}
	if silent {
		out = out[:len(out)-1]
	}
	return out
}

// syllableNuclei returns the Unicode scalar index of the first vowel of every
// syllable in text, ascending. An empty lang counts as English.
func syllableNuclei(text string, lang model.Language) []int {
	if lang == "" {
		lang = model.LanguageEN
	}
	runes := []rune(text)
	var out []int
	for i := 0; i < len(runes); {
		if !isWordRune(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && isWordRune(runes[j]) {
			j++
		}
		for _, n := range wordNuclei(runes[i:j], lang) {
			out = append(out, i+n)
		}
		i = j
	}
	return out
}

type cueWord struct {
	text               string
	hold, rise, breath bool
}

func kindOrder(k string) int {
	switch k {
	case model.CueHold:
		return 0
	case model.CueRise:
		return 1
	}
	return 2
}

// parseCueLine strips the cue marks (~ hold, ^ rise, | breath) from a cleaned
// line and returns the clean text with its cues. It reports false only when no
// text is left. It never fails otherwise.
func parseCueLine(raw string, lang model.Language) (model.LyricLine, bool) {
	var words []cueWord
	pendingRise := false
	for _, part := range strings.Fields(raw) {
		w := strings.Map(func(r rune) rune {
			if r == '^' || r == '~' || r == '|' {
				return -1
			}
			return r
		}, part)
		hold := strings.Contains(part, "~")
		rise := strings.Contains(part, "^")
		breath := strings.Contains(part, "|")
		leadBar, trailBar := strings.HasPrefix(part, "|"), strings.HasSuffix(part, "|")
		if w == "" {
			// A standalone mark: "~" and "|" belong to the previous word, "^" to the next.
			if len(words) > 0 {
				if breath {
					words[len(words)-1].breath = true
				}
				if hold {
					words[len(words)-1].hold = true
				}
			}
			if rise {
				pendingRise = true
			}
			continue
		}
		cw := cueWord{text: w, hold: hold, rise: rise || pendingRise}
		pendingRise = false
		if breath {
			if leadBar && len(words) > 0 {
				words[len(words)-1].breath = true
			}
			if trailBar || !leadBar {
				cw.breath = true
			}
		}
		words = append(words, cw)
	}
	if len(words) == 0 {
		return model.LyricLine{}, false
	}
	texts := make([]string, len(words))
	starts := make([]int, len(words))
	pos := 0
	for i, w := range words {
		texts[i] = w.text
		starts[i] = pos
		pos += len([]rune(w.text)) + 1
	}
	text := strings.Join(texts, " ")
	if isSectionLabel(text) {
		return model.LyricLine{}, false
	}
	nuclei := syllableNuclei(text, lang)

	var cues []model.LyricCue
	seen := map[[2]int]bool{}
	add := func(kind string, syl int) {
		key := [2]int{kindOrder(kind), syl}
		if seen[key] {
			return
		}
		seen[key] = true
		cues = append(cues, model.LyricCue{SyllableIndex: syl, Kind: kind, At: nuclei[syl]})
	}
	for i, w := range words {
		end := starts[i] + len([]rune(w.text))
		first, last := -1, -1
		for s, at := range nuclei {
			if at >= starts[i] && at < end {
				if first < 0 {
					first = s
				}
				last = s
			}
		}
		if first < 0 {
			continue
		}
		if w.hold {
			add(model.CueHold, last)
		}
		if w.rise {
			add(model.CueRise, first)
		}
		if w.breath && last != len(nuclei)-1 {
			add(model.CueBreath, last)
		}
	}
	sort.Slice(cues, func(a, b int) bool {
		if cues[a].SyllableIndex != cues[b].SyllableIndex {
			return cues[a].SyllableIndex < cues[b].SyllableIndex
		}
		return kindOrder(cues[a].Kind) < kindOrder(cues[b].Kind)
	})
	if len(cues) > maxCuesPerLine {
		cues = cues[:maxCuesPerLine]
	}
	if cues == nil {
		cues = []model.LyricCue{}
	}
	return model.LyricLine{Text: text, Cues: cues}, true
}
