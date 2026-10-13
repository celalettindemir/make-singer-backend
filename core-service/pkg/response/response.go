package response

import (
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
)

// Error codes
const (
	CodeValidationError = "VALIDATION_ERROR"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeNotFound        = "NOT_FOUND"
	CodeRateLimited     = "RATE_LIMITED"
	CodeJobFailed       = "JOB_FAILED"
	CodeServiceError    = "SERVICE_ERROR"
	CodeAIError         = "AI_ERROR"
)

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// langKey is the c.Locals key holding the request's i18n.Lang.
type langKey struct{}

// Lang returns the message language of the request, parsed once from
// Accept-Language and cached in c.Locals.
func Lang(c *fiber.Ctx) i18n.Lang {
	if l, ok := c.Locals(langKey{}).(i18n.Lang); ok {
		return l
	}
	l := i18n.FromAcceptLanguage(c.Get(fiber.HeaderAcceptLanguage))
	c.Locals(langKey{}, l)
	return l
}

// Error writes {"error":{"code","message","details"}}. code is never
// localized; message is msg rendered in the request's language (args
// fill its verbs). Content-Language names that language and
// Vary: Accept-Language keeps caches from mixing languages.
func Error(c *fiber.Ctx, status int, code string, msg i18n.Key, details interface{}, args ...any) error {
	lang := Lang(c)
	c.Set(fiber.HeaderContentLanguage, string(lang))
	c.Vary(fiber.HeaderAcceptLanguage)
	return c.Status(status).JSON(ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: i18n.Message(lang, msg, args...),
			Details: details,
		},
	})
}

func ValidationError(c *fiber.Ctx, msg i18n.Key, details interface{}) error {
	return Error(c, fiber.StatusBadRequest, CodeValidationError, msg, details)
}

func Unauthorized(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusUnauthorized, CodeUnauthorized, msg, nil)
}

func Forbidden(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusForbidden, CodeForbidden, msg, nil)
}

func NotFound(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusNotFound, CodeNotFound, msg, nil)
}

func RateLimited(c *fiber.Ctx) error {
	return Error(c, fiber.StatusTooManyRequests, CodeRateLimited, i18n.KeyRateLimited, nil)
}

func ServiceError(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusInternalServerError, CodeServiceError, msg, nil)
}

func AIError(c *fiber.Ctx, msg i18n.Key) error {
	return Error(c, fiber.StatusBadGateway, CodeAIError, msg, nil)
}

func OK(c *fiber.Ctx, data interface{}) error {
	return c.JSON(data)
}

func Created(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusCreated).JSON(data)
}

func Accepted(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusAccepted).JSON(data)
}

func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}
