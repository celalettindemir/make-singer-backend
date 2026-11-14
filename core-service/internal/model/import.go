package model

// ImportSection is one section of an imported song's structure.
type ImportSection struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Bars int    `json:"bars"`
}

// ImportLyricsSection is the lyric lines of one imported section.
type ImportLyricsSection struct {
	SectionID string   `json:"sectionId"`
	Lines     []string `json:"lines"`
}
