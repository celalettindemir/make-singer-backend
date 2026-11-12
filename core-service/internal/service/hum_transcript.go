package service

import (
	"strings"
	"unicode"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

const (
	maxHumLines     = 8
	maxHumLineRunes = 200
	minHumWords     = 2
)

var (
	// Pure fillers are never words. They are dropped at line edges, alone or
	// as a chain of at least three repeats ("hmhmhm").
	humPureFillersCommon = []string{
		"hmm", "hm", "mm", "ımm", "ım", "eee", "ee", "aaa", "ooo", "uuu", "yam", "pam", "dum",
	}
	humPureFillersByLang = map[model.Language][]string{
		model.LanguageTR: {"ıı", "ıım"},
		model.LanguageEN: {"um", "uhm", "huh"},
		model.LanguageFR: {"euh", "heu"},
	}
	// Word-like syllables are real words on their own ("la", "ya", "oh", "da"),
	// so they are only filler in a run of two or more filler tokens at a line
	// edge, or as one token of at least three repeats ("nanana"). Doubles such
	// as "baba", "nana", "yaya" stay: the user's words are never altered.
	humWordLike = []string{
		"na", "la", "da", "ta", "pa", "ba", "du", "ya", "nan", "lan", "oh", "ah", "uh", "ha",
	}
)

const (
	kindWord     = iota // a real word: stays
	kindWordLike        // may be filler, only inside an edge run
	kindStrong          // filler wherever it stands at an edge
)

// humFillerSet is the pure filler list for lang; an unknown or empty language
// gets the union of all languages.
func humFillerSet(lang model.Language) map[string]bool {
	set := make(map[string]bool)
	for _, w := range humPureFillersCommon {
		set[humLower(w)] = true
	}
	if extra, ok := humPureFillersByLang[lang]; ok {
		for _, w := range extra {
			set[humLower(w)] = true
		}
		return set
	}
	for _, extra := range humPureFillersByLang {
		for _, w := range extra {
			set[humLower(w)] = true
		}
	}
	return set
}

// humWordKey lowercases a word and drops everything that is not a letter.
func humWordKey(word string) string {
	var b strings.Builder
	for _, r := range word {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return humLower(b.String())
}

// humLower lowercases with Turkish dotted/dotless I rules but, unlike foldTR,
// keeps ı distinct from i so "ım" is filler and the word "im" is not.
func humLower(s string) string {
	return strings.ToLower(strings.NewReplacer("I", "ı", "İ", "i").Replace(s))
}

func repeatsUnit(word, unit string) bool {
	if unit == "" || len(word) < 2*len(unit) || len(word)%len(unit) != 0 {
		return false
	}
	return strings.Repeat(unit, len(word)/len(unit)) == word
}

// repeatsAtLeast3 reports whether word is unit repeated three or more times.
func repeatsAtLeast3(word, unit string) bool {
	return unit != "" && len(word) >= 3*len(unit) && repeatsUnit(word, unit)
}

// humTokenKind classifies one word key as kindWord, kindWordLike or kindStrong.
func humTokenKind(key string, pure map[string]bool) int {
	if key == "" || pure[key] {
		return kindStrong
	}
	for syl := range pure {
		if repeatsAtLeast3(key, syl) {
			return kindStrong
		}
	}
	for _, syl := range humWordLike {
		if repeatsAtLeast3(key, syl) {
			return kindStrong
		}
	}
	if runes := []rune(key); len(runes) >= 3 && repeatsUnit(key, string(runes[0])) {
		return kindStrong
	}
	// hmm, ımm, mm (not "im")
	rest := strings.TrimLeft(key, "hı")
	if len(key)-len(rest) <= 1 && rest != "" && strings.Trim(rest, "m") == "" {
		return kindStrong
	}
	for _, syl := range humWordLike {
		if key == syl {
			return kindWordLike
		}
	}
	return kindWord
}

// humEdgeRun is how many leading tokens to drop as filler: strong fillers
// (hmm, ımm, nanana) and runs of the SAME word-like syllable ("na na",
// "la la la"). Two different syllables ("ya da", "ta da") are words and stay.
func humEdgeRun(keys []string, kinds []int) int {
	i := 0
	for i < len(kinds) {
		if kinds[i] == kindStrong {
			i++
			continue
		}
		if kinds[i] != kindWordLike {
			break
		}
		j := i
		for j < len(kinds) && kinds[j] == kindWordLike && keys[j] == keys[i] {
			j++
		}
		if j-i < 2 {
			break
		}
		i = j
	}
	return i
}

// splitHumSentences breaks free text at . ! ? … ; and line ends; commas stay.
func splitHumSentences(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '.', '!', '?', '…', ';', '\n', '\r':
			return true
		}
		return false
	})
}

// HumLines turns a hum transcript into lyric lines for the lyricist. Filler
// words and syllables ("nanana", "ımm") are dropped at the edges of each line;
// fewer than minHumWords meaningful words in total means nil (no suggestion).
func HumLines(t *client.Transcription, lang model.Language) []string {
	if t == nil {
		return nil
	}
	raws := t.Segments
	if len(raws) == 0 {
		raws = splitHumSentences(t.Text)
	}
	fillers := humFillerSet(lang)

	var lines []string
	meaningful := 0
	for _, raw := range raws {
		for _, part := range splitHumSentences(raw) {
			text, ok := cleanLine(part)
			if !ok {
				continue
			}
			words := strings.Fields(text)
			keys := make([]string, len(words))
			kinds := make([]int, len(words))
			for i, w := range words {
				keys[i] = humWordKey(w)
				kinds[i] = humTokenKind(keys[i], fillers)
			}
			start := humEdgeRun(keys, kinds)
			rk, rkinds := make([]string, 0, len(keys)), make([]int, 0, len(keys))
			for i := len(keys) - 1; i >= start; i-- {
				rk, rkinds = append(rk, keys[i]), append(rkinds, kinds[i])
			}
			end := len(words) - humEdgeRun(rk, rkinds)
			if start >= end {
				continue
			}
			count := 0
			for i := start; i < end; i++ {
				if kinds[i] != kindStrong && len([]rune(humWordKey(words[i]))) >= 2 {
					count++
				}
			}
			if count == 0 {
				continue
			}
			line := strings.TrimRight(strings.Join(words[start:end], " "), " ,.;:!?…-–—")
			if r := []rune(line); len(r) > maxHumLineRunes {
				line = strings.TrimSpace(string(r[:maxHumLineRunes]))
			}
			if line == "" {
				continue
			}
			meaningful += count
			lines = append(lines, line)
			if len(lines) == maxHumLines {
				break
			}
		}
		if len(lines) == maxHumLines {
			break
		}
	}
	if meaningful < minHumWords {
		return nil
	}
	return lines
}
