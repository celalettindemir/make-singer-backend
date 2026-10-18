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

	CodeInsufficientCredits  = "INSUFFICIENT_CREDITS"
	CodePurchaseInvalid      = "PURCHASE_INVALID"
	CodePurchaseOwnedByOther = "PURCHASE_OWNED_BY_OTHER"
	CodePlanRequired         = "PLAN_REQUIRED"
	CodeAdminRequired        = "ADMIN_REQUIRED"
	CodeCampaignNotActive    = "CAMPAIGN_NOT_ACTIVE"
	CodeBalanceTooLow        = "BALANCE_TOO_LOW"
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

// InsufficientCredits writes the 402 answer with the price, the balance and the open debt.
func InsufficientCredits(c *fiber.Ctx, required, balance, debt int) error {
	return Error(c, fiber.StatusPaymentRequired, CodeInsufficientCredits, i18n.KeyInsufficientCredits,
		map[string]int{"required": required, "balance": balance, "debt": debt})
}

// AdminRequired writes the 403 answer for a signed-in user who is not an admin.
func AdminRequired(c *fiber.Ctx) error {
	return Error(c, fiber.StatusForbidden, CodeAdminRequired, i18n.KeyAdminRequired, nil)
}

// PlanRequired writes the 403 answer for a feature of a higher plan tier.
func PlanRequired(c *fiber.Ctx, tier string) error {
	return Error(c, fiber.StatusForbidden, CodePlanRequired, i18n.KeyPlanRequired,
		map[string]string{"requiredTier": tier})
}

// CampaignNotActive writes the 409 answer for stopping a campaign that is
// not active.
func CampaignNotActive(c *fiber.Ctx) error {
	return Error(c, fiber.StatusConflict, CodeCampaignNotActive, i18n.KeyCampaignNotActive, nil)
}

// BalanceTooLow writes the 409 answer for an admin deduction larger than the
// user's balance.
func BalanceTooLow(c *fiber.Ctx, balance int) error {
	return Error(c, fiber.StatusConflict, CodeBalanceTooLow, i18n.KeyBalanceTooLow,
		map[string]int{"balance": balance})
}
