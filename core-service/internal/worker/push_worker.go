package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/push"
)

const (
	// TaskTypePush sends the push notification of a finished job.
	TaskTypePush = "bildirim:gonder"
	// PushQueue is the asynq queue of TaskTypePush.
	PushQueue = "bildirim"
)

// PushTaskPayload is the payload of a TaskTypePush task.
type PushTaskPayload struct {
	JobID string `json:"jobId"`
}

// taskEnqueuer is the part of *asynq.Client the push hook uses.
type taskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// EnqueuePush queues the push of a finished job. It is fast and meant for the
// store's OnFinished hook; an error is only logged there.
func EnqueuePush(c taskEnqueuer, jobID string) error {
	payload, err := json.Marshal(PushTaskPayload{JobID: jobID})
	if err != nil {
		return err
	}
	_, err = c.Enqueue(asynq.NewTask(TaskTypePush, payload),
		asynq.Queue(PushQueue), asynq.MaxRetry(3))
	return err
}

// PushRepo is the part of the job store the push worker reads and prunes.
type PushRepo interface {
	Get(ctx context.Context, id string) (*model.Job, error)
	Devices(ctx context.Context, userID string) ([]model.Device, error)
	DeleteToken(ctx context.Context, token string) error
}

var _ PushRepo = (*jobs.PostgresStore)(nil)

// PushWorker sends the finish notification of a job to every device of its owner.
type PushWorker struct {
	repo     PushRepo
	sender   push.Sender
	enforced func() bool
	enabled  bool
}

func NewPushWorker(repo PushRepo, sender push.Sender, enforced func() bool, enabled bool) *PushWorker {
	if enforced == nil {
		enforced = func() bool { return false }
	}
	return &PushWorker{repo: repo, sender: sender, enforced: enforced, enabled: enabled}
}

// ProcessTask runs one TaskTypePush task. It fails (so asynq retries) only when
// every device failed with something other than an unregistered token.
func (w *PushWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p PushTaskPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("could not decode the push task payload: %v: %w", err, asynq.SkipRetry)
	}
	if !w.enabled || w.sender == nil {
		log.Printf("push: disabled, job %s not notified", p.JobID)
		return nil
	}
	job, err := w.repo.Get(ctx, p.JobID)
	if errors.Is(err, jobs.ErrJobNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if job.Status != model.JobStatusSucceeded && job.Status != model.JobStatusFailed {
		return nil
	}
	devices, err := w.repo.Devices(ctx, job.OwnerID)
	if err != nil {
		return err
	}
	enforced := w.enforced()
	failures := 0
	var lastErr error
	for _, d := range devices {
		err := w.sender.Send(ctx, d.Token, push.Compose(*job, d.Language, enforced))
		switch {
		case err == nil:
		case errors.Is(err, push.ErrUnregistered):
			if derr := w.repo.DeleteToken(ctx, d.Token); derr != nil {
				log.Printf("push: could not delete an unregistered token: %v", derr)
			}
		default:
			failures++
			lastErr = err
			log.Printf("push: send for job %s failed: %v", job.ID, err)
		}
	}
	if len(devices) > 0 && failures == len(devices) {
		return fmt.Errorf("push: every device failed for job %s: %w", job.ID, lastErr)
	}
	return nil
}
