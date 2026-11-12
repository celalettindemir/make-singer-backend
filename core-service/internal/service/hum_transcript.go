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
	humFillersCommon = []string{
		"na", "la", "da", "ta", "pa", "ba", "du", "dum", "ya", "yam", "pam", "nan", "lan",
		"hmm", "hm", "mm", "ımm", "ım", "eee", "ee", "aaa", "ooo", "uuu", "oh", "ah", "uh", "ha",
	}
	humFillersByLang = map[model.Language][]string{
		model.LanguageTR: {"ıı", "ıım"},
		model.LanguageEN: {"um", "uhm", "huh"},
		model.LanguageFR: {"euh", "heu"},
	}
	// Syllables that are real words when doubled ("baba", "papa") need three
	// repeats before they count as filler.
	humWordSyllables = map[string]bool{"ba": true, "pa": true}
)

// humFillerSet is the filler list for lang; an unknown or empty language
// gets the union of all languages.
func humFillerSet(lang model.Language) map[string]bool {
	set := make(map[string]bool)
	for _, w := range humFillersCommon {
		set[humLower(w)] = true
	}
	if extra, ok := humFillersByLang[lang]; ok {
		for _, w := range extra {
			set[humLower(w)] = true
		}
		return set
	}
	for _, extra := range humFillersByLang {
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

func isHumFiller(key string, fillers map[string]bool) bool {
	if key == "" || fillers[key] {
		return true
	}
	for syl := range fillers {
		if len(key) < len(syl)*2 || !repeatsUnit(key, syl) {
			continue
		}
		if humWordSyllables[syl] && len(key) < len(syl)*3 {
			continue
		}
		return true
	}
	runes := []rune(key)
	if len(runes) >= 2 && repeatsUnit(key, string(runes[0])) {
		return true
	}
	// hmm, ımm, mm (not "im")
	rest := strings.TrimLeft(key, "hı")
	if len(key)-len(rest) <= 1 && rest != "" && strings.Trim(rest, "m") == "" {
		return true
	}
	return false
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
			isFiller := make([]bool, len(words))
			for i, w := range words {
				isFiller[i] = isHumFiller(humWordKey(w), fillers)
			}
			start, end := 0, len(words)
			for start < end && isFiller[start] {
				start++
			}
			for end > start && isFiller[end-1] {
				end--
			}
			if start == end {
				continue
			}
			count := 0
			for i := start; i < end; i++ {
				if !isFiller[i] && len([]rune(humWordKey(words[i]))) >= 2 {
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
