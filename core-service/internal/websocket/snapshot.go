package websocket

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/makeasinger/api/internal/model"
)

// SnapshotSource reads a job's stored state (jobs.Repo.Get).
type SnapshotSource func(ctx context.Context, jobID string) (*model.Job, error)

const snapshotTimeout = 3 * time.Second

// WithSnapshot makes every new connection first get the job's current state.
// Broadcasts only carry later changes, so without it a client that connects
// after the job ended (a reconnect after the app was in the background) would
// wait on a silent socket forever.
func (h *Hub) WithSnapshot(source SnapshotSource) *Hub {
	h.snapshotSource = source
	return h
}

// snapshot is the legacy message for the job's stored state, or nil when there
// is no source, the job cannot be read, or it was canceled (no legacy message
// exists for that; the clients keep polling it as non-terminal).
// A complete message carries no result: the stored one may hold expired links,
// and the clients fetch the result endpoint anyway.
func (h *Hub) snapshot(ctx context.Context, jobID string) []byte {
	if h.snapshotSource == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	job, err := h.snapshotSource(ctx, jobID)
	if err != nil || job == nil {
		if err != nil {
			log.Printf("WebSocket snapshot of job %s failed: %v", jobID, err)
		}
		return nil
	}

	var msg any
	switch job.Status {
	case model.JobStatusSucceeded:
		msg = model.WSCompleteMessage{Type: model.WSMessageTypeComplete, JobID: job.ID}
	case model.JobStatusFailed:
		code := job.ErrorCode
		if code == "" {
			code = "RENDER_FAILED"
			if job.Type == model.JobTypeMaster {
				code = "MASTER_FAILED"
			}
		}
		message := ""
		if job.Error != nil {
			message = *job.Error
		}
		msg = model.WSErrorMessage{Type: model.WSMessageTypeError, JobID: job.ID,
			Error: model.WSError{Code: code, Message: message}}
	case model.JobStatusQueued, model.JobStatusRunning:
		msg = model.WSProgressMessage{Type: model.WSMessageTypeProgress, JobID: job.ID,
			Progress: job.Progress, Status: job.Status, CurrentStep: job.CurrentStep}
	default:
		return nil
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal snapshot message: %v", err)
		return nil
	}
	return data
}
