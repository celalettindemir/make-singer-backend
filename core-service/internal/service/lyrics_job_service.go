package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

const (
	// TaskTypeLyrics is the asynq task that writes a lyrics job.
	TaskTypeLyrics = "soz:yaz"
	// LyricsQueue is the asynq queue of lyrics jobs.
	LyricsQueue = "soz"
	// LyricsSectionTimeout is the budget of one section attempt.
	LyricsSectionTimeout = 90 * time.Second
	// lyricsJobTimeoutCap caps the asynq timeout of a whole job.
	lyricsJobTimeoutCap = 10 * time.Minute
	// lyricsRetrySpan is how long after finishing a job can be retried.
	lyricsRetrySpan = 24 * time.Hour

	// Steps shown while and after writing.
	LyricsStepWriting = "Writing lyrics"
	LyricsStepDone    = "Lyrics written"
	// LyricsErrorCode is the error code of a lyrics job that wrote nothing.
	LyricsErrorCode = "AI_ERROR"
)

// ErrActionIDRequired: credits are enforced and the request carries no valid
// X-Action-Id.
var ErrActionIDRequired = errors.New("action id required")

// LyricsJobInvalidError carries the field errors of a rejected request.
type LyricsJobInvalidError struct{ Details map[string]string }

func (e *LyricsJobInvalidError) Error() string { return "invalid lyrics job request" }

// LyricsTaskPayload is the payload of a TaskTypeLyrics task.
type LyricsTaskPayload struct {
	JobID string `json:"jobId"`
}

// sectionActionID derives the action id that pays for one rewrite section of
// one attempt, so each charge (and its refund) is idempotent on its own.
func sectionActionID(actionID string, section, attempt int) string {
	return uuid.NewSHA1(uuid.MustParse(actionID), []byte(fmt.Sprintf("%d:%d", section, attempt))).String()
}

// LyricsJobService starts, cancels and retries lyrics jobs; the asynq worker
// does the writing.
type LyricsJobService struct {
	repo      jobs.Repo
	client    *asynq.Client
	inspector *asynq.Inspector
	gen       LyricsGenerator
	validate  *validator.Validate
	credits   credit.Spender
}

var (
	_ JobCanceler   = (*LyricsJobService)(nil)
	_ LyricsRetrier = (*LyricsJobService)(nil)
)

func NewLyricsJobService(repo jobs.Repo, asynqClient *asynq.Client, inspector *asynq.Inspector, gen LyricsGenerator, v *validator.Validate) *LyricsJobService {
	return &LyricsJobService{repo: repo, client: asynqClient, inspector: inspector, gen: gen, validate: v}
}

// WithCredits turns on charging; without it nothing is charged and the
// balance is 0.
func (s *LyricsJobService) WithCredits(sp credit.Spender) *LyricsJobService {
	s.credits = sp
	return s
}

func lyricsOp(mode string) credit.Operation {
	if mode == model.LyricsModeRewrite {
		return credit.OpLyricsRewrite
	}
	return credit.OpLyricsGenerate
}

// validateRequest checks everything that can be checked before a charge.
func (s *LyricsJobService) validateRequest(req *model.LyricsJobRequest) error {
	bad := func(field, why string) error {
		return &LyricsJobInvalidError{Details: map[string]string{field: why}}
	}
	if err := s.validate.Struct(req); err != nil {
		details := map[string]string{}
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			for _, e := range ve {
				details[e.Field()] = e.Tag()
			}
		}
		return &LyricsJobInvalidError{Details: details}
	}
	seen := map[int]bool{}
	for _, sec := range req.Sections {
		if sec.Index >= len(req.Song) {
			return bad("sections", "index_out_of_range")
		}
		if seen[sec.Index] {
			return bad("sections", "duplicate_index")
		}
		seen[sec.Index] = true
		// Each derived request must pass the same checks as the stream routes.
		if req.Mode == model.LyricsModeRewrite {
			if strings.TrimSpace(req.Song[sec.Index].Lyrics) == "" {
				return bad("song", "lyrics_required")
			}
			d := req.RewriteFor(sec, req.Song)
			if s.validate.Struct(d) != nil || d.CheckIndex(d.SectionType) != nil {
				return bad("sections", "invalid")
			}
		} else {
			d := req.GenerateFor(sec, req.Song)
			if s.validate.Struct(d) != nil || d.CheckIndex(d.SectionType) != nil {
				return bad("sections", "invalid")
			}
		}
	}
	return nil
}

// charge applies the credit rules of a start. Mock provider and shadow mode
// charge nothing and only read the balance. Generate is one action charge;
// rewrite is one charge per section with a derived action id. On the first
// error the sections already charged are refunded. baseID is the canonical
// action id, "" when nothing was charged by rule.
func (s *LyricsJobService) charge(ctx context.Context, userID, actionID string, req *model.LyricsJobRequest) (charges []model.LyricsCharge, balance int, baseID string, err error) {
	if s.credits == nil {
		return nil, 0, "", nil
	}
	if s.gen.IsMock() || !s.credits.Enforced() {
		bal, err := s.credits.Balance(ctx, userID)
		if err != nil {
			if s.credits.Enforced() {
				return nil, 0, "", err
			}
			// Shadow mode: nothing is charged, so a failed balance read
			// must not fail the start.
			log.Printf("lyrics job: shadow balance read failed: %v", err)
			bal = 0
		}
		return nil, bal, "", nil
	}
	id, perr := uuid.Parse(actionID)
	if perr != nil {
		return nil, 0, "", ErrActionIDRequired
	}
	baseID = id.String()
	op := lyricsOp(req.Mode)
	if req.Mode == model.LyricsModeGenerate {
		bal, charged, err := s.credits.SpendAction(ctx, userID, op, baseID)
		if err != nil {
			return nil, 0, "", err
		}
		if charged {
			charges = append(charges, model.LyricsCharge{Section: -1, ActionID: baseID, Op: string(op)})
		}
		return charges, bal, baseID, nil
	}
	for _, sec := range req.Sections {
		aid := sectionActionID(baseID, sec.Index, 0)
		bal, charged, err := s.credits.SpendAction(ctx, userID, op, aid)
		if err != nil {
			RefundLyricsCharges(s.credits, userID, charges)
			return nil, 0, "", err
		}
		balance = bal
		if charged {
			charges = append(charges, model.LyricsCharge{Section: sec.Index, ActionID: aid, Op: string(op)})
		}
	}
	return charges, balance, baseID, nil
}

// RefundLyricsCharges refunds the charges with a fresh context (the caller's
// may already be canceled). Refunds are idempotent in the ledger; a failed one
// is logged, since the caller already has its own error to report.
func RefundLyricsCharges(sp credit.Spender, userID string, charges []model.LyricsCharge) {
	if sp == nil {
		return
	}
	for _, c := range charges {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := sp.RefundAction(ctx, userID, credit.Operation(c.Op), c.ActionID); err != nil {
			log.Printf("lyrics job: refund action %s: %v", c.ActionID, err)
		}
		cancel()
	}
}

// Start validates, charges, stores and enqueues a lyrics job.
func (s *LyricsJobService) Start(ctx context.Context, userID, actionID string, req *model.LyricsJobRequest) (*model.LyricsJobStartResponse, error) {
	if err := s.validateRequest(req); err != nil {
		return nil, err
	}
	charges, balance, baseID, err := s.charge(ctx, userID, actionID, req)
	if err != nil {
		return nil, err
	}
	input := model.LyricsJobInput{LyricsJobRequest: *req, ActionID: baseID, Charges: charges}
	payload, err := json.Marshal(input)
	if err != nil {
		RefundLyricsCharges(s.credits, userID, charges)
		return nil, err
	}
	result, err := json.Marshal(model.NewLyricsJobResult(req.Sections))
	if err != nil {
		RefundLyricsCharges(s.credits, userID, charges)
		return nil, err
	}
	job := &model.Job{
		ID: uuid.NewString(), Type: model.JobTypeLyrics, OwnerID: userID, Status: model.JobStatusQueued,
		ProjectID: req.ProjectID, ProjectName: req.ProjectName, Payload: payload, Result: result,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, job); err != nil {
		RefundLyricsCharges(s.credits, userID, charges)
		return nil, err
	}
	if err := s.enqueue(job.ID, 0, len(req.Sections)); err != nil {
		RefundLyricsCharges(s.credits, userID, charges)
		// The queued row would block the project's single active job slot.
		_ = s.repo.Update(context.Background(), job.ID, func(j *model.Job) error {
			j.Status = model.JobStatusCanceled
			return nil
		})
		return nil, fmt.Errorf("failed to enqueue lyrics job: %w", err)
	}
	return &model.LyricsJobStartResponse{JobID: job.ID, Status: model.JobStatusQueued, Balance: balance}, nil
}

func lyricsTaskID(jobID string, attempt int) string { return jobID + ":" + strconv.Itoa(attempt) }

func (s *LyricsJobService) enqueue(jobID string, attempt, sections int) error {
	payload, err := json.Marshal(LyricsTaskPayload{JobID: jobID})
	if err != nil {
		return err
	}
	timeout := time.Duration(sections) * LyricsSectionTimeout
	if timeout > lyricsJobTimeoutCap {
		timeout = lyricsJobTimeoutCap
	}
	_, err = s.client.Enqueue(asynq.NewTask(TaskTypeLyrics, payload),
		asynq.Queue(LyricsQueue),
		asynq.TaskID(lyricsTaskID(jobID, attempt)),
		asynq.MaxRetry(2),
		asynq.Timeout(timeout),
		asynq.Retention(24*time.Hour),
	)
	return err
}

func isTerminalStatus(st model.JobStatus) bool {
	return st == model.JobStatusSucceeded || st == model.JobStatusFailed || st == model.JobStatusCanceled
}

func decodeLyricsJob(job *model.Job) (model.LyricsJobInput, *model.LyricsJobResult, error) {
	var in model.LyricsJobInput
	if err := json.Unmarshal(job.Payload, &in); err != nil {
		return in, nil, fmt.Errorf("lyrics job %s: bad input: %w", job.ID, err)
	}
	res, err := model.ParseLyricsJobResult(job.Result)
	if err != nil {
		return in, nil, fmt.Errorf("lyrics job %s: bad result: %w", job.ID, err)
	}
	return in, res, nil
}

// cancelRefunds applies the cancel refund rules. No line written in the whole
// job: everything charged. Otherwise a generate job refunds nothing and a
// rewrite job the sections that never started (still pending).
func cancelRefunds(in model.LyricsJobInput, res *model.LyricsJobResult) []model.LyricsCharge {
	if !res.AnyLine() {
		return in.Charges
	}
	if in.Mode != model.LyricsModeRewrite {
		return nil
	}
	var out []model.LyricsCharge
	for _, c := range in.Charges {
		if sr := res.Section(c.Section); sr != nil && sr.Status == model.LyricsSectionPending {
			out = append(out, c)
		}
	}
	return out
}

// Cancel stops an owned, unfinished lyrics job. The row is set canceled
// first, so the worker stops writing at its next line; then the running task
// is interrupted.
func (s *LyricsJobService) Cancel(ctx context.Context, userID, jobID string) error {
	var refunds []model.LyricsCharge
	var attempt int
	err := s.repo.UpdateWith(ctx, jobID, func(j *model.Job, _ jobs.Emitter) error {
		if !j.IsOwnedBy(userID) || j.Type != model.JobTypeLyrics {
			return ErrJobNotFound
		}
		if isTerminalStatus(j.Status) {
			return ErrJobAlreadyCompleted
		}
		in, res, err := decodeLyricsJob(j)
		if err != nil {
			return err
		}
		refunds, attempt = cancelRefunds(in, res), in.Attempt
		j.Status = model.JobStatusCanceled
		return nil
	})
	if err != nil {
		return err
	}
	RefundLyricsCharges(s.credits, userID, refunds)
	if s.inspector != nil {
		if err := s.inspector.CancelProcessing(lyricsTaskID(jobID, attempt)); err != nil {
			log.Printf("lyrics job %s: cancel processing: %v", jobID, err)
		}
	}
	return nil
}

func retryable(j *model.Job, res *model.LyricsJobResult) bool {
	return j.Type == model.JobTypeLyrics && j.Status == model.JobStatusSucceeded &&
		len(res.FailedSections()) > 0 &&
		j.CompletedAt != nil && time.Since(*j.CompletedAt) < lyricsRetrySpan
}

// Retry writes the failed sections of a succeeded lyrics job again. A rewrite
// charges each of them again (a failed one was refunded); a generate retry is
// free. 402 from the charge aborts with nothing changed.
func (s *LyricsJobService) Retry(ctx context.Context, userID, jobID string) error {
	job, err := s.repo.GetOwned(ctx, userID, jobID)
	if err != nil {
		return err
	}
	in, res, err := decodeLyricsJob(job)
	if err != nil || !retryable(job, res) {
		if err != nil && job.Type == model.JobTypeLyrics {
			return err
		}
		return ErrNotRetryable
	}
	attempt := in.Attempt + 1
	var charges []model.LyricsCharge
	if in.Mode == model.LyricsModeRewrite && in.ActionID != "" && s.credits != nil && s.credits.Enforced() {
		for _, idx := range res.FailedSections() {
			aid := sectionActionID(in.ActionID, idx, attempt)
			_, charged, err := s.credits.SpendAction(ctx, userID, credit.OpLyricsRewrite, aid)
			if err != nil {
				RefundLyricsCharges(s.credits, userID, charges)
				return err
			}
			if charged {
				charges = append(charges, model.LyricsCharge{Section: idx, ActionID: aid, Op: string(credit.OpLyricsRewrite)})
			}
		}
	}
	var snapshot model.Job
	err = s.repo.UpdateWith(ctx, jobID, func(j *model.Job, em jobs.Emitter) error {
		in, res, err := decodeLyricsJob(j)
		if err != nil {
			return err
		}
		if !retryable(j, res) || in.Attempt != attempt-1 {
			return ErrNotRetryable
		}
		snapshot = *j
		for _, idx := range res.FailedSections() {
			sr := res.Section(idx)
			sr.Status, sr.Lines = model.LyricsSectionPending, []string{}
		}
		in.Attempt = attempt
		in.Charges = append(in.Charges, charges...)
		if j.Payload, err = json.Marshal(in); err != nil {
			return err
		}
		if j.Result, err = json.Marshal(res); err != nil {
			return err
		}
		j.Status, j.CompletedAt, j.ErrorCode, j.Progress = model.JobStatusQueued, nil, "", res.Progress()
		em.Emit(jobs.KindQueued, map[string]any{})
		return nil
	})
	if err != nil {
		RefundLyricsCharges(s.credits, userID, charges)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return jobs.ErrActiveLyricsJob
		}
		return err
	}
	if err := s.enqueue(jobID, attempt, len(in.Sections)); err != nil {
		RefundLyricsCharges(s.credits, userID, charges)
		// Put the job back as it was so the user can retry again.
		_ = s.repo.Update(context.Background(), jobID, func(j *model.Job) error {
			j.Status, j.CompletedAt, j.ErrorCode, j.Progress = snapshot.Status, snapshot.CompletedAt, snapshot.ErrorCode, snapshot.Progress
			j.Payload, j.Result = snapshot.Payload, snapshot.Result
			return nil
		})
		return fmt.Errorf("failed to enqueue lyrics retry: %w", err)
	}
	return nil
}
