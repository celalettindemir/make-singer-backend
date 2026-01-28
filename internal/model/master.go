package model

import "time"

// MasterPreviewRequest represents the request for a master preview
type MasterPreviewRequest struct {
	ProjectID        string        `json:"projectId" validate:"required,uuid"`
	Profile          MasterProfile `json:"profile" validate:"required,oneof=clean warm loud"`
	StemURLs         []string      `json:"stemUrls" validate:"required,min=1,dive,url"`
	MixSnapshot      MixSnapshot   `json:"mixSnapshot" validate:"required"`
	PreviewStartTime *int          `json:"previewStartTime" validate:"omitempty,min=0"`
}

// MixSnapshot contains mix settings
type MixSnapshot struct {
	Channels []MixChannel `json:"channels" validate:"required,min=1,dive"`
	Preset   MixPreset    `json:"preset" validate:"required,oneof=default vocal_friendly bass_heavy bright warm"`
}

// MixChannel represents a single channel in the mix
type MixChannel struct {
	StemID   string  `json:"stemId" validate:"required"`
	VolumeDb float64 `json:"volumeDb" validate:"min=-60,max=12"`
	Mute     bool    `json:"mute"`
	Solo     bool    `json:"solo"`
}

// MasterPreviewResponse represents the response for a master preview
type MasterPreviewResponse struct {
	FileURL   string    `json:"fileUrl"`
	Duration  int       `json:"duration"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// MasterFinalRequest represents the request for final mastering
type MasterFinalRequest struct {
	ProjectID   string        `json:"projectId" validate:"required,uuid"`
	Profile     MasterProfile `json:"profile" validate:"required,oneof=clean warm loud"`
	StemURLs    []string      `json:"stemUrls" validate:"required,min=1,dive,url"`
	MixSnapshot MixSnapshot   `json:"mixSnapshot" validate:"required"`
	VocalTakes  []VocalTake   `json:"vocalTakes" validate:"omitempty,dive"`
}

// VocalTake represents a vocal take for mastering
type VocalTake struct {
	SectionID string `json:"sectionId" validate:"required"`
	TakeID    string `json:"takeId" validate:"required"`
	FileURL   string `json:"fileUrl" validate:"required,url"`
	OffsetMs  *int   `json:"offsetMs" validate:"omitempty"`
}

// MasterFinalResponse represents the response when starting final mastering
type MasterFinalResponse struct {
	JobID             string    `json:"jobId"`
	Status            JobStatus `json:"status"`
	EstimatedDuration int       `json:"estimatedDuration"`
}

// MasterStatusResponse represents the status of a master job
type MasterStatusResponse struct {
	JobID       string    `json:"jobId"`
	Status      JobStatus `json:"status"`
	Progress    int       `json:"progress"`
	CurrentStep string    `json:"currentStep,omitempty"`
}

// MasterResultResponse represents the result of completed mastering
type MasterResultResponse struct {
	FileURL   string        `json:"fileUrl"`
	Duration  float64       `json:"duration"`
	Profile   MasterProfile `json:"profile"`
	PeakDb    float64       `json:"peakDb"`
	LUFS      int           `json:"lufs"`
	ExpiresAt time.Time     `json:"expiresAt"`
}
