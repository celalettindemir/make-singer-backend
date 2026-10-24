package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

// JobsHandler serves the jobs of every type: list, detail, events, cancel and
// retry.
type JobsHandler struct {
	center    *service.JobCenter
	validator *validator.Validate
}

func NewJobsHandler(center *service.JobCenter, v *validator.Validate) *JobsHandler {
	return &JobsHandler{center: center, validator: v}
}

// jobID reads the :id path parameter; a value that is not a UUID is 400.
func jobID(c *fiber.Ctx) (string, bool) {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

// jobError maps a job-center error to its answer.
func jobError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, service.ErrJobNotFound):
		return response.NotFound(c, i18n.KeyJobNotFound)
	case errors.Is(err, service.ErrJobAlreadyCompleted):
		return response.ValidationError(c, i18n.KeyJobAlreadyCompleted, nil)
	case errors.Is(err, service.ErrNotRetryable):
		return response.JobNotRetryable(c)
	}
	return internalError(c, err)
}

// List handles GET /api/jobs.
func (h *JobsHandler) List(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	since := time.Now().Add(-service.DefaultJobListWindow)
	if raw := c.Query("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return response.ValidationError(c, i18n.KeyValidationFailed, nil)
		}
		since = t
	}
	list, err := h.center.List(c.Context(), userID, since)
	if err != nil {
		return internalError(c, err)
	}
	return response.OK(c, fiber.Map{"jobs": list})
}

// Detail handles GET /api/jobs/:id.
func (h *JobsHandler) Detail(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	id, ok := jobID(c)
	if !ok {
		return response.ValidationError(c, i18n.KeyValidationFailed, nil)
	}
	d, err := h.center.Detail(c.Context(), userID, id)
	if err != nil {
		return jobError(c, err)
	}
	return response.OK(c, d)
}

// Events handles GET /api/jobs/:id/events.
func (h *JobsHandler) Events(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	id, ok := jobID(c)
	if !ok {
		return response.ValidationError(c, i18n.KeyValidationFailed, nil)
	}
	var after int64
	if raw := c.Query("afterSeq"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return response.ValidationError(c, i18n.KeyValidationFailed, nil)
		}
		after = n
	}
	events, err := h.center.Events(c.Context(), userID, id, after)
	if err != nil {
		return jobError(c, err)
	}
	out := make([]model.JobEventView, 0, len(events))
	for _, e := range events {
		data := e.Data
		if len(data) == 0 {
			data = json.RawMessage("{}")
		}
		out = append(out, model.JobEventView{JobID: e.JobID, Seq: e.Seq, Kind: e.Kind, Data: data, At: e.At})
	}
	return response.OK(c, fiber.Map{"events": out})
}

// Cancel handles POST /api/jobs/:id/cancel and answers the job as it is now.
func (h *JobsHandler) Cancel(c *fiber.Ctx) error {
	return h.act(c, h.center.Cancel)
}

// Retry handles POST /api/jobs/:id/retry and answers the job as it is now.
func (h *JobsHandler) Retry(c *fiber.Ctx) error {
	return h.act(c, h.center.Retry)
}

func (h *JobsHandler) act(c *fiber.Ctx, do func(ctx context.Context, userID, id string) error) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	id, ok := jobID(c)
	if !ok {
		return response.ValidationError(c, i18n.KeyValidationFailed, nil)
	}
	if err := do(c.Context(), userID, id); err != nil {
		return jobError(c, err)
	}
	v, err := h.center.View(c.Context(), userID, id)
	if err != nil {
		return jobError(c, err)
	}
	return response.OK(c, v)
}
