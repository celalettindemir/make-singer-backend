package model

import "time"

// Job represents a background job in the system
type Job struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"` // "render" or "master"
	OwnerID     string     `json:"ownerId"`
	Status      JobStatus  `json:"status"`
	Progress    int        `json:"progress"`
	CurrentStep string     `json:"currentStep,omitempty"`
	Error       *string    `json:"error,omitempty"`
	Payload     []byte     `json:"-"`                // Stored as JSON
	Result      []byte     `json:"result,omitempty"` // Stored as JSON
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	RetryCount  int        `json:"retryCount"`
}

// IsOwnedBy: a job belongs only to the user who started it. A record without
// an OwnerID (from before Phase 3) is opened to nobody; records live for 24
// hours, so no migration is needed.
func (j *Job) IsOwnedBy(userID string) bool {
	return userID != "" && j.OwnerID == userID
}

// Job types
const (
	JobTypeRender = "render"
	JobTypeMaster = "master"
)

// RenderJobPayload contains the data for a render job
type RenderJobPayload struct {
	OwnerID         string        `json:"ownerId"`
	ProjectID       string        `json:"projectId"`
	Brief           Brief         `json:"brief"`
	Arrangement     Arrangement   `json:"arrangement"`
	StemMode        StemMode      `json:"stemMode"`
	FocusInstrument *Instrument   `json:"focusInstrument,omitempty"`
	Guide           *GuideRequest `json:"guide,omitempty"`
}

// MasterJobPayload contains the data for a master job
type MasterJobPayload struct {
	OwnerID     string        `json:"ownerId"`
	ProjectID   string        `json:"projectId"`
	Profile     MasterProfile `json:"profile"`
	StemURLs    []string      `json:"stemUrls"`
	MixSnapshot MixSnapshot   `json:"mixSnapshot"`
	VocalTakes  []VocalTake   `json:"vocalTakes,omitempty"`
}
