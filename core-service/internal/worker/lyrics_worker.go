package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// errLyricsCanceled stops a provider stream whose job was canceled.
var errLyricsCanceled = errors.New("lyrics job canceled")

// sectionAttempts is the first try plus the one automatic retry.
const sectionAttempts = 2

// cancelPollEvery is how often a running section looks at its job row, for a
// cancel whose task interrupt did not arrive.
const cancelPollEvery = 3 * time.Second

// LyricsWorker writes a lyrics job section by section: every line is stored
// and emitted at once, so the client sees it live or reads it later.
type LyricsWorker struct {
	repo    jobs.Repo
	gen     service.LyricsGenerator
	credits credit.Spender

	sectionTimeout time.Duration
	isLastAttempt  func(context.Context) bool
}

func NewLyricsWorker(repo jobs.Repo, gen service.LyricsGenerator, credits credit.Spender) *LyricsWorker {
	return &LyricsWorker{
		repo: repo, gen: gen, credits: credits,
		sectionTimeout: service.LyricsSectionTimeout,
		isLastAttempt:  asynqIsLastAttempt,
	}
}

func isTerminalLyrics(st model.JobStatus) bool {
	return st == model.JobStatusSucceeded || st == model.JobStatusFailed || st == model.JobStatusCanceled
}

// ProcessTask runs one TaskTypeLyrics task. A run that is interrupted (pod
// stop, task timeout) returns an error so asynq tries again; the next run
// resumes at the first section that is not done or failed.
func (w *LyricsWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p service.LyricsTaskPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("could not decode the lyrics task payload: %v: %w", err, asynq.SkipRetry)
	}
	job, err := w.repo.Get(ctx, p.JobID)
	if errors.Is(err, jobs.ErrJobNotFound) {
		return nil
	}
	if err != nil {
		return w.giveUp(ctx, p.JobID, err, false)
	}
	if isTerminalLyrics(job.Status) {
		return nil
	}
	var in model.LyricsJobInput
	if err := json.Unmarshal(job.Payload, &in); err != nil {
		return w.giveUp(ctx, p.JobID, fmt.Errorf("lyrics job %s: bad input: %v: %w", p.JobID, err, asynq.SkipRetry), true)
	}
	res, err := model.ParseLyricsJobResult(job.Result)
	if err != nil {
		return w.giveUp(ctx, p.JobID, fmt.Errorf("lyrics job %s: bad result: %v: %w", p.JobID, err, asynq.SkipRetry), true)
	}

	err = w.repo.UpdateWith(ctx, p.JobID, func(j *model.Job, _ jobs.Emitter) error {
		if isTerminalLyrics(j.Status) {
			return jobs.ErrSkipWrite
		}
		j.Status, j.CurrentStep, j.Progress = model.JobStatusRunning, service.LyricsStepWriting, res.Progress()
		if j.StartedAt == nil {
			now := time.Now().UTC()
			j.StartedAt = &now
		}
		return nil
	})
	if errors.Is(err, jobs.ErrSkipWrite) {
		return nil
	}
	if err != nil {
		return w.giveUp(ctx, p.JobID, err, false)
	}

	// song is the context sent to the model: the request's song with the lines
	// this job has written so far (also those of an earlier run).
	song := append([]model.LyricsSongSection(nil), in.Song...)
	for _, sr := range res.Sections {
		if sr.Status == model.LyricsSectionDone && sr.Index < len(song) {
			song[sr.Index].Lyrics = sr.Text()
		}
	}
	for _, sec := range in.Sections {
		if sr := res.Section(sec.Index); sr != nil &&
			(sr.Status == model.LyricsSectionDone || sr.Status == model.LyricsSectionFailed) {
			continue
		}
		lines, outcome, err := w.writeSection(ctx, p.JobID, &in, sec, song)
		switch outcome {
		case outcomeCanceled:
			return nil
		case outcomeInterrupted:
			return w.interrupted(ctx, p.JobID, err)
		case outcomeError:
			return w.giveUp(ctx, p.JobID, err, false)
		case outcomeDone:
			song[sec.Index].Lyrics = strings.Join(lines, "\n")
		}
	}
	if err := w.finish(ctx, p.JobID, false); err != nil {
		return w.giveUp(ctx, p.JobID, err, false)
	}
	return nil
}

type sectionOutcome int

const (
	outcomeDone sectionOutcome = iota
	outcomeFailed
	outcomeCanceled
	outcomeInterrupted
	outcomeError
)

// writeSection writes one section with its automatic retry. Failure of the
// section is a stored result (outcomeFailed); outcomeError is a store failure.
func (w *LyricsWorker) writeSection(ctx context.Context, jobID string, in *model.LyricsJobInput, sec model.LyricsJobSection, song []model.LyricsSongSection) ([]string, sectionOutcome, error) {
	idx := sec.Index
	for attempt := 0; attempt < sectionAttempts; attempt++ {
		err := w.repo.UpdateWith(ctx, jobID, func(j *model.Job, em jobs.Emitter) error {
			if isTerminalLyrics(j.Status) {
				return jobs.ErrSkipWrite
			}
			return mutateSection(j, func(res *model.LyricsJobResult, sr *model.LyricsJobSectionResult) {
				sr.Status, sr.Lines = model.LyricsSectionWriting, []string{}
				em.Emit(jobs.KindSectionStarted, map[string]any{"section": idx})
			}, idx)
		})
		if errors.Is(err, jobs.ErrSkipWrite) {
			return nil, outcomeCanceled, nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, outcomeInterrupted, ctx.Err()
			}
			return nil, outcomeError, err
		}

		lines, gerr := w.callProvider(ctx, jobID, in, sec, song)
		switch {
		case errors.Is(gerr, errLyricsCanceled):
			return nil, outcomeCanceled, nil
		case ctx.Err() != nil:
			if w.canceled(jobID) {
				return nil, outcomeCanceled, nil
			}
			return nil, outcomeInterrupted, ctx.Err()
		case gerr == nil && len(lines) >= 1:
			err := w.repo.UpdateWith(ctx, jobID, func(j *model.Job, em jobs.Emitter) error {
				if isTerminalLyrics(j.Status) {
					return jobs.ErrSkipWrite
				}
				return mutateSection(j, func(res *model.LyricsJobResult, sr *model.LyricsJobSectionResult) {
					sr.Status = model.LyricsSectionDone
					j.Progress = res.Progress()
					em.Emit(jobs.KindSectionDone, map[string]any{"section": idx})
				}, idx)
			})
			if errors.Is(err, jobs.ErrSkipWrite) {
				return nil, outcomeCanceled, nil
			}
			if err != nil {
				return nil, outcomeError, err
			}
			return lines, outcomeDone, nil
		}
		log.Printf("lyrics job %s section %d attempt %d: %d lines, err=%v", jobID, idx, attempt+1, len(lines), gerr)
	}
	err := w.repo.UpdateWith(ctx, jobID, func(j *model.Job, em jobs.Emitter) error {
		if isTerminalLyrics(j.Status) {
			return jobs.ErrSkipWrite
		}
		return mutateSection(j, func(res *model.LyricsJobResult, sr *model.LyricsJobSectionResult) {
			sr.Status, sr.Lines = model.LyricsSectionFailed, []string{}
			j.Progress = res.Progress()
			em.Emit(jobs.KindSectionFailed, map[string]any{"section": idx, "code": service.LyricsErrorCode})
		}, idx)
	})
	if errors.Is(err, jobs.ErrSkipWrite) {
		return nil, outcomeCanceled, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, outcomeInterrupted, ctx.Err()
		}
		return nil, outcomeError, err
	}
	return nil, outcomeFailed, nil
}

// mutateSection applies fn to the result entry of song index idx and stores
// the result back into the job.
func mutateSection(j *model.Job, fn func(*model.LyricsJobResult, *model.LyricsJobSectionResult), idx int) error {
	res, err := model.ParseLyricsJobResult(j.Result)
	if err != nil {
		return err
	}
	sr := res.Section(idx)
	if sr == nil {
		return fmt.Errorf("lyrics job %s has no section %d", j.ID, idx)
	}
	fn(res, sr)
	j.Result, err = json.Marshal(res)
	return err
}

// callProvider streams one section from the provider under the section
// timeout. Each non-blank line is stored and emitted before the next one is
// taken; a canceled job row stops the stream.
func (w *LyricsWorker) callProvider(ctx context.Context, jobID string, in *model.LyricsJobInput, sec model.LyricsJobSection, song []model.LyricsSongSection) ([]string, error) {
	actx, cancel := context.WithTimeout(ctx, w.sectionTimeout)
	defer cancel()
	go func() { // a cancel whose task interrupt did not arrive
		tick := time.NewTicker(cancelPollEvery)
		defer tick.Stop()
		for {
			select {
			case <-actx.Done():
				return
			case <-tick.C:
				if w.canceled(jobID) {
					cancel()
					return
				}
			}
		}
	}()

	idx := sec.Index
	var lines []string
	onLine := func(line string) error {
		if strings.TrimSpace(line) == "" {
			return nil
		}
		err := w.repo.UpdateWith(ctx, jobID, func(j *model.Job, em jobs.Emitter) error {
			if isTerminalLyrics(j.Status) {
				return jobs.ErrSkipWrite
			}
			if err := mutateSection(j, func(_ *model.LyricsJobResult, sr *model.LyricsJobSectionResult) {
				sr.Lines = append(sr.Lines, line)
			}, idx); err != nil {
				return err
			}
			em.Emit(jobs.KindLine, map[string]any{"section": idx, "text": line})
			return nil
		})
		if errors.Is(err, jobs.ErrSkipWrite) {
			return errLyricsCanceled
		}
		if err != nil {
			return err
		}
		lines = append(lines, line)
		return nil
	}

	var err error
	if in.Mode == model.LyricsModeRewrite {
		_, err = w.gen.RewriteStream(actx, in.RewriteFor(sec, song), onLine)
	} else {
		_, err = w.gen.GenerateStream(actx, in.GenerateFor(sec, song), onLine)
	}
	if err != nil && w.canceled(jobID) {
		err = errLyricsCanceled
	}
	return lines, err
}

// canceled reads the job row on a context of its own (the caller's may be
// the one that was canceled).
func (w *LyricsWorker) canceled(jobID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j, err := w.repo.Get(ctx, jobID)
	return err == nil && j.Status == model.JobStatusCanceled
}

// interrupted handles a run cut short by the context. On the last asynq
// attempt the unfinished sections are failed so the job ends (and the user can
// retry them); earlier, the error makes asynq run the task again.
func (w *LyricsWorker) interrupted(ctx context.Context, jobID string, cause error) error {
	if w.canceled(jobID) {
		return nil
	}
	return w.giveUp(ctx, jobID, cause, false)
}

// finish ends the job (see service.FinishLyricsJob).
func (w *LyricsWorker) finish(ctx context.Context, jobID string, failUnfinished bool) error {
	return service.FinishLyricsJob(ctx, w.repo, w.credits, jobID, failUnfinished)
}

// giveUp is the exit of every error path. Unless asynq will run the task
// again, the job must still end: left queued or running it would hold the
// project's single lyrics slot forever and keep its charge.
func (w *LyricsWorker) giveUp(ctx context.Context, jobID string, err error, final bool) error {
	if !final && !w.isLastAttempt(ctx) {
		return err
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if ferr := w.finish(fctx, jobID, true); ferr != nil {
		log.Printf("lyrics job %s: could not end the job: %v", jobID, ferr)
	}
	return err
}
