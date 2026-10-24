package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/makeasinger/api/internal/model"
)

// ErrJobNotFound: the job does not exist OR does not belong to the requesting
// user. The two cases are deliberately not told apart; a 403 would confirm
// that the job exists.
var ErrJobNotFound = errors.New("job not found")

// ErrJobNotCompleted is returned when a result is requested before the job
// has succeeded. Handlers answer it with 400.
var ErrJobNotCompleted = errors.New("job not completed")

// ErrJobAlreadyCompleted is returned when a finished job is canceled.
// Handlers answer it with 400.
var ErrJobAlreadyCompleted = errors.New("job already completed")

// ErrSkipWrite: Update callbacks return it to leave the record untouched;
// Update returns it to the caller, which treats it as success.
var ErrSkipWrite = errors.New("skip job write")

// ErrActiveLyricsJob: the owner already has a queued or running lyrics job
// for the project (unique-index violation on Create).
var ErrActiveLyricsJob = errors.New("active lyrics job for project")

type activeJobError struct{ jobID string }

func (e *activeJobError) Error() string { return ErrActiveLyricsJob.Error() + " " + e.jobID }
func (e *activeJobError) Unwrap() error { return ErrActiveLyricsJob }

// ActiveJobID returns the id of the already active job carried by an
// ErrActiveLyricsJob error, or "" when err carries none.
func ActiveJobID(err error) string {
	var ae *activeJobError
	if errors.As(err, &ae) {
		return ae.jobID
	}
	return ""
}

const (
	// ResultRetention is how long a finished job stays readable.
	ResultRetention = 30 * 24 * time.Hour
	// EventRetention is how long the events of a finished job are kept.
	EventRetention = 24 * time.Hour
)

// Event is one entry of a job's event log. Seq starts at 1 per job.
type Event struct {
	JobID   string          `json:"jobId"`
	OwnerID string          `json:"ownerId"`
	Seq     int64           `json:"seq"`
	Kind    string          `json:"kind"`
	Data    json.RawMessage `json:"data"`
	At      time.Time       `json:"at"`
}

// Emitter lets an UpdateWith callback add events beyond the derived ones.
type Emitter interface {
	Emit(kind string, data any)
}

// Publisher is called after commit with the events of that transaction.
type Publisher interface {
	Publish(ctx context.Context, events []Event)
}

// Repo is the job store used by the services.
type Repo interface {
	// Create inserts the job and emits job.queued.
	Create(ctx context.Context, job *model.Job) error
	// Get returns ErrJobNotFound when the job is missing.
	Get(ctx context.Context, id string) (*model.Job, error)
	// GetOwned returns ErrJobNotFound for anyone but the owner.
	GetOwned(ctx context.Context, userID, id string) (*model.Job, error)
	Update(ctx context.Context, id string, fn func(*model.Job) error) error
	UpdateWith(ctx context.Context, id string, fn func(*model.Job, Emitter) error) error
	List(ctx context.Context, userID string, since time.Time, limit int) ([]model.Job, error)
	EventsAfter(ctx context.Context, userID, id string, afterSeq int64, limit int) ([]Event, error)
	Sweep(ctx context.Context, now time.Time) (jobs, events int64, err error)
	// DeleteOwner removes the user's jobs, events and devices.
	DeleteOwner(ctx context.Context, userID string) error
}

// Event kinds.
const (
	KindQueued         = "job.queued"
	KindProgress       = "job.progress"
	KindSucceeded      = "job.succeeded"
	KindFailed         = "job.failed"
	KindCanceled       = "job.canceled"
	KindSectionStarted = "lyrics.section_started"
	KindLine           = "lyrics.line"
	KindSectionDone    = "lyrics.section_done"
	KindSectionFailed  = "lyrics.section_failed"
)

func isTerminal(s model.JobStatus) bool {
	return s == model.JobStatusSucceeded || s == model.JobStatusFailed || s == model.JobStatusCanceled
}
