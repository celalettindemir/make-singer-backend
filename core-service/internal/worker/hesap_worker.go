package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/service"
)

// HesapWorker, silinen hesabin gecikmeli R2 temizligini yapar.
type HesapWorker struct {
	hesap *service.AccountService
}

func NewHesapWorker(hesap *service.AccountService) *HesapWorker {
	return &HesapWorker{hesap: hesap}
}

func (w *HesapWorker) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var yuk struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(t.Payload(), &yuk); err != nil {
		// Bozuk yuk tekrar denemekle duzelmez.
		return fmt.Errorf("hesap temizlik yuku cozulemedi: %v: %w", err, asynq.SkipRetry)
	}
	return w.hesap.Cleanup(ctx, yuk.UserID)
}
