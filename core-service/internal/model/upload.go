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
}
