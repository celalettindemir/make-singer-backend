package service

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/model"
)

// maxContextChars caps the lyrics of the song block (runes). Over it the
// sections farthest from the target lose their text first (spec 7 §3.3).
const maxContextChars = 8000

// Prompt markers; tests look for them.
const (
	topicHeader      = "What the song is about (from the songwriter):"
	songBlockHeader  = "The whole song, in order:"
	targetMark       = "<- WRITE THIS SECTION"
	emptyMark        = "(not written yet)"
	repeatChorusRule = "This song already has a written chorus. Repeat it for this chorus, word for word or nearly so, so the hook comes back."
	turkishGuidance  = `Write natural, spoken Turkish as a Turkish songwriter would. Avoid phrases that sound translated from English, avoid forced inverted word order, and never invent words or break vowel harmony to make a rhyme.`
)

var languageGuidance = map[model.Language]string{
	model.LanguageTR: turkishGuidance,
	model.LanguageEN: `Write natural, idiomatic English. Prefer concrete images over clichés; never pad a line just to land a rhyme.`,
	model.LanguageFR: `Écris un français naturel et chantable. Évite les tournures traduites de l'anglais et les rimes forcées.`,
}

func langName(language model.Language) string {
	switch language {
	case model.LanguageTR:
		return "Turkish"
	case model.LanguageFR:
		return "French"
	}
	return "English"
}

func buildSystemPrompt(language model.Language) string {
	return systemPrompt(language, `Always output your response as valid JSON in the exact format requested.
Do not include any text outside the JSON structure.`)
}

// buildStreamSystemPrompt is the system prompt of the streaming routes: the
// same writer brief, but plain lines instead of JSON.
func buildStreamSystemPrompt(language model.Language) string {
	return systemPrompt(language, `Output only the lyric lines, one per line, plain text. No title, no section labels, no numbering, no quotes, no explanation.`)
}

func systemPrompt(language model.Language, outputRule string) string {
	return fmt.Sprintf(`You are a professional %s songwriter with expertise in various music genres.
Your task is to write compelling, emotionally resonant lyrics that match the requested style and mood.
%s
Lines must flow when sung: keep the syllable count of neighbouring lines close, let rhymes come naturally, and keep one voice and one story across the whole song.
%s`, langName(language), languageGuidance[language], outputRule)
}

func buildGeneratePrompt(req *model.LyricsGenerateRequest, language model.Language) string {
	minLines, maxLines := targetLines(req.SectionType, sectionBars(req.LyricsContext))
	var b strings.Builder
	writeGenerateBody(&b, req, language, "Create 2 different draft versions. Each draft should have "+lineRange(minLines, maxLines)+" that fit the section type.")
	fmt.Fprintf(&b, `
Output a single JSON object with exactly one key, "drafts". Its value is an array of exactly 2 drafts; each draft is an array of %d-%d plain strings (one string per lyric line). Use only plain strings: no nested objects, no extra keys, no line numbers. Escape any double quote inside a line as \" or avoid it.
Example shape: {"drafts": [["line1","line2","line3","line4"], ["line1","line2","line3","line4"]]}`, minLines, maxLines)
	return b.String()
}

// buildStreamGeneratePrompt asks for one draft of plain lines; the context,
// the section guidance and the chorus rule are the sync prompt's.
func buildStreamGeneratePrompt(req *model.LyricsGenerateRequest, language model.Language) string {
	minLines, maxLines := targetLines(req.SectionType, sectionBars(req.LyricsContext))
	var b strings.Builder
	writeGenerateBody(&b, req, language, "Write one draft with "+lineRange(minLines, maxLines)+" that fit the section type.")
	return b.String()
}

// writeGenerateBody writes the part of the generate prompt both variants
// share; ask is the sentence that sets the number of drafts and lines.
func writeGenerateBody(b *strings.Builder, req *model.LyricsGenerateRequest, language model.Language, ask string) {
	fmt.Fprintf(b, "Generate lyrics for a %s song's %s section.\n", req.Genre, req.SectionType)
	fmt.Fprintf(b, "Vibes/mood: %s\n", strings.Join(req.Vibes, ", "))
	fmt.Fprintf(b, "Write ALL lyrics in %s, even if the vibes are given in another language.\n", langName(language))
	writeContext(b, req.LyricsContext, false)
	b.WriteString("\n" + ask + `
For a verse: move the story forward or set the scene.
For a chorus: create a memorable, singable hook that sums up the song.
For a bridge: provide contrast or a new perspective.
For other sections: follow conventions of that section type.
`)
	if repeatsChorus(req.SectionType, req.LyricsContext) {
		b.WriteString(repeatChorusRule + "\n")
	}
}

func buildRewritePrompt(req *model.LyricsRewriteRequest, language model.Language) string {
	var b strings.Builder
	writeRewriteBody(&b, req, language)
	b.WriteString(`

Output as JSON: {"lines": ["line1","line2","line3","line4"]}`)
	return b.String()
}

// buildStreamRewritePrompt is the rewrite prompt without the JSON format
// line; the stream system prompt asks for plain lines.
func buildStreamRewritePrompt(req *model.LyricsRewriteRequest, language model.Language) string {
	var b strings.Builder
	writeRewriteBody(&b, req, language)
	return b.String()
}

func writeRewriteBody(b *strings.Builder, req *model.LyricsRewriteRequest, language model.Language) {
	fmt.Fprintf(b, "Rewrite the following lyrics for a %s song's %s section.\n", req.Genre, req.SectionType)
	fmt.Fprintf(b, "Current vibes/mood: %s\n", strings.Join(req.Vibes, ", "))
	if req.Instructions != "" {
		fmt.Fprintf(b, "Specific instructions: %s\n", req.Instructions)
	}
	writeContext(b, req.LyricsContext, true)
	fmt.Fprintf(b, `
Current lyrics:
%s

Keep the general meaning but improve the flow, rhyming, and emotional impact.
Maintain the same number of lines.
Write ALL lyrics in %s, translating them if the current lyrics are in another language.`, req.CurrentLyrics, langName(language))
}

// writeContext adds the topic, tempo and song blocks when the request has them.
func writeContext(b *strings.Builder, ctx model.LyricsContext, rewrite bool) {
	if ctx.Topic != "" {
		fmt.Fprintf(b, "\n%s\n%s\nEvery section of the song tells this story; keep it.\n", topicHeader, ctx.Topic)
	}
	if ctx.BPM != nil {
		fmt.Fprintf(b, "Tempo: %d BPM. Keep the lines short enough to sing at this tempo.\n", *ctx.BPM)
	}
	if block := songBlock(ctx, rewrite); block != "" {
		b.WriteString("\n" + block)
		b.WriteString("Continue what the written sections say: same story, same voice, same point of view. Do not copy their lines (except a repeated chorus).\n")
		if ctx.Topic == "" {
			b.WriteString("No topic was given: take it from the written sections.\n")
		}
	}
}

// songBlock lists the song's sections and marks the target. With
// skipTargetLyrics the target's own text is left out (a rewrite sends it
// as currentLyrics). Over maxContextChars the farthest sections lose their
// text first; on a tie the later section goes first.
func songBlock(ctx model.LyricsContext, skipTargetLyrics bool) string {
	if len(ctx.Song) == 0 || ctx.SectionIndex == nil {
		return ""
	}
	target := *ctx.SectionIndex
	texts := make([]string, len(ctx.Song))
	total := 0
	for i, s := range ctx.Song {
		if i == target && skipTargetLyrics {
			continue
		}
		texts[i] = strings.TrimSpace(s.Lyrics)
		total += utf8.RuneCountInString(texts[i])
	}
	order := make([]int, 0, len(ctx.Song))
	for i := range ctx.Song {
		if i != target {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		da, db := abs(order[a]-target), abs(order[b]-target)
		if da != db {
			return da > db
		}
		return order[a] > order[b]
	})
	for _, i := range order {
		if total <= maxContextChars {
			break
		}
		total -= utf8.RuneCountInString(texts[i])
		texts[i] = ""
	}

	var b strings.Builder
	b.WriteString(songBlockHeader + "\n")
	for i, s := range ctx.Song {
		fmt.Fprintf(&b, "%d. %s", i+1, s.Type)
		if s.Bars != nil {
			fmt.Fprintf(&b, " (%d bars)", *s.Bars)
		}
		switch {
		case i == target:
			b.WriteString(" " + targetMark + "\n")
		case texts[i] != "":
			b.WriteString(":\n" + indent(texts[i]) + "\n")
		default:
			b.WriteString(": " + emptyMark + "\n")
		}
	}
	return b.String()
}

// targetLines is the line count to ask for: from the bars when known
// (about two bars a line), else a range per section type.
func targetLines(section model.SectionType, bars *int) (min, max int) {
	if bars != nil {
		n := *bars / 2
		if n < 2 {
			n = 2
		}
		if n > 12 {
			n = 12
		}
		return n, n
	}
	switch section {
	case model.SectionIntro, model.SectionOutro, model.SectionPrechorus:
		return 2, 4
	case model.SectionVerse:
		return 6, 8
	case model.SectionChorus:
		return 4, 6
	case model.SectionBridge:
		return 4, 4
	}
	return 4, 8
}

func sectionBars(ctx model.LyricsContext) *int {
	if ctx.SectionIndex == nil || *ctx.SectionIndex < 0 || *ctx.SectionIndex >= len(ctx.Song) {
		return nil
	}
	return ctx.Song[*ctx.SectionIndex].Bars
}

// repeatsChorus: a chorus is being written while another chorus has text.
func repeatsChorus(section model.SectionType, ctx model.LyricsContext) bool {
	if section != model.SectionChorus || ctx.SectionIndex == nil {
		return false
	}
	for i, s := range ctx.Song {
		if i != *ctx.SectionIndex && s.Type == model.SectionChorus && strings.TrimSpace(s.Lyrics) != "" {
			return true
		}
	}
	return false
}

func lineRange(min, max int) string {
	if min == max {
		return fmt.Sprintf("%d lines", min)
	}
	return fmt.Sprintf("%d-%d lines", min, max)
}

func indent(s string) string {
	return "   " + strings.ReplaceAll(s, "\n", "\n   ")
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
