package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makeasinger/api/internal/client"
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

// ImportCanceler cancels an import job, refunds it and schedules the source
// cleanup.
type ImportCanceler interface {
	Cancel(ctx context.Context, userID, jobID string) error
}

// JobCenter reads jobs of every type and dispatches cancel and retry by type.
type JobCenter struct {
	repo   jobs.Repo
	render *RenderService
	master *MasterService
	cancel JobCanceler
	retry  LyricsRetrier
	imp    ImportCanceler
	store  client.StorageClient
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

// WithImport wires the import side. Without it an import job answers cancel
// with ErrJobNotFound. Retry of an import job is always ErrNotRetryable here;
// POST /api/imports/:jobId/retry is its own endpoint.
func (c *JobCenter) WithImport(cancel ImportCanceler) *JobCenter {
	c.imp = cancel
	return c
}

// WithImportStorage gives Detail the storage that signs an import result.
func (c *JobCenter) WithImportStorage(storage client.StorageClient) *JobCenter {
	c.store = storage
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
	case model.JobTypeImport:
		if job.Status != model.JobStatusSucceeded || len(job.Result) == 0 {
			return d, nil
		}
		var record model.ImportRecord
		if err := json.Unmarshal(job.Result, &record); err != nil {
			return nil, fmt.Errorf("failed to unmarshal import result: %w", err)
		}
		res, err := BuildImportResult(ctx, c.store, &record)
		if err != nil {
			return nil, err
		}
		if d.Result, err = json.Marshal(res); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// BuildImportResult signs the stored keys of an import result. Signed URLs
// are never stored: every call signs again.
func BuildImportResult(ctx context.Context, storage client.StorageClient, rec *model.ImportRecord) (*model.ImportResult, error) {
	if storage == nil {
		return nil, errors.New("no storage to sign the import result")
	}
	res := &model.ImportResult{
		DurationMs: rec.DurationMs, BPM: rec.BPM, Key: rec.Key,
		Structure: rec.Structure, Lyrics: rec.Lyrics, LyricTimeline: rec.LyricTimeline,
		Stems: make([]model.StemResult, 0, len(rec.Stems)),
	}
	for _, st := range rec.Stems {
		if st.Key == "" {
			return nil, fmt.Errorf("the import stem record has no object key: %s", st.ID)
		}
		u, exp, err := storage.URLFor(ctx, st.Key)
		if err != nil {
			return nil, fmt.Errorf("could not generate the stem URL: %w", err)
		}
		res.Stems = append(res.Stems, model.StemResult{
			ID: st.ID, Instrument: st.Instrument, FileURL: u, Duration: st.Duration,
			ExpiresAt: model.ExpiresPtr(exp),
		})
	}
	if rec.Guide != nil && rec.Guide.NotesKey != nil && *rec.Guide.NotesKey != "" {
		u, _, err := storage.URLFor(ctx, *rec.Guide.NotesKey)
		if err != nil {
			return nil, fmt.Errorf("could not generate the notes URL: %w", err)
		}
		res.Guide = &model.GuideResult{NotesURL: &u, PitchShiftSemitones: rec.Guide.PitchShiftSemitones}
	}
	return res, nil
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
	case model.JobTypeImport:
		if c.imp == nil {
			return ErrJobNotFound
		}
		return c.imp.Cancel(ctx, userID, id)
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
