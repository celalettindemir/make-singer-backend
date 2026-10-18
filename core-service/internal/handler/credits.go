package handler

import (
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// CreditsHandler serves /api/credits.
type CreditsHandler struct {
	svc *credit.Service
}

// NewCreditsHandler builds the handler over svc.
func NewCreditsHandler(svc *credit.Service) *CreditsHandler {
	return &CreditsHandler{svc: svc}
}

// Get handles GET /api/credits.
func (h *CreditsHandler) Get(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	summary, err := h.svc.Summary(c.Context(), userID)
	if err != nil {
		return creditsFailure(c, "summary", err)
	}
	return response.OK(c, summary)
}

// History handles GET /api/credits/history.
func (h *CreditsHandler) History(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	page, err := h.svc.History(c.Context(), userID, c.Query("cursor"), c.Query("kind"))
	if errors.Is(err, credit.ErrBadCursor) {
		return response.ValidationError(c, i18n.KeyCursorInvalid, map[string]string{"cursor": "invalid"})
	}
	if errors.Is(err, credit.ErrBadFilter) {
		return response.ValidationError(c, i18n.KeyValidationFailed, map[string]string{"kind": "invalid"})
	}
	if err != nil {
		return creditsFailure(c, "history", err)
	}
	return response.OK(c, page)
}

func creditsFailure(c *fiber.Ctx, op string, err error) error {
	log.Printf("credits: %s failed: %v", op, err)
	return response.ServiceError(c, i18n.KeyInternal)
}
