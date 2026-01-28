package model

import "time"

// Job represents a background job in the system
type Job struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"` // "render" or "master"
	Status      JobStatus  `json:"status"`
	Progress    int        `json:"progress"`
	CurrentStep string     `json:"currentStep,omitempty"`
	Error       *string    `json:"error,omitempty"`
	Payload     []byte     `json:"-"` // Stored as JSON
	Result      []byte     `json:"-"` // Stored as JSON
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	RetryCount  int        `json:"retryCount"`
}

// Job types
const (
	JobTypeRender = "render"
	JobTypeMaster = "master"
)

// RenderJobPayload contains the data for a render job
type RenderJobPayload struct {
	ProjectID   string      `json:"projectId"`
	Brief       Brief       `json:"brief"`
	Arrangement Arrangement `json:"arrangement"`
}

// MasterJobPayload contains the data for a master job
type MasterJobPayload struct {
	ProjectID   string        `json:"projectId"`
	Profile     MasterProfile `json:"profile"`
	StemURLs    []string      `json:"stemUrls"`
	MixSnapshot MixSnapshot   `json:"mixSnapshot"`
	VocalTakes  []VocalTake   `json:"vocalTakes,omitempty"`
}
