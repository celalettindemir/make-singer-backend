package client

import "context"

// GuideAnalyzeRequest is the audio-service /rehber-analiz request. Keys are
// R2 object keys; no signed URL is carried.
type GuideAnalyzeRequest struct {
	GuideKey        string              `json:"guide_key"`
	ReferenceKey    string              `json:"reference_key"`
	InstrumentalKey string              `json:"instrumental_key"`
	Words           []GuideWord         `json:"words"`
	Sections        []GuideSectionInput `json:"sections"`
	VocalGender     string              `json:"vocal_gender"`
	BPM             float64             `json:"bpm"`
	AlignedKey      string              `json:"aligned_key"`
	NotesKey        string              `json:"notes_key"`
}

// GuideWord is one Suno word timing on the guide's own clock.
type GuideWord struct {
	Word    string  `json:"word"`
	StartS  float64 `json:"start_s"`
	EndS    float64 `json:"end_s"`
	Success bool    `json:"success"`
}

// GuideSectionInput is one lyric section in song order.
type GuideSectionInput struct {
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Lines []string `json:"lines"`
}

// GuideAnalyzeResult: the aligned guide and the notes are already written to
// the request's keys. RangeMidi is nil when no pitch was found.
type GuideAnalyzeResult struct {
	AlignedURL   string  `json:"aligned_url"`
	NotesURL     string  `json:"notes_url"`
	OffsetMs     int     `json:"offset_ms"`
	RangeMidi    *[2]int `json:"range_midi"`
	NoteCount    int     `json:"note_count"`
	LinesMatched int     `json:"lines_matched"`
}

// PitchShiftItem is one file to shift into OutputKey.
type PitchShiftItem struct {
	InputKey  string `json:"input_key"`
	OutputKey string `json:"output_key"`
}

// PitchShiftNotes is the guide note strip to move with the audio.
type PitchShiftNotes struct {
	InputKey  string
	OutputKey string
}

// GuideAudio is what the guided render needs from audio-service. It is kept
// separate from AudioProcessor so existing fakes do not break.
type GuideAudio interface {
	AnalyzeGuide(ctx context.Context, req *GuideAnalyzeRequest) (*GuideAnalyzeResult, error)
	PitchShift(ctx context.Context, items []PitchShiftItem, semitones int, notes *PitchShiftNotes) error
}

var _ GuideAudio = (*AudioClient)(nil)

// AnalyzeGuide calls /rehber-analiz.
func (c *AudioClient) AnalyzeGuide(ctx context.Context, req *GuideAnalyzeRequest) (*GuideAnalyzeResult, error) {
	var result GuideAnalyzeResult
	if err := c.post(ctx, "/rehber-analiz", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// PitchShift calls /perde-kaydir once per item, so a stems render's ten
// files never meet the client timeout in one request. The notes go with the
// first request.
func (c *AudioClient) PitchShift(ctx context.Context, items []PitchShiftItem, semitones int, notes *PitchShiftNotes) error {
	for i, item := range items {
		body := map[string]any{"items": []PitchShiftItem{item}, "semitones": semitones}
		if i == 0 && notes != nil {
			body["notes_key"] = notes.InputKey
			body["notes_output_key"] = notes.OutputKey
		}
		var out struct {
			Outputs []string `json:"outputs"`
		}
		if err := c.post(ctx, "/perde-kaydir", body, &out); err != nil {
			return err
		}
	}
	return nil
}
