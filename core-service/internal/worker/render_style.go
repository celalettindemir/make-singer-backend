package worker

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/model"
)

// styleTavani: Suno V6 style alani siniri (karakter). Bayt olarak kesmek
// her zaman bu sinirin altinda kalir.
const styleTavani = 1000

// buildMusicStyle, brief ve arrangement'tan Suno style metnini uretir.
// Degeri olmayan alanlar yazilmaz; BPM/key yoksa Suno kendisi secer.
func buildMusicStyle(p *model.RenderJobPayload) string {
	parcalar := []string{string(p.Brief.Genre), "instrumental"}

	if v := p.Brief.BPM.Value; v != nil {
		parcalar = append(parcalar, fmt.Sprintf("%d BPM", *v))
	}
	if k := p.Brief.Key; k.Tonic != nil && k.Scale != nil {
		parcalar = append(parcalar, fmt.Sprintf("%s %s", *k.Tonic, *k.Scale))
	}
	for _, v := range p.Brief.Vibes {
		if v = strings.TrimSpace(v); v != "" {
			parcalar = append(parcalar, v)
		}
	}
	if d := p.Arrangement.Density; d != "" {
		parcalar = append(parcalar, fmt.Sprintf("%s arrangement", d))
	}
	if g := p.Arrangement.Groove; g != "" {
		parcalar = append(parcalar, strings.ReplaceAll(string(g), "_", " ")+" groove")
	}
	if len(p.Arrangement.Instruments) > 0 {
		adlar := make([]string, len(p.Arrangement.Instruments))
		for i, in := range p.Arrangement.Instruments {
			adlar[i] = string(in)
		}
		parcalar = append(parcalar, "featuring "+strings.Join(adlar, ", "))
	}

	return kes(strings.Join(parcalar, ", "), styleTavani)
}

// kes, s'yi en fazla n bayta rune sinirinda keser.
func kes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
