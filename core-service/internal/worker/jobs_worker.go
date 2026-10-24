package worker

import (
	"context"
	"log"
	"time"

	"github.com/hibiken/asynq"
)

const (
	// TaskTypeJobsSweep deletes expired jobs and old events.
	TaskTypeJobsSweep = "uretim:temizle"
	// TaskTypeLyricsStale fails idle lyrics jobs.
	TaskTypeLyricsStale = "soz:bayat-tara"
	// QueueMaintenance is the queue of the periodic sweeps.
	QueueMaintenance = "bakim"
)

// JobSweeper is the retention part of the job store.
type JobSweeper interface {
	Sweep(ctx context.Context, now time.Time) (jobs, events int64, err error)
}

// StaleLyricsFailer ends the lyrics jobs nobody is working on.
type StaleLyricsFailer interface {
	FailStale(ctx context.Context, now time.Time) (int, error)
}

// JobsWorker runs the periodic job maintenance tasks.
type JobsWorker struct {
	sweeper JobSweeper
	lyrics  StaleLyricsFailer
}

func NewJobsWorker(sweeper JobSweeper, lyrics StaleLyricsFailer) *JobsWorker {
	return &JobsWorker{sweeper: sweeper, lyrics: lyrics}
}

// Sweep deletes jobs past their expiry and events 24 h after their job ended.
func (w *JobsWorker) Sweep(ctx context.Context, _ *asynq.Task) error {
	if w.sweeper == nil {
		return nil
	}
	jobs, events, err := w.sweeper.Sweep(ctx, time.Now())
	if err != nil {
		return err
	}
	if jobs > 0 || events > 0 {
		log.Printf("jobs: swept %d expired job(s) and %d old event(s)", jobs, events)
	}
	return nil
}

// FailStaleLyrics fails lyrics jobs whose worker was lost.
func (w *JobsWorker) FailStaleLyrics(ctx context.Context, _ *asynq.Task) error {
	if w.lyrics == nil {
		return nil
	}
	n, err := w.lyrics.FailStale(ctx, time.Now())
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("jobs: failed %d stale lyrics job(s)", n)
	}
	return nil
}
