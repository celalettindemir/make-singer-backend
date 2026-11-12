package service

import "strings"

// writeHumLines tells the lyricist which words the user sang in their hum.
// Without usable lines it writes nothing.
func writeHumLines(b *strings.Builder, lines []string) {
	var kept []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		return
	}
	b.WriteString("\nThe user hummed the song's melody and sang these words in it:\n")
	for _, l := range kept {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("The song is built around this hum. Write lines that fit with these words: same story, similar length and rhythm. Use the words themselves only in the chorus.\n")
}
