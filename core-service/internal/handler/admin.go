package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/pkg/response"
)

// AdminCampaigns is the campaign service the admin API uses
// (*credit.Campaigns).
type AdminCampaigns interface {
	Create(ctx context.Context, in credit.NewCampaign, by string) (credit.Campaign, *credit.Campaign, error)
	Stop(ctx context.Context, id int64) (credit.Campaign, error)
	List(ctx context.Context) (credit.CampaignOverview, error)
	Get(ctx context.Context, id int64) (credit.CampaignDetail, error)
}

// AdminCredits is the user and movement service the admin API uses
// (*credit.Admin).
type AdminCredits interface {
	Users(ctx context.Context, q, cursor string) (credit.AdminUserPage, error)
	User(ctx context.Context, userID string) (credit.AdminUserDetail, error)
	Adjust(ctx context.Context, userID string, in credit.Adjustment, by string) (credit.AdjustResult, error)
	Movements(ctx context.Context, q credit.MovementQuery) (credit.AdminMovementPage, error)
}

// AdminHandler serves the campaign, user and movement routes under
// /api/admin. The chain in front of it (admin token, rate limit, gate) is
// bound in main.
type AdminHandler struct {
	campaigns AdminCampaigns
	credits   AdminCredits
}

// NewAdminHandler builds the handler.
func NewAdminHandler(campaigns AdminCampaigns, credits AdminCredits) *AdminHandler {
	return &AdminHandler{campaigns: campaigns, credits: credits}
}

// Routes binds the admin routes on r (the /api/admin group).
func (h *AdminHandler) Routes(r fiber.Router) {
	r.Get("/campaigns", h.ListCampaigns)
	r.Post("/campaigns", h.CreateCampaign)
	r.Get("/campaigns/:id", h.GetCampaign)
	r.Post("/campaigns/:id/stop", h.StopCampaign)
	r.Get("/users", h.ListUsers)
	r.Get("/users/:id", h.GetUser)
	r.Post("/users/:id/adjust", h.Adjust)
	r.Get("/movements", h.ListMovements)
}

func adminFailure(c *fiber.Ctx, op string, err error) error {
	log.Printf("admin: %s failed: %v", op, err)
	return response.ServiceError(c, i18n.KeyInternal)
}

func invalidField(c *fiber.Ctx, field string) error {
	return response.ValidationError(c, i18n.KeyValidationFailed, map[string]string{field: "invalid"})
}

// fieldOf names the field of a validation error.
func fieldOf(err error) string {
	var fe *credit.FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return "body"
}

// nullable is a JSON field that must be present but may be null.
type nullable[T any] struct {
	Present bool
	Value   *T
}

// UnmarshalJSON records that the key was present.
func (n *nullable[T]) UnmarshalJSON(b []byte) error {
	n.Present = true
	if string(bytes.TrimSpace(b)) == "null" {
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// decodeBody reads the request body as exactly one JSON object without
// unknown keys.
func decodeBody(c *fiber.Ctx, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return false
	}
	return !dec.More()
}

// ListCampaigns handles GET /api/admin/campaigns.
func (h *AdminHandler) ListCampaigns(c *fiber.Ctx) error {
	o, err := h.campaigns.List(c.UserContext())
	if err != nil {
		return adminFailure(c, "list campaigns", err)
	}
	return response.OK(c, o.View())
}

type createCampaignRequest struct {
	Name      *string          `json:"name"`
	Quota     *int             `json:"quota"`
	PerUser   *int             `json:"perUser"`
	ValidDays *int             `json:"validDays"`
	EndsAt    nullable[string] `json:"endsAt"`
}

// CreateCampaign handles POST /api/admin/campaigns.
func (h *AdminHandler) CreateCampaign(c *fiber.Ctx) error {
	by := middleware.GetAdminEmail(c)
	if by == "" {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req createCampaignRequest
	if !decodeBody(c, &req) {
		return invalidField(c, "body")
	}
	switch {
	case req.Name == nil:
		return invalidField(c, "name")
	case req.Quota == nil:
		return invalidField(c, "quota")
	case req.PerUser == nil:
		return invalidField(c, "perUser")
	case req.ValidDays == nil:
		return invalidField(c, "validDays")
	case !req.EndsAt.Present:
		return invalidField(c, "endsAt")
	}
	in := credit.NewCampaign{Name: *req.Name, Quota: *req.Quota, PerUser: *req.PerUser, ValidDays: *req.ValidDays}
	if req.EndsAt.Value != nil {
		day, err := credit.ParseDay(*req.EndsAt.Value)
		if err != nil {
			return invalidField(c, "endsAt")
		}
		in.EndsAt = &day
	}
	created, replaced, err := h.campaigns.Create(c.UserContext(), in, by)
	if errors.Is(err, credit.ErrCampaignInvalid) {
		return invalidField(c, fieldOf(err))
	}
	if err != nil {
		return adminFailure(c, "create campaign", err)
	}
	out := credit.CreatedCampaignView{Campaign: created.View()}
	if replaced != nil {
		r := replaced.View()
		out.Replaced = &r
	}
	return response.Created(c, out)
}

// GetCampaign handles GET /api/admin/campaigns/:id.
func (h *AdminHandler) GetCampaign(c *fiber.Ctx) error {
	id, ok := credit.ParseCampaignUUID(c.Params("id"))
	if !ok {
		return response.NotFound(c, i18n.KeyAdminNotFound)
	}
	d, err := h.campaigns.Get(c.UserContext(), id)
	if errors.Is(err, credit.ErrCampaignNotFound) {
		return response.NotFound(c, i18n.KeyAdminNotFound)
	}
	if err != nil {
		return adminFailure(c, "get campaign", err)
	}
	return response.OK(c, d.View())
}

// StopCampaign handles POST /api/admin/campaigns/:id/stop.
func (h *AdminHandler) StopCampaign(c *fiber.Ctx) error {
	id, ok := credit.ParseCampaignUUID(c.Params("id"))
	if !ok {
		return response.NotFound(c, i18n.KeyAdminNotFound)
	}
	stopped, err := h.campaigns.Stop(c.UserContext(), id)
	switch {
	case errors.Is(err, credit.ErrCampaignNotFound):
		return response.NotFound(c, i18n.KeyAdminNotFound)
	case errors.Is(err, credit.ErrCampaignNotActive):
		return response.CampaignNotActive(c)
	case err != nil:
		return adminFailure(c, "stop campaign", err)
	}
	return response.OK(c, stopped.View())
}

// ListUsers handles GET /api/admin/users.
func (h *AdminHandler) ListUsers(c *fiber.Ctx) error {
	page, err := h.credits.Users(c.UserContext(), c.Query("q"), c.Query("cursor"))
	switch {
	case errors.Is(err, credit.ErrQueryTooShort):
		return invalidField(c, "q")
	case errors.Is(err, credit.ErrBadCursor):
		return response.ValidationError(c, i18n.KeyCursorInvalid, map[string]string{"cursor": "invalid"})
	case err != nil:
		return adminFailure(c, "list users", err)
	}
	return response.OK(c, page)
}

// GetUser handles GET /api/admin/users/:id.
func (h *AdminHandler) GetUser(c *fiber.Ctx) error {
	d, err := h.credits.User(c.UserContext(), c.Params("id"))
	if errors.Is(err, credit.ErrUserNotFound) {
		return response.NotFound(c, i18n.KeyAdminNotFound)
	}
	if err != nil {
		return adminFailure(c, "get user", err)
	}
	return response.OK(c, d)
}

type adjustRequest struct {
	Amount    *int          `json:"amount"`
	Note      *string       `json:"note"`
	ValidDays nullable[int] `json:"validDays"`
	RequestID *string       `json:"requestId"`
}

// Adjust handles POST /api/admin/users/:id/adjust.
func (h *AdminHandler) Adjust(c *fiber.Ctx) error {
	by := middleware.GetAdminEmail(c)
	if by == "" {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var req adjustRequest
	if !decodeBody(c, &req) {
		return invalidField(c, "body")
	}
	switch {
	case req.Amount == nil:
		return invalidField(c, "amount")
	case req.Note == nil:
		return invalidField(c, "note")
	case !req.ValidDays.Present:
		return invalidField(c, "validDays")
	case req.RequestID == nil:
		return invalidField(c, "requestId")
	}
	res, err := h.credits.Adjust(c.UserContext(), c.Params("id"), credit.Adjustment{
		Amount: *req.Amount, Note: *req.Note, ValidDays: req.ValidDays.Value, RequestID: *req.RequestID,
	}, by)
	var tooLow *credit.BalanceTooLowError
	switch {
	case errors.Is(err, credit.ErrUserNotFound):
		return response.NotFound(c, i18n.KeyAdminNotFound)
	case errors.Is(err, credit.ErrAdjustInvalid):
		return invalidField(c, fieldOf(err))
	case errors.As(err, &tooLow):
		return response.BalanceTooLow(c, tooLow.Balance)
	case err != nil:
		return adminFailure(c, "adjust credits", err)
	}
	return response.OK(c, res)
}

// ListMovements handles GET /api/admin/movements.
func (h *AdminHandler) ListMovements(c *fiber.Ctx) error {
	q := credit.MovementQuery{Cursor: c.Query("cursor"), Kind: c.Query("kind"), UserID: c.Query("userId")}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return invalidField(c, "userId")
		}
	}
	if s := c.Query("campaignId"); s != "" {
		id, ok := credit.ParseCampaignUUID(s)
		if !ok {
			return invalidField(c, "campaignId")
		}
		q.CampaignID = &id
	}
	if s := c.Query("from"); s != "" {
		d, err := credit.ParseDay(s)
		if err != nil {
			return invalidField(c, "from")
		}
		q.From = &d
	}
	if s := c.Query("to"); s != "" {
		d, err := credit.ParseDay(s)
		if err != nil {
			return invalidField(c, "to")
		}
		q.To = &d
	}
	page, err := h.credits.Movements(c.UserContext(), q)
	switch {
	case errors.Is(err, credit.ErrBadCursor):
		return response.ValidationError(c, i18n.KeyCursorInvalid, map[string]string{"cursor": "invalid"})
	case errors.Is(err, credit.ErrBadFilter):
		return invalidField(c, "kind")
	case err != nil:
		return adminFailure(c, "list movements", err)
	}
	return response.OK(c, page)
}
