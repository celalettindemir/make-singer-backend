package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/googleplay"
)

const (
	notificationStoreApple  = "appstore"
	notificationStoreGoogle = "googleplay"
)

// notificationApplier is the part of *credit.Purchases the worker uses.
type notificationApplier interface {
	ApplyAppleNotification(ctx context.Context, n *appstore.Notification) error
	ApplyGoogleNotification(ctx context.Context, n *googleplay.DeveloperNotification) error
}

// refiller is the part of *credit.Purchases the refill task uses.
type refiller interface {
	RunRefill(ctx context.Context) (refilled, resynced int, err error)
}

// lotExpirer is the part of the ledger the expiry task uses.
type lotExpirer interface {
	ExpireLots(ctx context.Context, now time.Time, limit int) (int, error)
}

// campaignSweeper is the part of *credit.Campaigns the expiry task uses.
type campaignSweeper interface {
	Sweep(ctx context.Context, now time.Time) (ended, gifts int, err error)
}

// notificationVerifier is appstore.Verifier's notification check.
type notificationVerifier interface {
	Notification(signedPayload string) (*appstore.Notification, error)
}

// CreditWorker replays store webhooks that failed to process inline.
type CreditWorker struct {
	p     notificationApplier
	r     refiller
	apple notificationVerifier // nil: Apple replays are dropped

	expirer lotExpirer      // nil: ProcessExpire is a no-op
	sweeper campaignSweeper // nil: no campaign sweep
	now     func() time.Time
}

// expireBatch is the lots one ExpireLots call sweeps (P7); expireMaxBatches
// bounds one task run.
const (
	expireBatch      = 500
	expireMaxBatches = 200
)

// WithExpiry wires the kredi:sure task.
func (w *CreditWorker) WithExpiry(e lotExpirer, c campaignSweeper) *CreditWorker {
	w.expirer, w.sweeper = e, c
	return w
}

// NewCreditWorker builds the worker. A nil apple verifier drops Apple replays.
func NewCreditWorker(p *credit.Purchases, apple *appstore.Verifier) *CreditWorker {
	w := &CreditWorker{p: p, r: p}
	if apple != nil {
		w.apple = apple
	}
	return w
}

// ProcessNotification handles a kredi:bildirim task. Processing errors are
// returned so that asynq retries.
func (w *CreditWorker) ProcessNotification(ctx context.Context, t *asynq.Task) error {
	var payload struct {
		Store string `json:"store"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("could not decode the notification payload: %v: %w", err, asynq.SkipRetry)
	}
	body, err := base64.StdEncoding.DecodeString(payload.Body)
	if err != nil {
		return fmt.Errorf("could not decode the notification body: %v: %w", err, asynq.SkipRetry)
	}
	switch payload.Store {
	case notificationStoreApple:
		return w.replayApple(ctx, body)
	case notificationStoreGoogle:
		n, err := googleplay.DecodePush(body)
		if err != nil {
			return fmt.Errorf("could not decode the push body: %v: %w", err, asynq.SkipRetry)
		}
		err = w.p.ApplyGoogleNotification(ctx, n)
		if errors.Is(err, credit.ErrPurchaseInvalid) {
			log.Printf("credit worker: google notification dropped: %v", err)
			return nil
		}
		return err
	default:
		return fmt.Errorf("unknown notification store %q: %w", payload.Store, asynq.SkipRetry)
	}
}

func (w *CreditWorker) replayApple(ctx context.Context, body []byte) error {
	if w.apple == nil {
		log.Printf("credit worker: app store verifier not configured, dropping notification")
		return nil
	}
	var req struct {
		SignedPayload string `json:"signedPayload"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("could not decode the app store body: %v: %w", err, asynq.SkipRetry)
	}
	n, err := w.apple.Notification(req.SignedPayload)
	if err != nil {
		return fmt.Errorf("app store notification no longer verifies: %v: %w", err, asynq.SkipRetry)
	}
	err = w.p.ApplyAppleNotification(ctx, n)
	if errors.Is(err, credit.ErrPurchaseInvalid) {
		log.Printf("credit worker: app store notification %s dropped: %v", n.UUID, err)
		return nil
	}
	return err
}

// ProcessRefill handles a kredi:dolum task: the yearly plan refill, the
// resync of overdue subscriptions and the pruning of old actions. Errors
// are returned and fail the task; the scheduler enqueues it without
// retries, so the next tick is the retry. Every step is idempotent.
func (w *CreditWorker) ProcessRefill(ctx context.Context, _ *asynq.Task) error {
	refilled, resynced, err := w.r.RunRefill(ctx)
	if err != nil {
		return fmt.Errorf("credit refill failed: %w", err)
	}
	if refilled > 0 || resynced > 0 {
		log.Printf("credit worker: refill done refilled=%d resynced=%d", refilled, resynced)
	}
	return nil
}

// ProcessExpire handles a kredi:sure task: it sweeps expired lots in batches
// of 500, then ends campaigns by date and retries missing signup gifts. Both
// steps run even when one fails; the scheduler enqueues the task without
// retries, so the next tick is the retry. Every step is idempotent.
func (w *CreditWorker) ProcessExpire(ctx context.Context, _ *asynq.Task) error {
	now := time.Now
	if w.now != nil {
		now = w.now
	}
	var errs []error
	total := 0
	if w.expirer != nil {
		for range expireMaxBatches {
			n, err := w.expirer.ExpireLots(ctx, now(), expireBatch)
			total += n
			if err != nil {
				errs = append(errs, fmt.Errorf("credit expiry failed: %w", err))
				break
			}
			if n < expireBatch {
				break
			}
		}
	}
	ended, gifts := 0, 0
	if w.sweeper != nil {
		var err error
		ended, gifts, err = w.sweeper.Sweep(ctx, now())
		if err != nil {
			errs = append(errs, fmt.Errorf("campaign sweep failed: %w", err))
		}
	}
	if total > 0 || ended > 0 || gifts > 0 {
		log.Printf("credit worker: expiry done lots=%d campaignsEnded=%d giftsRetried=%d", total, ended, gifts)
	}
	return errors.Join(errs...)
}
