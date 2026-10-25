package model

import "time"

// UploadVocalResponse represents the response for vocal upload
type UploadVocalResponse struct {
	ID         string    `json:"id"`
	FileURL    string    `json:"fileUrl"`
	Duration   float64   `json:"duration"`
	SampleRate int       `json:"sampleRate"`
	Channels   int       `json:"channels"`
	CreatedAt  time.Time `json:"createdAt"`
	// ExpiresAt: fileUrl is presigned and EXPIRING. The client must not cache
	// this URL and later show a broken link.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// MicInfo describes the input that recorded a take (R-VOC-17). Both fields are
// already validated/cleaned by the handler; empty means "not sent".
type MicInfo struct {
	Kind string
	Name string
}
