package model

// LyricsGenerateRequest represents the request body for lyrics generation
type LyricsGenerateRequest struct {
	Genre       Genre       `json:"genre" validate:"required,oneof=pop rock hiphop rnb electronic jazz country folk classical latin reggae blues"`
	SectionType SectionType `json:"sectionType" validate:"required,oneof=intro verse prechorus chorus bridge outro instrumental"`
	Vibes       []string    `json:"vibes" validate:"required,min=1,max=3,dive,min=1"`
	Language    Language    `json:"language" validate:"omitempty,oneof=en tr fr"`
	LyricsContext
}

// LyricsGenerateResponse represents the response for lyrics generation
type LyricsGenerateResponse struct {
	Drafts  [][]string `json:"drafts"`
	Balance int        `json:"balance"`
}

// LyricsRewriteRequest represents the request body for lyrics rewriting
type LyricsRewriteRequest struct {
	CurrentLyrics string      `json:"currentLyrics" validate:"required,min=1"`
	Genre         Genre       `json:"genre" validate:"required,oneof=pop rock hiphop rnb electronic jazz country folk classical latin reggae blues"`
	SectionType   SectionType `json:"sectionType" validate:"required,oneof=intro verse prechorus chorus bridge outro instrumental"`
	Vibes         []string    `json:"vibes" validate:"required,min=1,max=3,dive,min=1"`
	Instructions  string      `json:"instructions" validate:"omitempty,max=500"`
	Language      Language    `json:"language" validate:"omitempty,oneof=en tr fr"`
	LyricsContext
}

// LyricsRewriteResponse represents the response for lyrics rewriting
type LyricsRewriteResponse struct {
	Lines   []string `json:"lines"`
	Balance int      `json:"balance"`
}

// Singing cue kinds (spec 9 §3.3).
const (
	CueHold   = "hold"
	CueRise   = "rise"
	CueBreath = "breath"
)

// LyricCue is a singing hint on one syllable of a clean lyric line. SyllableIndex
// counts the line's syllables from 0; At is the Unicode scalar index of that
// syllable's first vowel in the line text. Breath means "breathe after this
// syllable". A preview only: never sent to Suno.
type LyricCue struct {
	SyllableIndex int    `json:"syllableIndex"`
	Kind          string `json:"kind"`
	At            int    `json:"at"`
}

// LyricLine is one clean lyric line with its cues. Cues is never nil.
type LyricLine struct {
	Text string     `json:"text"`
	Cues []LyricCue `json:"cues"`
}
