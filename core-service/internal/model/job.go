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
	Payload     []byte     `json:"-"`                // Stored as JSON (column girdi)
	Result      []byte     `json:"result,omitempty"` // Stored as JSON
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	RetryCount  int        `json:"retryCount"`

	// Fields of the durable store (uretim.isler); legacy responses are built
	// field by field, so none of them is serialized.
	ProjectID   string `json:"-"`
	ProjectName string `json:"-"`
	ErrorCode   string `json:"-"`
	LastSeq     int64  `json:"-"`
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
	JobTypeLyrics = "lyrics"
)

// RenderJobPayload contains the data for a render job
type RenderJobPayload struct {
	OwnerID         string        `json:"ownerId"`
	ProjectID       string        `json:"projectId"`
	Brief           Brief         `json:"brief"`
	Arrangement     Arrangement   `json:"arrangement"`
	StemMode        StemMode      `json:"stemMode"`
	FocusInstrument *Instrument   `json:"focusInstrument,omitempty"`
	Vocal           *VocalRequest `json:"vocal,omitempty"`
	// GuideAnalysis: the note strip and key shift are made (Plus only),
	// decided server-side at start.
	GuideAnalysis bool `json:"guideAnalysis,omitempty"`
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
