package handler

import (
	"errors"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
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
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req model.WSTicketRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}

	result, err := h.service.Issue(c.Context(), userID, req.JobID)
	if err != nil {
		if errors.Is(err, service.ErrJobNotFound) {
			return response.NotFound(c, i18n.KeyJobNotFound)
		}
		return response.ServiceError(c, i18n.KeyTicketIssueFailed)
	}
	return response.OK(c, result)
}

// Gate consumes the ticket BEFORE the WebSocket upgrade. Rejecting after the
// upgrade would mean a close frame instead of an HTTP status code.
func (h *WSTicketHandler) Gate(c *fiber.Ctx) error {
	err := h.service.Redeem(c.Context(), c.Query("ticket"), c.Params("jobId"))
	if errors.Is(err, service.ErrInvalidTicket) {
		return response.Unauthorized(c, i18n.KeyTicketInvalid)
	}
	if err != nil {
		return response.ServiceError(c, i18n.KeyTicketCheckFailed)
	}
	return c.Next()
}

// IssueUser handles POST /api/ws/ticket/me
// @Summary      Get WebSocket ticket for the user job stream
// @Description  Issue a single-use, 30-second ticket for GET /ws/me?ticket=...
// @Tags         WebSocket
// @Produce      json
// @Success      200 {object} model.WSTicketResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      500 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/ws/ticket/me [post]
func (h *WSTicketHandler) IssueUser(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	result, err := h.service.IssueUser(c.Context(), userID)
	if err != nil {
		return response.ServiceError(c, i18n.KeyTicketIssueFailed)
	}
	return response.OK(c, result)
}

// GateUser consumes a user ticket BEFORE the upgrade and stores the user id in
// c.Locals("userId") for the /ws/me handler.
func (h *WSTicketHandler) GateUser(c *fiber.Ctx) error {
	userID, err := h.service.RedeemUser(c.Context(), c.Query("ticket"))
	if errors.Is(err, service.ErrInvalidTicket) {
		return response.Unauthorized(c, i18n.KeyTicketInvalid)
	}
	if err != nil {
		return response.ServiceError(c, i18n.KeyTicketCheckFailed)
	}
	c.Locals("userId", userID)
	return c.Next()
}
