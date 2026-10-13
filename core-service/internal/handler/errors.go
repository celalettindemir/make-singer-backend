package handler

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// internalError logs an unexpected service error and answers with the
// generic localized 500 SERVICE_ERROR. The error text stays in the log:
// it can carry internal detail and cannot be localized.
func internalError(c *fiber.Ctx, err error) error {
	log.Printf("%s %s: %v", c.Method(), c.Route().Path, err)
	return response.ServiceError(c, i18n.KeyInternal)
}

// aiError is internalError for the AI provider: 502 AI_ERROR.
func aiError(c *fiber.Ctx, err error) error {
	log.Printf("%s %s: %v", c.Method(), c.Route().Path, err)
	return response.AIError(c, i18n.KeyAIUnavailable)
}
