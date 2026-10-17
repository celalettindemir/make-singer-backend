package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/googleplay"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

const (
	webhookTimeout     = 5 * time.Second
	webhookMaxRetry    = 10
	webhookStoreApple  = "appstore"
	webhookStoreGoogle = "googleplay"
)

// NotificationVerifier verifies a signed App Store notification
// (appstore.Verifier).
type NotificationVerifier interface {
	Notification(signedPayload string) (*appstore.Notification, error)
}

// PushAuth verifies the Authorization header of a Pub/Sub push
// (googleplay.PushVerifier).
type PushAuth interface {
	Verify(authorizationHeader string) error
}

// Enqueuer enqueues asynq tasks.
type Enqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// WebhooksHandler serves the store webhooks.
type WebhooksHandler struct {
	p      PurchaseProcessor
	apple  NotificationVerifier
	google PushAuth
	q      Enqueuer
}

// NewWebhooksHandler builds the handler. A nil apple or google verifier
// answers the store with 503 (the integration is not configured).
func NewWebhooksHandler(p PurchaseProcessor, apple NotificationVerifier, google PushAuth, q Enqueuer) *WebhooksHandler {
	return &WebhooksHandler{p: p, apple: apple, google: google, q: q}
}

// AppStore handles POST /api/webhooks/appstore.
func (h *WebhooksHandler) AppStore(c *fiber.Ctx) error {
	if h.apple == nil {
		return response.Error(c, fiber.StatusServiceUnavailable, response.CodeServiceError, i18n.KeyStoreUnavailable, nil)
	}
	var req struct {
		SignedPayload string `json:"signedPayload"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil || req.SignedPayload == "" {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	n, err := h.apple.Notification(req.SignedPayload)
	if err != nil {
		log.Printf("webhooks: appstore notification rejected: %v", err)
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), webhookTimeout)
	defer cancel()
	err = h.p.ApplyAppleNotification(ctx, n)
	if errors.Is(err, credit.ErrPurchaseInvalid) {
		// An unverifiable inner payload does not get better on retry.
		log.Printf("webhooks: appstore notification %s dropped: %v", n.UUID, err)
		return c.SendStatus(fiber.StatusOK)
	}
	return h.finish(c, webhookStoreApple, err)
}

// GooglePlay handles POST /api/webhooks/googleplay.
func (h *WebhooksHandler) GooglePlay(c *fiber.Ctx) error {
	if h.google == nil {
		return response.Error(c, fiber.StatusServiceUnavailable, response.CodeServiceError, i18n.KeyStoreUnavailable, nil)
	}
	if err := h.google.Verify(c.Get(fiber.HeaderAuthorization)); err != nil {
		log.Printf("webhooks: googleplay push rejected: %v", err)
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	n, err := googleplay.DecodePush(c.Body())
	if err != nil {
		log.Printf("webhooks: googleplay push undecodable: %v", err)
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if n.Test {
		return c.SendStatus(fiber.StatusOK)
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), webhookTimeout)
	defer cancel()
	return h.finish(c, webhookStoreGoogle, h.p.ApplyGoogleNotification(ctx, n))
}

// finish answers 200 for a processed webhook. A processing error is handed to
// the credit queue and still answers 200; only a failed enqueue answers 500,
// so that the store redelivers.
func (h *WebhooksHandler) finish(c *fiber.Ctx, store string, procErr error) error {
	if procErr == nil {
		return c.SendStatus(fiber.StatusOK)
	}
	log.Printf("webhooks: %s processing failed, queueing: %v", store, procErr)
	payload, err := json.Marshal(map[string]string{
		"store": store,
		"body":  base64.StdEncoding.EncodeToString(c.Body()),
	})
	if err != nil {
		return internalError(c, err)
	}
	task := asynq.NewTask(credit.TaskTypeNotification, payload)
	if _, err := h.q.Enqueue(task, asynq.Queue(credit.QueueCredit), asynq.MaxRetry(webhookMaxRetry)); err != nil {
		return internalError(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}
