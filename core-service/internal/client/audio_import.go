package client

import (
	"context"

	"github.com/makeasinger/api/internal/model"
)

// ImportPrepareRequest is the audio-service /ice-aktar-hazirla request.
type ImportPrepareRequest struct {
	InputKey string `json:"input_key"`
}

// ImportPrepareResponse carries the decoded duration.
type ImportPrepareResponse struct {
	DurationMs int `json:"duration_ms"`
}

// ImportMeasureRequest is the audio-service /ice-aktar-olc request.
type ImportMeasureRequest struct {
	InputKey string `json:"input_key"`
}

// ImportMeasureResponse: BPM and Key are nil when they could not be measured.
type ImportMeasureResponse struct {
	BPM *float64         `json:"bpm"`
	Key *model.KeyResult `json:"key"`
}

// TimedWordIn is one transcribed word with its time, as audio-service wants it.
type TimedWordIn struct {
	Word    string  `json:"word"`
	StartS  float64 `json:"start_s"`
	EndS    float64 `json:"end_s"`
	Success bool    `json:"success"`
}

// NotesSection is a lyric section: its id and line texts.
type NotesSection struct {
	ID    string   `json:"id"`
	Lines []string `json:"lines"`
}

// ImportNotesRequest is the audio-service /ice-aktar-notalar request.
type ImportNotesRequest struct {
	VocalKey string         `json:"vocal_key"`
	Words    []TimedWordIn  `json:"words"`
	Sections []NotesSection `json:"sections"`
	BPM      float64        `json:"bpm"`
	NotesKey string         `json:"notes_key"`
}

// ImportNotesResponse: the notes file is already written to NotesKey.
type ImportNotesResponse struct {
	NotesURL     string `json:"notes_url"`
	RangeMidi    []int  `json:"range_midi"`
	LinesMatched int    `json:"lines_matched"`
}

// ImportAudio is what the import worker needs from audio-service. It is kept
// separate from AudioProcessor so existing fakes do not break.
type ImportAudio interface {
	PrepareImport(ctx context.Context, req *ImportPrepareRequest) (*ImportPrepareResponse, error)
	MeasureImport(ctx context.Context, req *ImportMeasureRequest) (*ImportMeasureResponse, error)
	ImportNotes(ctx context.Context, req *ImportNotesRequest) (*ImportNotesResponse, error)
	IsConfigured() bool
}

var _ ImportAudio = (*AudioClient)(nil)

// PrepareImport calls /ice-aktar-hazirla; 422 is ErrAudioUnprocessable.
func (c *AudioClient) PrepareImport(ctx context.Context, req *ImportPrepareRequest) (*ImportPrepareResponse, error) {
	var result ImportPrepareResponse
	if err := c.post(ctx, "/ice-aktar-hazirla", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// MeasureImport calls /ice-aktar-olc.
func (c *AudioClient) MeasureImport(ctx context.Context, req *ImportMeasureRequest) (*ImportMeasureResponse, error) {
	var result ImportMeasureResponse
	if err := c.post(ctx, "/ice-aktar-olc", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ImportNotes calls /ice-aktar-notalar.
func (c *AudioClient) ImportNotes(ctx context.Context, req *ImportNotesRequest) (*ImportNotesResponse, error) {
	var result ImportNotesResponse
	if err := c.post(ctx, "/ice-aktar-notalar", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
