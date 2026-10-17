package handler

import (
	"context"
	"errors"
	"log"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/googleplay"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/pkg/response"
)

// PurchaseProcessor is the part of *credit.Purchases the HTTP layer uses.
type PurchaseProcessor interface {
	VerifyApple(ctx context.Context, userID, signedTransaction string) (credit.PurchaseResult, error)
	VerifyGoogle(ctx context.Context, userID, productID, purchaseToken string) (credit.PurchaseResult, error)
	ApplyAppleNotification(ctx context.Context, n *appstore.Notification) error
	ApplyGoogleNotification(ctx context.Context, n *googleplay.DeveloperNotification) error
}

// PurchasesHandler serves POST /api/purchases/{apple,google}.
type PurchasesHandler struct {
	p         PurchaseProcessor
	validator *validator.Validate
}

// NewPurchasesHandler builds the handler over p.
func NewPurchasesHandler(p PurchaseProcessor, v *validator.Validate) *PurchasesHandler {
	return &PurchasesHandler{p: p, validator: v}
}

type applePurchaseRequest struct {
	SignedTransaction string `json:"signedTransaction" validate:"required"`
}

type googlePurchaseRequest struct {
	ProductID     string `json:"productId" validate:"required"`
	PurchaseToken string `json:"purchaseToken" validate:"required"`
}

// Apple handles POST /api/purchases/apple.
func (h *PurchasesHandler) Apple(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req applePurchaseRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}
	res, err := h.p.VerifyApple(c.UserContext(), userID, req.SignedTransaction)
	return purchaseAnswer(c, res, err)
}

// Google handles POST /api/purchases/google.
func (h *PurchasesHandler) Google(c *fiber.Ctx) error {
	userID, ok := requestOwner(c)
	if !ok {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req googlePurchaseRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}
	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}
	res, err := h.p.VerifyGoogle(c.UserContext(), userID, req.ProductID, req.PurchaseToken)
	return purchaseAnswer(c, res, err)
}

func purchaseAnswer(c *fiber.Ctx, res credit.PurchaseResult, err error) error {
	switch {
	case err == nil:
		return response.OK(c, res)
	case errors.Is(err, credit.ErrPurchaseInvalid):
		log.Printf("purchases: invalid purchase: %v", err)
		return response.Error(c, fiber.StatusUnprocessableEntity, response.CodePurchaseInvalid, i18n.KeyPurchaseInvalid, nil)
	case errors.Is(err, credit.ErrOwnedByOther):
		return response.Error(c, fiber.StatusConflict, response.CodePurchaseOwnedByOther, i18n.KeyPurchaseOwnedByOther, nil)
	case errors.Is(err, credit.ErrStoreUnavailable), errors.Is(err, credit.ErrStoreTransient):
		return response.Error(c, fiber.StatusServiceUnavailable, response.CodeServiceError, i18n.KeyStoreUnavailable, nil)
	default:
		return internalError(c, err)
	}
}
