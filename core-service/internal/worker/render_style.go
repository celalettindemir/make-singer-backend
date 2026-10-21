package worker

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/model"
)

// maxStyleLen: Suno V6 style field limit (characters). Cutting by bytes
// always stays under this limit.
const maxStyleLen = 1000

// buildMusicStyle builds the Suno style text from the brief and arrangement.
// Fields without a value are omitted; if BPM/key is missing Suno picks its own.
// A vocal render is sung, so its style has no "instrumental" part.
func buildMusicStyle(p *model.RenderJobPayload) string {
	parts := []string{string(p.Brief.Genre)}
	if p.Vocal == nil {
		parts = append(parts, "instrumental")
	}

	if v := p.Brief.BPM.Value; v != nil {
		parts = append(parts, fmt.Sprintf("%d BPM", *v))
	}
	if k := p.Brief.Key; k.Tonic != nil && k.Scale != nil {
		parts = append(parts, fmt.Sprintf("%s %s", *k.Tonic, *k.Scale))
	}
	for _, v := range p.Brief.Vibes {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, v)
		}
	}
	if d := p.Arrangement.Density; d != "" {
		parts = append(parts, fmt.Sprintf("%s arrangement", d))
	}
	if g := p.Arrangement.Groove; g != "" {
		parts = append(parts, strings.ReplaceAll(string(g), "_", " ")+" groove")
	}
	if len(p.Arrangement.Instruments) > 0 {
		names := make([]string, len(p.Arrangement.Instruments))
		for i, in := range p.Arrangement.Instruments {
			names[i] = string(in)
		}
		parts = append(parts, "featuring "+strings.Join(names, ", "))
	}

	return truncate(strings.Join(parts, ", "), maxStyleLen)
}

// truncate cuts s to at most n bytes, on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
