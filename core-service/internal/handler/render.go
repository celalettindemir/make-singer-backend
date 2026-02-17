package handler

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type RenderHandler struct {
	service   *service.RenderService
	validator *validator.Validate
}

func NewRenderHandler(svc *service.RenderService, v *validator.Validate) *RenderHandler {
	return &RenderHandler{
		service:   svc,
		validator: v,
	}
}

// Start handles POST /api/render/start
// @Summary      Start render job
// @Description  Start an asynchronous render job for a song arrangement
// @Tags         Render
// @Accept       json
// @Produce      json
// @Param        request body model.RenderStartRequest true "Render start request"
// @Success      202 {object} model.RenderStartResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/render/start [post]
func (h *RenderHandler) Start(c *fiber.Ctx) error {
	var req model.RenderStartRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.StartRender(c.Context(), &req)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.Accepted(c, result)
}

// Status handles GET /api/render/status/:jobId
// @Summary      Get render job status
// @Description  Get the current status and progress of a render job
// @Tags         Render
// @Produce      json
// @Param        jobId path string true "Job ID"
// @Success      200 {object} model.RenderStatusResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/render/status/{jobId} [get]
func (h *RenderHandler) Status(c *fiber.Ctx) error {
	jobID := c.Params("jobId")
	if jobID == "" {
		return response.ValidationError(c, "Job ID is required", nil)
	}

	result, err := h.service.GetStatus(c.Context(), jobID)
	if err != nil {
		if err.Error() == "job not found" {
			return response.NotFound(c, "Job not found")
		}
		return response.ServiceError(c, err.Error())
	}

	return response.OK(c, result)
}

// Result handles GET /api/render/result/:jobId
// @Summary      Get render job result
// @Description  Get the result of a completed render job including stems
// @Tags         Render
// @Produce      json
// @Param        jobId path string true "Job ID"
// @Success      200 {object} model.RenderResultResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/render/result/{jobId} [get]
func (h *RenderHandler) Result(c *fiber.Ctx) error {
	jobID := c.Params("jobId")
	if jobID == "" {
		return response.ValidationError(c, "Job ID is required", nil)
	}

	result, err := h.service.GetResult(c.Context(), jobID)
	if err != nil {
		if err.Error() == "job not found" {
			return response.NotFound(c, "Job not found")
		}
		if err.Error() == "job not completed" {
			return response.ValidationError(c, "Job not completed yet", nil)
		}
		return response.ServiceError(c, err.Error())
	}

	return response.OK(c, result)
}

// Cancel handles POST /api/render/cancel/:jobId
// @Summary      Cancel render job
// @Description  Cancel a running or queued render job
// @Tags         Render
// @Produce      json
// @Param        jobId path string true "Job ID"
// @Success      200 {object} model.RenderCancelResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/render/cancel/{jobId} [post]
func (h *RenderHandler) Cancel(c *fiber.Ctx) error {
	jobID := c.Params("jobId")
	if jobID == "" {
		return response.ValidationError(c, "Job ID is required", nil)
	}

	result, err := h.service.CancelRender(c.Context(), jobID)
	if err != nil {
		if err.Error() == "job not found" {
			return response.NotFound(c, "Job not found")
		}
		if err.Error() == "job already completed" {
			return response.ValidationError(c, "Job already completed", nil)
		}
		return response.ServiceError(c, err.Error())
	}

	return response.OK(c, result)
}
