package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

// ErrNotRetryable: the job is not a lyrics job, has no failed section, is not
// succeeded, or finished 24 h ago or more. Handlers answer it with 400
// JOB_NOT_RETRYABLE.
var ErrNotRetryable = errors.New("job not retryable")

const (
	// jobListLimit is the contract's cap on GET /api/jobs.
	jobListLimit = 50
	// jobEventsLimit is the contract's page size of GET /api/jobs/:id/events.
	jobEventsLimit = 500
	// DefaultJobListWindow is the default of the `since` query parameter.
	DefaultJobListWindow = 24 * time.Hour
)

// JobCanceler cancels a lyrics job.
type JobCanceler interface {
	Cancel(ctx context.Context, userID, jobID string) error
}

// LyricsRetrier writes the failed sections of a lyrics job again.
type LyricsRetrier interface {
	Retry(ctx context.Context, userID, jobID string) error
}

// JobCenter reads jobs of every type and dispatches cancel and retry by type.
type JobCenter struct {
	repo   jobs.Repo
	render *RenderService
	master *MasterService
	cancel JobCanceler
	retry  LyricsRetrier
}

func NewJobCenter(repo jobs.Repo, render *RenderService, master *MasterService) *JobCenter {
	return &JobCenter{repo: repo, render: render, master: master}
}

// WithLyrics wires the lyrics side. Without it a lyrics job answers cancel and
// retry with ErrJobNotFound (no lyrics job can exist in a build that does not
// start any).
func (c *JobCenter) WithLyrics(cancel JobCanceler, retry LyricsRetrier) *JobCenter {
	c.cancel, c.retry = cancel, retry
	return c
}

// List returns every non-terminal job of the user plus the jobs that finished
// at or after since, newest first, at most 50.
func (c *JobCenter) List(ctx context.Context, userID string, since time.Time) ([]model.JobView, error) {
	all, err := c.repo.List(ctx, userID, since, jobListLimit)
	if err != nil {
		return nil, err
	}
	out := make([]model.JobView, 0, len(all))
	for i := range all {
		out = append(out, all[i].View())
	}
	return out, nil
}

// View returns one owned job without its result.
func (c *JobCenter) View(ctx context.Context, userID, id string) (*model.JobView, error) {
	job, err := c.repo.GetOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	v := job.View()
	return &v, nil
}

var nullJSON = json.RawMessage("null")

// Detail returns one owned job with its result. A render or master result is
// built with freshly signed URLs on every call.
func (c *JobCenter) Detail(ctx context.Context, userID, id string) (*model.JobDetailView, error) {
	job, err := c.repo.GetOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	d := &model.JobDetailView{JobView: job.View(), Result: nullJSON}
	switch job.Type {
	case model.JobTypeLyrics:
		if len(job.Result) > 0 {
			d.Result = json.RawMessage(job.Result)
		} else {
			d.Result = json.RawMessage(`{"sections":[]}`)
		}
	case model.JobTypeRender:
		if job.Status != model.JobStatusSucceeded || len(job.Result) == 0 {
			return d, nil
		}
		var record model.RenderResultRecord
		if err := json.Unmarshal(job.Result, &record); err != nil {
			return nil, fmt.Errorf("failed to unmarshal render result: %w", err)
		}
		res, err := c.render.BuildResult(ctx, &record)
		if err != nil {
			return nil, err
		}
		if d.Result, err = json.Marshal(res); err != nil {
			return nil, err
		}
	case model.JobTypeMaster:
		if job.Status != model.JobStatusSucceeded || len(job.Result) == 0 {
			return d, nil
		}
		res, err := c.master.BuildResult(ctx, job.Result)
		if err != nil {
			return nil, err
		}
		if d.Result, err = json.Marshal(res); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Events returns the events after afterSeq (at most 500), ascending.
func (c *JobCenter) Events(ctx context.Context, userID, id string, afterSeq int64) ([]jobs.Event, error) {
	return c.repo.EventsAfter(ctx, userID, id, afterSeq, jobEventsLimit)
}

// Cancel cancels an owned job by its type. A finished job is
// ErrJobAlreadyCompleted.
func (c *JobCenter) Cancel(ctx context.Context, userID, id string) error {
	job, err := c.repo.GetOwned(ctx, userID, id)
	if err != nil {
		return err
	}
	switch job.Type {
	case model.JobTypeRender:
		_, err = c.render.CancelRender(ctx, userID, id)
		return err
	case model.JobTypeMaster:
		return c.master.Cancel(ctx, userID, id)
	case model.JobTypeLyrics:
		if c.cancel == nil {
			return ErrJobNotFound
		}
		return c.cancel.Cancel(ctx, userID, id)
	}
	return ErrJobNotFound
}

// Retry writes the failed sections of an owned lyrics job again; any other
// job is ErrNotRetryable.
func (c *JobCenter) Retry(ctx context.Context, userID, id string) error {
	job, err := c.repo.GetOwned(ctx, userID, id)
	if err != nil {
		return err
	}
	if job.Type != model.JobTypeLyrics {
		return ErrNotRetryable
	}
	if c.retry == nil {
		return ErrJobNotFound
	}
	return c.retry.Retry(ctx, userID, id)
}
