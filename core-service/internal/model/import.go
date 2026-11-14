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

// ImportStartResponse is the 202 answer of POST /api/imports.
type ImportStartResponse struct {
	JobID      string `json:"jobId"`
	DurationMs int    `json:"durationMs"`
}

// ImportPayload is the input of an import job (column girdi). The project
// name lives in the job row (ProjectName), not here.
type ImportPayload struct {
	SourceKey   string `json:"sourceKey"`
	DurationMs  int    `json:"durationMs"`
	ContentType string `json:"contentType"`
}
