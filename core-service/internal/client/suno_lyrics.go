package client

import (
	"strings"

	"github.com/makeasinger/api/internal/model"
)

// sunoSectionTags maps our section types to Suno's lyric tags.
var sunoSectionTags = map[string]string{
	"intro":     "Intro",
	"verse":     "Verse",
	"prechorus": "Pre-Chorus",
	"chorus":    "Chorus",
	"bridge":    "Bridge",
	"outro":     "Outro",
}

// SunoLyrics writes the sections as Suno lyrics: a [Tag] line per section
// and its trimmed lines, blank lines between sections. Sections without a
// line are left out; lines go as written (Faz 0: cue letters do not help).
func SunoLyrics(sections []model.GuideSection) string {
	var blocks []string
	for _, s := range sections {
		var lines []string
		for _, l := range s.Lines {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			continue
		}
		tag, ok := sunoSectionTags[s.Type]
		if !ok && s.Type != "" {
			tag = strings.ToUpper(s.Type[:1]) + s.Type[1:]
		}
		blocks = append(blocks, "["+tag+"]\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}
