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

// ImportStemRecord is the stored form of one separated stem: the object key,
// signed on every read, and the length in seconds.
type ImportStemRecord struct {
	ID         string     `json:"id"`
	Instrument Instrument `json:"instrument"`
	Key        string     `json:"key"`
	Duration   float64    `json:"duration"`
}

// ImportRecord is the stored result of an import job (column sonuc): the
// ImportResult with object keys in place of signed URLs. Guide.NotesKey is
// the notes.json key.
type ImportRecord struct {
	DurationMs    int                    `json:"durationMs"`
	BPM           *float64               `json:"bpm"`
	Key           *KeyResult             `json:"key"`
	Structure     []ImportSection        `json:"structure"`
	Lyrics        []ImportLyricsSection  `json:"lyrics"`
	Stems         []ImportStemRecord     `json:"stems"`
	LyricTimeline []LyricTimelineSection `json:"lyricTimeline"`
	Guide         *GuideRecord           `json:"guide"`
}

// ImportResult is the result of an import job returned by GET /api/jobs/:id.
type ImportResult struct {
	DurationMs    int                    `json:"durationMs"`
	BPM           *float64               `json:"bpm"`
	Key           *KeyResult             `json:"key"`
	Structure     []ImportSection        `json:"structure"`
	Lyrics        []ImportLyricsSection  `json:"lyrics"`
	Stems         []StemResult           `json:"stems"`
	LyricTimeline []LyricTimelineSection `json:"lyricTimeline"`
	Guide         *GuideResult           `json:"guide"`
}
