package credit

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"
)

const (
	// HistoryPageSize is the number of movements per history page.
	HistoryPageSize = 20

	actionWindow       = 10 * time.Minute
	lyricsGenerateUses = 12
	lyricsRewriteUses  = 2
)

// ErrBadCursor is returned by History for a cursor that is not a movement id.
var ErrBadCursor = errors.New("credit: bad history cursor")

// ErrUnknownOperation is returned for an operation without a price.
var ErrUnknownOperation = errors.New("credit: unknown operation")

// Spender is what the render and lyrics paths need from the credit system.
type Spender interface {
	// Spend debits op once per ref and returns the new balance.
	Spend(ctx context.Context, userID string, op Operation, ref string) (balance int, err error)
	// SpendAction charges the first request of an action and lets its
	// follow-ups through free; charged is true only for the first.
	SpendAction(ctx context.Context, userID string, op Operation, actionID string) (balance int, charged bool, err error)
	// Refund reverses the debit made with ref.
	Refund(ctx context.Context, op Operation, ref string) error
	// RefundAction reverses the charge of an action.
	RefundAction(ctx context.Context, userID string, op Operation, actionID string) error
	// Balance reads the balance without creating an account.
	Balance(ctx context.Context, userID string) (int, error)
	// Enforced reports whether debits are real (false = shadow mode).
	Enforced() bool
}

// Ledger is the store surface the service uses.
type Ledger interface {
	EnsureAccount(ctx context.Context, userID string) (Account, error)
	ReadAccount(ctx context.Context, userID string) (Account, bool, error)
	Debit(ctx context.Context, userID string, tur, ref, reason string, amount int, allowNegative bool) (Account, bool, error)
	Refund(ctx context.Context, debitTur, refundTur, ref string) error
	Credit(ctx context.Context, userID, tur, ref, reason string, amount int) (Account, bool, error)
	SetPlan(ctx context.Context, userID, tur, ref string, amount int, periodEnd *time.Time) (Account, bool, error)
	ClawBack(ctx context.Context, userID, ref string, amount int) (Account, bool, error)
	SpendAction(ctx context.Context, userID string, op Operation, actionID string, amount int, window time.Duration, maxUses int, allowNegative bool) (ActionCharge, error)
	DropAction(ctx context.Context, ref string) error
	History(ctx context.Context, userID string, beforeID int64, limit int) ([]Movement, error)
	DeleteUserData(ctx context.Context, userID string) error
	LatestSubscription(ctx context.Context, userID string) (*Subscription, error)
}

// Settings is the service configuration.
type Settings struct {
	Enforce     bool
	Welcome     int
	PlanMonthly int
	Costs       Costs
}

// Service holds the credit business rules. It implements Spender.
type Service struct {
	ledger Ledger
	set    Settings
}

var _ Spender = (*Service)(nil)

// NewService builds the service over ledger.
func NewService(ledger Ledger, s Settings) *Service {
	return &Service{ledger: ledger, set: s}
}

// Enforced reports whether debits are real.
func (s *Service) Enforced() bool { return s.set.Enforce }

// Balance reads the balance; a missing account reads as 0.
func (s *Service) Balance(ctx context.Context, userID string) (int, error) {
	a, _, err := s.ledger.ReadAccount(ctx, userID)
	if err != nil {
		return 0, err
	}
	return a.Balance(), nil
}

// shadow handles a call in shadow mode: it logs a would-be 402 and returns
// the current balance, writing nothing.
func (s *Service) shadow(ctx context.Context, userID string, op Operation) (int, error) {
	bal, err := s.Balance(ctx, userID)
	if err != nil {
		return 0, err
	}
	if cost := s.set.Costs.Of(op); bal < cost {
		log.Printf("credits: shadow insufficient user=%s op=%s required=%d balance=%d", userID, op, cost, bal)
	}
	return bal, nil
}

// Spend debits the cost of op once per ref.
func (s *Service) Spend(ctx context.Context, userID string, op Operation, ref string) (int, error) {
	if !s.set.Enforce {
		return s.shadow(ctx, userID, op)
	}
	if !knownOperation(op) {
		return 0, ErrUnknownOperation
	}
	acct, _, err := s.ledger.Debit(ctx, userID, "render", ref, string(op), s.set.Costs.Of(op), false)
	if err != nil {
		return 0, err
	}
	return acct.Balance(), nil
}

func knownOperation(op Operation) bool {
	switch op {
	case OpRenderMix, OpRenderFocus, OpRenderStems, OpLyricsGenerate, OpLyricsRewrite:
		return true
	}
	return false
}

// actionUses is the request budget of one action.
func actionUses(op Operation) int {
	switch op {
	case OpLyricsGenerate:
		return lyricsGenerateUses
	case OpLyricsRewrite:
		return lyricsRewriteUses
	default:
		return 1
	}
}

// SpendAction charges the first request of an action.
func (s *Service) SpendAction(ctx context.Context, userID string, op Operation, actionID string) (int, bool, error) {
	if !s.set.Enforce {
		bal, err := s.shadow(ctx, userID, op)
		return bal, false, err
	}
	if !knownOperation(op) {
		return 0, false, ErrUnknownOperation
	}
	ch, err := s.ledger.SpendAction(ctx, userID, op, actionID, s.set.Costs.Of(op), actionWindow, actionUses(op), false)
	if err != nil {
		return 0, false, err
	}
	return ch.Account.Balance(), ch.Charged, nil
}

// Refund reverses the render debit made with ref.
func (s *Service) Refund(ctx context.Context, _ Operation, ref string) error {
	if !s.set.Enforce {
		return nil
	}
	return s.ledger.Refund(ctx, "render", "render_iade", ref)
}

// RefundAction reverses the charge of an action.
func (s *Service) RefundAction(ctx context.Context, userID string, _ Operation, actionID string) error {
	if !s.set.Enforce {
		return nil
	}
	return s.ledger.Refund(ctx, "soz", "soz_iade", userID+":"+actionID)
}

// Plan is the caller's Pro subscription as the API shows it.
type Plan struct {
	Product   string    `json:"product"`
	Status    string    `json:"status"`
	PeriodEnd time.Time `json:"periodEnd"`
	AutoRenew bool      `json:"autoRenew"`
}

// CostTable is the price table as the API shows it.
type CostTable struct {
	Render RenderCosts `json:"render"`
	Lyrics LyricsCosts `json:"lyrics"`
}

// RenderCosts are the render prices.
type RenderCosts struct {
	Mix   int `json:"mix"`
	Focus int `json:"focus"`
	Stems int `json:"stems"`
}

// LyricsCosts are the lyrics prices.
type LyricsCosts struct {
	Generate int `json:"generate"`
	Rewrite  int `json:"rewrite"`
}

// Welcome is the state of the welcome gift.
type Welcome struct {
	Available bool `json:"available"`
	Amount    int  `json:"amount"`
}

// Summary is the answer of GET /api/credits.
type Summary struct {
	Balance           int       `json:"balance"`
	PlanCredits       int       `json:"planCredits"`
	PermanentCredits  int       `json:"permanentCredits"`
	Plan              *Plan     `json:"plan"`
	Costs             CostTable `json:"costs"`
	Welcome           Welcome   `json:"welcome"`
	StoreAccountToken string    `json:"storeAccountToken"`
	Enforced          bool      `json:"enforced"`
}

var planStatus = map[string]string{
	"aktif":        "active",
	"odeme_sorunu": "billing_issue",
	"ek_sure":      "grace",
	"bitti":        "expired",
	"iade":         "refunded",
}

// Summary reads (creating when missing) the user's account and plan.
func (s *Service) Summary(ctx context.Context, userID string) (Summary, error) {
	acct, err := s.ledger.EnsureAccount(ctx, userID)
	if err != nil {
		return Summary{}, err
	}
	sub, err := s.ledger.LatestSubscription(ctx, userID)
	if err != nil {
		return Summary{}, err
	}
	c := s.set.Costs
	out := Summary{
		Balance:           acct.Balance(),
		PlanCredits:       acct.Plan,
		PermanentCredits:  acct.Permanent,
		Costs:             CostTable{Render: RenderCosts{Mix: c.RenderMix, Focus: c.RenderFocus, Stems: c.RenderStems}, Lyrics: LyricsCosts{Generate: c.LyricsGenerate, Rewrite: c.LyricsRewrite}},
		Welcome:           Welcome{Available: !acct.WelcomeClaimed, Amount: s.set.Welcome},
		StoreAccountToken: acct.StoreToken.String(),
		Enforced:          s.set.Enforce,
	}
	if sub != nil {
		if status, ok := planStatus[sub.Status]; ok && !sub.PeriodEnd.IsZero() {
			out.Plan = &Plan{Product: sub.Product, Status: status, PeriodEnd: sub.PeriodEnd, AutoRenew: sub.AutoRenew}
		}
	}
	return out, nil
}

// ClaimWelcome grants the welcome gift once and returns the new summary.
func (s *Service) ClaimWelcome(ctx context.Context, userID string) (Summary, error) {
	if _, _, err := s.ledger.Credit(ctx, userID, turHediye, userID, "", s.set.Welcome); err != nil {
		return Summary{}, err
	}
	return s.Summary(ctx, userID)
}

// HistoryItem is one movement as the API shows it.
type HistoryItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Operation *string   `json:"operation"`
	Amount    int       `json:"amount"`
	CreatedAt time.Time `json:"createdAt"`
}

// HistoryPage is one page of movements, newest first.
type HistoryPage struct {
	Items      []HistoryItem `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

var movementKind = map[string]string{
	"render":      "render",
	"render_iade": "render_refund",
	"soz":         "lyrics",
	"soz_iade":    "lyrics_refund",
	"hediye":      "welcome",
	"paket":       "pack",
	"plan_dolum":  "plan_refill",
	"plan_bitis":  "plan_end",
	"magaza_iade": "store_refund",
}

// operationKinds are the kinds whose reason names the operation bought.
var operationKinds = map[string]bool{"render": true, "render_refund": true, "lyrics": true, "lyrics_refund": true}

// History returns one page of the user's movements. cursor is the
// NextCursor of the previous page; empty means the first page.
func (s *Service) History(ctx context.Context, userID, cursor string) (HistoryPage, error) {
	var before int64
	if cursor != "" {
		id, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || id <= 0 {
			return HistoryPage{}, ErrBadCursor
		}
		before = id
	}
	rows, err := s.ledger.History(ctx, userID, before, HistoryPageSize+1)
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Items: []HistoryItem{}}
	if len(rows) > HistoryPageSize {
		rows = rows[:HistoryPageSize]
		next := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		page.NextCursor = &next
	}
	for _, m := range rows {
		kind, ok := movementKind[m.Tur]
		if !ok {
			kind = m.Tur
		}
		item := HistoryItem{ID: strconv.FormatInt(m.ID, 10), Kind: kind, Amount: m.Amount, CreatedAt: m.CreatedAt}
		if operationKinds[kind] && m.Reason != "" {
			op := m.Reason
			item.Operation = &op
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}
