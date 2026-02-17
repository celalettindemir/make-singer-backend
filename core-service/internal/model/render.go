package model

import "time"

// RenderStartRequest represents the request to start a render job
type RenderStartRequest struct {
	ProjectID   string      `json:"projectId" validate:"required,uuid"`
	Brief       Brief       `json:"brief" validate:"required"`
	Arrangement Arrangement `json:"arrangement" validate:"required"`
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
	Instruments     []Instrument      `json:"instruments" validate:"required,min=1,dive,oneof=drums bass piano guitar synth strings brass woodwinds percussion pads lead fx"`
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
	Stems     []StemResult `json:"stems"`
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
}

// RenderCancelResponse represents the response when canceling a render
type RenderCancelResponse struct {
	Success bool      `json:"success"`
	JobID   string    `json:"jobId"`
	Status  JobStatus `json:"status"`
}
