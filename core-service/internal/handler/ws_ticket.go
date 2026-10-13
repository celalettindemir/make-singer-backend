package handler

import (
	"errors"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

type WSTicketHandler struct {
	service   *service.WSTicketService
	validator *validator.Validate
}

func NewWSTicketHandler(svc *service.WSTicketService, v *validator.Validate) *WSTicketHandler {
	return &WSTicketHandler{service: svc, validator: v}
}

// Issue handles POST /api/ws/ticket
// @Summary      Get WebSocket ticket
// @Description  Issue a single-use, 30-second ticket for GET /ws/jobs/{jobId}?ticket=...
// @Tags         WebSocket
// @Accept       json
// @Produce      json
// @Param        request body model.WSTicketRequest true "Ticket request"
// @Success      200 {object} model.WSTicketResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      404 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/ws/ticket [post]
func (h *WSTicketHandler) Issue(c *fiber.Ctx) error {
	userID, ok := istekSahibi(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var req model.WSTicketRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, "Invalid request body", nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, "Validation failed", formatValidationErrors(err))
	}

	result, err := h.service.Issue(c.Context(), userID, req.JobID)
	if err != nil {
		if errors.Is(err, service.ErrJobNotFound) {
			return response.NotFound(c, "Job not found")
		}
		return response.ServiceError(c, "Bilet uretilemedi")
	}
	return response.OK(c, result)
}

// Gate, WebSocket upgrade'inden ONCE bileti tuketir. Upgrade sonrasi
// reddetmek, HTTP durum kodu yerine kapanis cercevesi demek olurdu.
func (h *WSTicketHandler) Gate(c *fiber.Ctx) error {
	err := h.service.Redeem(c.Context(), c.Query("ticket"), c.Params("jobId"))
	if errors.Is(err, service.ErrInvalidTicket) {
		return response.Unauthorized(c, "Gecersiz WebSocket bileti")
	}
	if err != nil {
		return response.ServiceError(c, "Bilet denetlenemedi")
	}
	return c.Next()
}
