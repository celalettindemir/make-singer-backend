package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/service"
)

// AccountWorker performs the delayed R2 cleanup of a deleted account.
type AccountWorker struct {
	accounts *service.AccountService
}

func NewAccountWorker(accountService *service.AccountService) *AccountWorker {
	return &AccountWorker{accounts: accountService}
}

func (w *AccountWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		// A malformed payload is not fixed by retrying.
		return fmt.Errorf("could not decode the account cleanup payload: %v: %w", err, asynq.SkipRetry)
	}
	return w.accounts.Cleanup(ctx, payload.UserID)
}
