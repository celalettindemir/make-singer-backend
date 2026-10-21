package model

import (
	"errors"
	"fmt"
	"time"
)

// RenderStartRequest represents the request to start a render job
type RenderStartRequest struct {
	ProjectID       string      `json:"projectId" validate:"required,uuid"`
	Brief           Brief       `json:"brief" validate:"required"`
	Arrangement     Arrangement `json:"arrangement" validate:"required"`
	StemMode        StemMode    `json:"stemMode" validate:"omitempty,oneof=mix focus stems"`
	FocusInstrument *Instrument `json:"focusInstrument" validate:"omitempty,oneof=drums bass piano guitar synth strings brass woodwinds percussion pads lead fx"`
	// Guide asks for a guide singer over the instrumental (Plus/Pro, spec 9).
	Guide *GuideRequest `json:"guide,omitempty" validate:"omitempty"`
}

// GuideRequest is what the guided render needs: the singer's voice, the
// lyrics in song order and, when measured, the user's comfortable range.
type GuideRequest struct {
	VocalGender VocalGender    `json:"vocalGender" validate:"required,oneof=m f"`
	Sections    []GuideSection `json:"sections" validate:"required,min=1,max=32,dive"`
	Range       *VocalRange    `json:"range,omitempty" validate:"omitempty"`
}

// GuideSection is one song section with its lyric lines.
type GuideSection struct {
	ID    string   `json:"id" validate:"required,max=64"`
	Type  string   `json:"type" validate:"required,max=32"`
	Lines []string `json:"lines" validate:"max=32,dive,max=200"`
}

// VocalRange is a comfortable singing range in MIDI note numbers.
type VocalRange struct {
	Low  int `json:"low" validate:"min=24,max=96"`
	High int `json:"high" validate:"min=24,max=96,gtfield=Low"`
}

// ErrInvalidStemSettings: stemMode and focusInstrument are inconsistent.
var ErrInvalidStemSettings = errors.New("invalid stem settings")

// StemSettings normalizes the request's stem mode: an empty mode is mix (the
// cheap side; an old client cannot accidentally spend 62 credits). focus
// requires an instrument that is part of the arrangement; other modes cannot
// take an instrument. A plan-based restriction (e.g. stems only on a certain
// plan) will be added here later.
func (r *RenderStartRequest) StemSettings() (StemMode, *Instrument, error) {
	mode := r.StemMode
	if mode == "" {
		mode = StemModeMix
	}
	if mode != StemModeFocus {
		if r.FocusInstrument != nil {
			return "", nil, fmt.Errorf("%w: focusInstrument is only allowed in focus mode", ErrInvalidStemSettings)
		}
		return mode, nil, nil
	}
	if r.FocusInstrument == nil {
		return "", nil, fmt.Errorf("%w: focus mode requires focusInstrument", ErrInvalidStemSettings)
	}
	for _, i := range r.Arrangement.Instruments {
		if i == *r.FocusInstrument {
			focus := *r.FocusInstrument
			return mode, &focus, nil
		}
	}
	return "", nil, fmt.Errorf("%w: focusInstrument is not in the arrangement", ErrInvalidStemSettings)
}

// Brief contains song parameters
type Brief struct {
	Genre     Genre         `json:"genre" validate:"required,oneof=pop rock hiphop rnb electronic jazz country folk classical latin reggae blues"`
	Vibes     []string      `json:"vibes" validate:"required,min=1,max=5"`
	BPM       BPMConfig     `json:"bpm" validate:"required"`
	Key       KeyConfig     `json:"key" validate:"required"`
	Structure []SongSection `json:"structure" validate:"required,min=1,dive"`
}

// BPMConfig holds BPM settings
type BPMConfig struct {
	Mode  BPMMode `json:"mode" validate:"required,oneof=auto range fixed"`
	Value *int    `json:"value" validate:"omitempty,min=40,max=220"`
	Min   *int    `json:"min" validate:"omitempty,min=40,max=220"`
	Max   *int    `json:"max" validate:"omitempty,min=40,max=220"`
}

// KeyConfig holds key settings
type KeyConfig struct {
	Mode  KeyMode `json:"mode" validate:"required,oneof=auto manual"`
	Tonic *Tonic  `json:"tonic" validate:"omitempty,oneof=C C# D D# E F F# G G# A A# B"`
	Scale *Scale  `json:"scale" validate:"omitempty,oneof=major minor"`
}

// SongSection represents a section in the song structure
type SongSection struct {
	ID   string      `json:"id" validate:"required"`
	Type SectionType `json:"type" validate:"required,oneof=intro verse prechorus chorus bridge outro instrumental"`
	Bars int         `json:"bars" validate:"required,min=1,max=64"`
}

// Arrangement contains arrangement settings
type Arrangement struct {
	Instruments     []Instrument      `json:"instruments" validate:"required,min=1,dive,oneof=drums bass piano guitar synth strings brass woodwinds percussion pads lead fx" enums:"drums,bass,piano,guitar,synth,strings,brass,woodwinds,percussion,pads,lead,fx"`
	Density         Density           `json:"density" validate:"required,oneof=minimal medium full"`
	Groove          Groove            `json:"groove" validate:"required,oneof=straight swing half_time"`
	SectionEmphasis []SectionEmphasis `json:"sectionEmphasis" validate:"omitempty,dive"`
}

// SectionEmphasis represents emphasis for a section
type SectionEmphasis struct {
	SectionID string `json:"sectionId" validate:"required"`
	Emphasis  string `json:"emphasis" validate:"required,oneof=bigger biggest"`
}

// RenderStartResponse represents the response when starting a render
type RenderStartResponse struct {
	JobID             string    `json:"jobId"`
	Status            JobStatus `json:"status"`
	EstimatedDuration int       `json:"estimatedDuration"`
	CreatedAt         time.Time `json:"createdAt"`
	Balance           int       `json:"balance"`
}

// RenderStatusResponse represents the status of a render job
type RenderStatusResponse struct {
	JobID       string     `json:"jobId"`
	Status      JobStatus  `json:"status"`
	Progress    int        `json:"progress"`
	CurrentStep string     `json:"currentStep,omitempty"`
	Error       *string    `json:"error"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	RetryCount  int        `json:"retryCount"`
}

// RenderResultResponse represents the result of a completed render
type RenderResultResponse struct {
	ID        string       `json:"id"`
	BPM       int          `json:"bpm"`
	Duration  float64      `json:"duration"`
	Key       KeyResult    `json:"key"`
	CreatedAt time.Time    `json:"createdAt"`
	StemMode  StemMode     `json:"stemMode"`
	Stems     []StemResult `json:"stems"`
	Guide     *GuideResult `json:"guide"`
}

// GuideResult is the guide singer of a guided render; null otherwise.
// NotesURL is null when the note strip could not be made.
type GuideResult struct {
	FileURL             string     `json:"fileUrl"`
	NotesURL            *string    `json:"notesUrl"`
	PitchShiftSemitones int        `json:"pitchShiftSemitones"`
	OffsetMs            int        `json:"offsetMs"`
	ExpiresAt           *time.Time `json:"expiresAt,omitempty"`
}

// KeyResult represents the final key of the render
type KeyResult struct {
	Tonic Tonic `json:"tonic"`
	Scale Scale `json:"scale"`
}

// StemResult represents a single stem in the render result
type StemResult struct {
	ID           string     `json:"id"`
	Instrument   Instrument `json:"instrument"`
	FileURL      string     `json:"fileUrl"`
	Duration     float64    `json:"duration"`
	WaveformData []float64  `json:"waveformData"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
}

// RenderResultRecord is the render result stored in the job record. For
// stems the object key is stored, not a signed URL; URLs are regenerated on
// every read (see MasterResultRecord).
type RenderResultRecord struct {
	ID        string             `json:"id"`
	BPM       int                `json:"bpm"`
	Duration  float64            `json:"duration"`
	Key       KeyResult          `json:"key"`
	CreatedAt time.Time          `json:"createdAt"`
	StemMode  StemMode           `json:"stemMode"`
	Stems     []StemResultRecord `json:"stems"`
	Guide     *GuideRecord       `json:"guide,omitempty"`
}

// GuideRecord is the stored form of the guide: object keys, not URLs.
type GuideRecord struct {
	FileKey             string  `json:"fileKey"`
	NotesKey            *string `json:"notesKey,omitempty"`
	PitchShiftSemitones int     `json:"pitchShiftSemitones"`
	OffsetMs            int     `json:"offsetMs"`
}

// StemResultRecord is the stored form of a single stem.
type StemResultRecord struct {
	ID           string     `json:"id"`
	Instrument   Instrument `json:"instrument"`
	FileKey      string     `json:"fileKey"`
	Duration     float64    `json:"duration"`
	WaveformData []float64  `json:"waveformData"`
}

// RenderCancelResponse represents the response when canceling a render
type RenderCancelResponse struct {
	Success bool      `json:"success"`
	JobID   string    `json:"jobId"`
	Status  JobStatus `json:"status"`
}
