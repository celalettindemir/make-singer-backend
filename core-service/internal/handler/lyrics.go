package handler

import (
	"errors"
	"log"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/pkg/response"
)

// actionIDHeader carries the client-generated id of one user action; every
// request of the action (first call and follow-ups) sends the same id.
const actionIDHeader = "X-Action-Id"

type LyricsHandler struct {
	service   service.LyricsGenerator
	validator *validator.Validate
	credits   credit.Spender
}

// WithCredits turns on per-action credit charging. Without it the handler
// charges nothing and reports balance 0.
func (h *LyricsHandler) WithCredits(s credit.Spender) *LyricsHandler {
	h.credits = s
	return h
}

func NewLyricsHandler(svc service.LyricsGenerator, v *validator.Validate) *LyricsHandler {
	return &LyricsHandler{
		service:   svc,
		validator: v,
	}
}

// Generate handles POST /api/lyrics/generate
// @Summary      Generate lyrics
// @Description  Generate lyrics for a song section using AI
// @Tags         Lyrics
// @Accept       json
// @Produce      json
// @Param        request body model.LyricsGenerateRequest true "Generate request"
// @Success      200 {object} model.LyricsGenerateResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      502 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/lyrics/generate [post]
func (h *LyricsHandler) Generate(c *fiber.Ctx) error {
	var req model.LyricsGenerateRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}

	act, err := h.begin(c, credit.OpLyricsGenerate)
	if act == nil {
		return err
	}

	var result *model.LyricsGenerateResponse
	result, err = h.service.Generate(c.Context(), &req)
	if err != nil {
		act.refund(c)
		return aiError(c, err)
	}
	if !hasLyricLines(result.Drafts...) {
		act.refund(c)
		return aiError(c, errEmptyLyrics)
	}
	result.Balance = act.balance

	return response.OK(c, result)
}

// Rewrite handles POST /api/lyrics/rewrite
// @Summary      Rewrite lyrics
// @Description  Rewrite existing lyrics with AI-powered suggestions
// @Tags         Lyrics
// @Accept       json
// @Produce      json
// @Param        request body model.LyricsRewriteRequest true "Rewrite request"
// @Success      200 {object} model.LyricsRewriteResponse
// @Failure      400 {object} response.ErrorResponse
// @Failure      401 {object} response.ErrorResponse
// @Failure      429 {object} response.ErrorResponse
// @Failure      502 {object} response.ErrorResponse
// @Security     BearerAuth
// @Router       /api/lyrics/rewrite [post]
func (h *LyricsHandler) Rewrite(c *fiber.Ctx) error {
	var req model.LyricsRewriteRequest
	if err := c.BodyParser(&req); err != nil {
		return response.ValidationError(c, i18n.KeyInvalidBody, nil)
	}

	if err := h.validator.Struct(&req); err != nil {
		return response.ValidationError(c, i18n.KeyValidationFailed, formatValidationErrors(err))
	}

	act, err := h.begin(c, credit.OpLyricsRewrite)
	if act == nil {
		return err
	}

	var result *model.LyricsRewriteResponse
	result, err = h.service.Rewrite(c.Context(), &req)
	if err != nil {
		act.refund(c)
		return aiError(c, err)
	}
	if !hasLyricLines(result.Lines) {
		act.refund(c)
		return aiError(c, errEmptyLyrics)
	}
	result.Balance = act.balance

	return response.OK(c, result)
}

var errEmptyLyrics = errors.New("AI returned no lyric lines")

// hasLyricLines reports whether any of the line groups holds a non-blank line.
func hasLyricLines(groups ...[]string) bool {
	for _, g := range groups {
		for _, l := range g {
			if strings.TrimSpace(l) != "" {
				return true
			}
		}
	}
	return false
}

// lyricsAction is the credit state of one lyrics request.
type lyricsAction struct {
	h       *LyricsHandler
	userID  string
	op      credit.Operation
	id      string
	balance int
	charged bool
}

// begin applies the credit rules before the AI runs. A nil action means the
// response is already written; the returned error is the handler's result. Mock mode and shadow mode (credits not enforced) charge
// nothing and only report the balance.
func (h *LyricsHandler) begin(c *fiber.Ctx, op credit.Operation) (*lyricsAction, error) {
	act := &lyricsAction{h: h, op: op}
	if h.credits == nil {
		return act, nil
	}
	userID, ok := requestOwner(c)
	if !ok {
		return nil, response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	act.userID = userID

	if h.service.IsMock() || !h.credits.Enforced() {
		bal, err := h.credits.Balance(c.Context(), userID)
		if err != nil {
			return nil, internalError(c, err)
		}
		act.balance = bal
		return act, nil
	}

	id, err := uuid.Parse(c.Get(actionIDHeader))
	if err != nil {
		return nil, response.ValidationError(c, i18n.KeyActionIDRequired, map[string]string{actionIDHeader: "required"})
	}
	act.id = id.String()

	bal, charged, err := h.credits.SpendAction(c.Context(), userID, op, act.id)
	if err != nil {
		var insufficient *credit.InsufficientError
		switch {
		case errors.As(err, &insufficient):
			return nil, response.InsufficientCredits(c, insufficient.Required, insufficient.Balance)
		case errors.Is(err, credit.ErrActionExpired):
			return nil, response.ValidationError(c, i18n.KeyActionExpired, map[string]string{actionIDHeader: "action_expired"})
		default:
			return nil, internalError(c, err)
		}
	}
	act.balance, act.charged = bal, charged
	return act, nil
}

// refund returns the charge when this request made it; a follow-up request of
// the same action never refunds. A failed refund is logged, since the user
// already gets the AI error.
func (a *lyricsAction) refund(c *fiber.Ctx) {
	if !a.charged {
		return
	}
	if err := a.h.credits.RefundAction(c.Context(), a.userID, a.op, a.id); err != nil {
		log.Printf("%s %s: refund action %s: %v", c.Method(), c.Route().Path, a.id, err)
	}
}

// formatValidationErrors formats validator errors for response
func formatValidationErrors(err error) interface{} {
	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		errors := make(map[string]string)
		for _, e := range validationErrors {
			errors[e.Field()] = e.Tag()
		}
		return errors
	}
	return nil
}
