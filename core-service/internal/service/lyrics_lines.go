package service

import (
	"errors"
	"regexp"
	"strings"
)

// maxStreamLines is how many lyric lines one stream may emit.
const maxStreamLines = 16

var errLineLimit = errors.New("lyrics line limit reached")

var (
	listMarkerRe = regexp.MustCompile(`^(?:\d+[.)]|[-*•])\s*`)
	sectionWords = foldSet(map[string]bool{
		"verse": true, "chorus": true, "bridge": true, "intro": true, "outro": true,
		"pre-chorus": true, "hook": true,
		"nakarat": true, "kita": true, "köprü": true, "giriş": true, "çıkış": true,
		"couplet": true, "refrain": true, "pont": true,
	})
	repeatSuffixRe = regexp.MustCompile(`\(x\d+\)$`)
	numberSuffixRe = regexp.MustCompile(`\s\d+$`)
	quotePairs     = [][2]string{{`"`, `"`}, {"“", "”"}, {"«", "»"}}
)

// lineSplitter turns arbitrary text chunks into whole cleaned lyric lines.
type lineSplitter struct {
	emit  func(line string) error
	buf   strings.Builder
	count int
}

func newLineSplitter(emit func(line string) error) *lineSplitter {
	return &lineSplitter{emit: emit}
}

// Write buffers chunk and emits every complete line. After the maxStreamLines-th
// emitted line it returns errLineLimit.
func (s *lineSplitter) Write(chunk string) error {
	if s.count >= maxStreamLines {
		return errLineLimit
	}
	s.buf.WriteString(chunk)
	text := s.buf.String()
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		if err := s.line(text[:i]); err != nil {
			return err
		}
		text = text[i+1:]
	}
	s.buf.Reset()
	s.buf.WriteString(text)
	return nil
}

// Close emits the trailing partial line unless the limit was already hit.
func (s *lineSplitter) Close() error {
	if s.count >= maxStreamLines {
		return nil
	}
	tail := s.buf.String()
	s.buf.Reset()
	return s.line(tail)
}

func (s *lineSplitter) Count() int { return s.count }

func (s *lineSplitter) line(raw string) error {
	text, ok := cleanLine(raw)
	if !ok {
		return nil
	}
	if err := s.emit(text); err != nil {
		return err
	}
	s.count++
	if s.count >= maxStreamLines {
		return errLineLimit
	}
	return nil
}

// cleanLine trims a raw model line; ok is false for blank lines and section labels.
func cleanLine(raw string) (string, bool) {
	text := strings.TrimSpace(listMarkerRe.ReplaceAllString(strings.TrimSpace(raw), ""))
	if text == "" || isSectionLabel(text) {
		return "", false
	}
	for _, q := range quotePairs {
		if len(text) >= len(q[0])+len(q[1]) && strings.HasPrefix(text, q[0]) && strings.HasSuffix(text, q[1]) {
			text = strings.TrimSpace(text[len(q[0]) : len(text)-len(q[1])])
			break
		}
	}
	return text, text != ""
}

func isSectionLabel(text string) bool {
	if (strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]")) ||
		(strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")")) {
		return true
	}
	// A section word, then optional number and/or "(x2)", then optional colon.
	t := strings.TrimSpace(strings.TrimSuffix(text, ":"))
	t = strings.TrimSpace(repeatSuffixRe.ReplaceAllString(t, ""))
	t = strings.TrimSpace(strings.TrimSuffix(t, ":"))
	t = strings.TrimSpace(numberSuffixRe.ReplaceAllString(t, ""))
	return sectionWords[foldTR(t)]
}

// foldTR lowercases and maps the dotless ı to i so "KITA" matches "kıta".
func foldTR(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.ReplaceAll(s, "I", "ı")), "ı", "i")
}

func foldSet(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[foldTR(k)] = true
	}
	return out
}
