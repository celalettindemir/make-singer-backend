package model

// HumFidelity says how closely the song follows the hum's melody.
type HumFidelity string

const (
	HumFaithful HumFidelity = "faithful"
	HumFree     HumFidelity = "free"
)

// Accepted hum length in milliseconds (the app records 5 to 30 seconds).
const (
	HumMinMs = 5000
	HumMaxMs = 30000
)

// HumTranscript holds the lyric lines found in the hum.
type HumTranscript struct {
	Lines []string `json:"lines"`
}

// HumUploadResponse is the POST /api/hum result. Transcript is always
// written: null means no usable words were found (or the step timed out).
type HumUploadResponse struct {
	HumID      string         `json:"humId"`
	FileURL    string         `json:"fileUrl"`
	DurationMs int            `json:"durationMs"`
	Transcript *HumTranscript `json:"transcript"`
}

// HumRequest asks for a song built on an uploaded hum's melody.
type HumRequest struct {
	HumID    string      `json:"humId" validate:"required,uuid"`
	Fidelity HumFidelity `json:"fidelity" validate:"required,oneof=faithful free"`
}

// HumJob is the render payload's hum: the resolved R2 key and the fidelity.
type HumJob struct {
	Key      string      `json:"key"`
	Fidelity HumFidelity `json:"fidelity"`
}
