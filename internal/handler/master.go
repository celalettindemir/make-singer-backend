package handler

import (
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type MasterHandler struct {
	service   *service.MasterService
	validator *validator.Validate
}

func NewMasterHandler(svc *service.MasterService, v *validator.Validate) *MasterHandler {
	return &MasterHandler{
		service:   svc,
		validator: v,
	}
}

// Preview handles POST /api/master/preview
func (h *MasterHandler) Preview(c *fiber.Ctx) error {
	var req model.MasterPreviewRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.Preview(c.Context(), &req)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.OK(c, result)
}

// Final handles POST /api/master/final
func (h *MasterHandler) Final(c *fiber.Ctx) error {
	var req model.MasterFinalRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.StartFinal(c.Context(), &req)
	if err != nil {
		return response.ServiceError(c, err.Error())
	}

	return response.Accepted(c, result)
}

// Status handles GET /api/master/status/:jobId
func (h *MasterHandler) Status(c *fiber.Ctx) error {
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

// Result handles GET /api/master/result/:jobId
func (h *MasterHandler) Result(c *fiber.Ctx) error {
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
