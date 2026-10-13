package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// Report handles POST /api/social/sikayetler. 201 with no body; a repeat
// report is also 201 (no new record is opened).
func (h *SocialHandler) Report(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, "Kimlik dogrulanamadi")
	}
	var input social.ReportInput
	if err := c.BodyParser(&input); err != nil {
		return badBodyError(c)
	}
	if err := h.service.Report(c.Context(), userID, input); err != nil {
		return socialError(c, err)
	}
	// SendStatus would write "Created" into the body; the body is left empty on purpose.
	c.Status(fiber.StatusCreated)
	return nil
}
