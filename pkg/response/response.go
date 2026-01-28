package response

import "github.com/gofiber/fiber/v2"

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

func Error(c *fiber.Ctx, status int, code, message string, details interface{}) error {
	return c.Status(status).JSON(ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

func ValidationError(c *fiber.Ctx, message string, details interface{}) error {
	return Error(c, fiber.StatusBadRequest, CodeValidationError, message, details)
}

func Unauthorized(c *fiber.Ctx, message string) error {
	return Error(c, fiber.StatusUnauthorized, CodeUnauthorized, message, nil)
}

func Forbidden(c *fiber.Ctx, message string) error {
	return Error(c, fiber.StatusForbidden, CodeForbidden, message, nil)
}

func NotFound(c *fiber.Ctx, message string) error {
	return Error(c, fiber.StatusNotFound, CodeNotFound, message, nil)
}

func RateLimited(c *fiber.Ctx) error {
	return Error(c, fiber.StatusTooManyRequests, CodeRateLimited, "Rate limit exceeded", nil)
}

func ServiceError(c *fiber.Ctx, message string) error {
	return Error(c, fiber.StatusInternalServerError, CodeServiceError, message, nil)
}

func AIError(c *fiber.Ctx, message string) error {
	return Error(c, fiber.StatusBadGateway, CodeAIError, message, nil)
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
