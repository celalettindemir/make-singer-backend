package model

import "time"

// ExportMP3Request represents the request for MP3 export
type ExportMP3Request struct {
	ProjectID     string         `json:"projectId" validate:"required,uuid"`
	MasterFileURL string         `json:"masterFileUrl" validate:"required,url"`
	Quality       *int           `json:"quality" validate:"omitempty,oneof=128 192 256 320"`
	Metadata      *ExportMetadata `json:"metadata" validate:"omitempty"`
}

// ExportMetadata contains ID3 tag metadata
type ExportMetadata struct {
	Title   string `json:"title" validate:"omitempty,max=200"`
	Artist  string `json:"artist" validate:"omitempty,max=200"`
	Album   string `json:"album" validate:"omitempty,max=200"`
	Year    *int   `json:"year" validate:"omitempty,min=1900,max=2100"`
	Credits string `json:"credits" validate:"omitempty,max=1000"`
}

// ExportMP3Response represents the response for MP3 export
type ExportMP3Response struct {
	FileURL   string    `json:"fileUrl"`
	Size      int64     `json:"size"`
	Format    string    `json:"format"`
	Quality   int       `json:"quality"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// ExportWAVRequest represents the request for WAV export
type ExportWAVRequest struct {
	ProjectID     string `json:"projectId" validate:"required,uuid"`
	MasterFileURL string `json:"masterFileUrl" validate:"required,url"`
	BitDepth      *int   `json:"bitDepth" validate:"omitempty,oneof=16 24 32"`
	SampleRate    *int   `json:"sampleRate" validate:"omitempty,oneof=44100 48000 96000"`
}

// ExportWAVResponse represents the response for WAV export
type ExportWAVResponse struct {
	FileURL    string    `json:"fileUrl"`
	Size       int64     `json:"size"`
	Format     string    `json:"format"`
	BitDepth   int       `json:"bitDepth"`
	SampleRate int       `json:"sampleRate"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// ExportStemsRequest represents the request for stems export
type ExportStemsRequest struct {
	ProjectID     string   `json:"projectId" validate:"required,uuid"`
	StemURLs      []string `json:"stemUrls" validate:"required,min=1,dive,url"`
	Format        string   `json:"format" validate:"omitempty,oneof=wav mp3"`
	IncludeVocals bool     `json:"includeVocals"`
	VocalURLs     []string `json:"vocalUrls" validate:"omitempty,dive,url"`
	IncludeMaster bool     `json:"includeMaster"`
	MasterURL     string   `json:"masterUrl" validate:"omitempty,url"`
}

// ExportStemsResponse represents the response for stems export
type ExportStemsResponse struct {
	FileURL   string    `json:"fileUrl"`
	Size      int64     `json:"size"`
	FileCount int       `json:"fileCount"`
	ExpiresAt time.Time `json:"expiresAt"`
}
