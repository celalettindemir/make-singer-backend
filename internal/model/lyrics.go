package model

// LyricsGenerateRequest represents the request body for lyrics generation
type LyricsGenerateRequest struct {
	Genre       Genre       `json:"genre" validate:"required,oneof=pop rock hiphop rnb electronic jazz country folk classical latin reggae blues"`
	SectionType SectionType `json:"sectionType" validate:"required,oneof=intro verse prechorus chorus bridge outro instrumental"`
	Vibes       []string    `json:"vibes" validate:"required,min=1,max=3,dive,min=1"`
	Language    Language    `json:"language" validate:"omitempty,oneof=en tr fr"`
}

// LyricsGenerateResponse represents the response for lyrics generation
type LyricsGenerateResponse struct {
	Drafts [][]string `json:"drafts"`
}

// LyricsRewriteRequest represents the request body for lyrics rewriting
type LyricsRewriteRequest struct {
	CurrentLyrics string      `json:"currentLyrics" validate:"required,min=1"`
	Genre         Genre       `json:"genre" validate:"required,oneof=pop rock hiphop rnb electronic jazz country folk classical latin reggae blues"`
	SectionType   SectionType `json:"sectionType" validate:"required,oneof=intro verse prechorus chorus bridge outro instrumental"`
	Vibes         []string    `json:"vibes" validate:"required,min=1,max=3,dive,min=1"`
	Instructions  string      `json:"instructions" validate:"omitempty,max=500"`
}

// LyricsRewriteResponse represents the response for lyrics rewriting
type LyricsRewriteResponse struct {
	Lines []string `json:"lines"`
}
