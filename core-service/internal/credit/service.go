package credit

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	actionWindow       = 10 * time.Minute
	lyricsGenerateUses = 12
	lyricsRewriteUses  = 2
)

// ErrUnknownOperation is returned for an operation without a price.
var ErrUnknownOperation = errors.New("credit: unknown operation")

// Spender is what the render and lyrics paths need from the credit system.
type Spender interface {
	// Spend debits op once per ref and returns the new balance. projectID
	// is the project the spend belongs to ("" = none); the history shows it.
	Spend(ctx context.Context, userID string, op Operation, ref, projectID string) (balance int, err error)
	// SpendAction charges the first request of an action and lets its
	// follow-ups through free; charged is true only for the first.
	SpendAction(ctx context.Context, userID string, op Operation, actionID string) (balance int, charged bool, err error)
	// Refund reverses the debit made with ref.
	Refund(ctx context.Context, op Operation, ref string) error
	// RefundAction reverses the charge of an action.
	RefundAction(ctx context.Context, userID string, op Operation, actionID string) error
	// Balance reads the balance without creating an account.
	Balance(ctx context.Context, userID string) (int, error)
	// HasPro reports whether the user has an active Pro plan (P4).
	HasPro(ctx context.Context, userID string) (bool, error)
	// Enforced reports whether debits are real (false = shadow mode).
	Enforced() bool
}

// Ledger is the store surface the service uses.
type Ledger interface {
	EnsureAccount(ctx context.Context, userID string) (Account, error)
	ReadAccount(ctx context.Context, userID string) (Account, bool, error)
	Balance(ctx context.Context, userID string, now time.Time) (int, error)
	Lots(ctx context.Context, userID string, now time.Time) ([]Lot, error)
	Gift(ctx context.Context, userID string, now time.Time) (*Lot, error)
	Debit(ctx context.Context, userID, tur, ref string, amount int, meta Meta, now time.Time) (int, error)
	DeductAdmin(ctx context.Context, userID, ref string, amount int, by, note string, now time.Time) (int, error)
	Refund(ctx context.Context, userID, debitTur, refundTur, ref string, now time.Time, grace time.Duration) (int, bool, error)
	MovementOwner(ctx context.Context, tur, ref string) (string, bool, error)
	Grant(ctx context.Context, userID, tur, ref string, g Grant, now time.Time) (int, error)
	EndPlanLots(ctx context.Context, userID, tur, ref string, now time.Time) (int, error)
	ExtendPlanLot(ctx context.Context, userID string, until time.Time) error
	ClawBackPack(ctx context.Context, userID, packRef, refundRef string, amount int, now time.Time) (int, error)
	ExpireLots(ctx context.Context, now time.Time, limit int) (int, error)
	SpendAction(ctx context.Context, userID string, op Operation, actionID string, amount int, window time.Duration, maxUses int, now time.Time) (ActionCharge, error)
	DropAction(ctx context.Context, ref string) error
	History(ctx context.Context, userID, cursor string, filter HistoryFilter) (Page, error)
	DeleteUserData(ctx context.Context, userID string) error
	LatestSubscription(ctx context.Context, userID string) (*Subscription, error)
}

// Settings is the service configuration.
type Settings struct {
	Enforce bool
	// PlanCredits is the credits each tier gives per month.
	PlanCredits map[Tier]int
	Costs       Costs
	// RefundGrace is how long the lot lasts that a refund into an expired
	// lot creates.
	RefundGrace time.Duration
}

// Service holds the credit business rules. It implements Spender.
type Service struct {
	ledger Ledger
	set    Settings
	now    func() time.Time
}

var _ Spender = (*Service)(nil)

// NewService builds the service over ledger.
func NewService(ledger Ledger, s Settings) *Service {
	return &Service{ledger: ledger, set: s, now: time.Now}
}

// Enforced reports whether debits are real.
func (s *Service) Enforced() bool { return s.set.Enforce }

// Balance reads the balance; a missing account reads as 0.
func (s *Service) Balance(ctx context.Context, userID string) (int, error) {
	return s.ledger.Balance(ctx, userID, s.now())
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
func (s *Service) Spend(ctx context.Context, userID string, op Operation, ref, projectID string) (int, error) {
	if !s.set.Enforce {
		return s.shadow(ctx, userID, op)
	}
	if !knownOperation(op) {
		return 0, ErrUnknownOperation
	}
	return s.ledger.Debit(ctx, userID, debitTur(op), ref, s.set.Costs.Of(op),
		Meta{Reason: string(op), ProjectID: projectID}, s.now())
}

// debitTur is the ledger kind (kredi.hareket.tur) of the debit of op.
func debitTur(op Operation) string {
	switch op {
	case OpMaster:
		return "master"
	case OpLyricsGenerate, OpLyricsRewrite:
		return turSoz
	default:
		return "render"
	}
}

// refundTur is the ledger kind of the refund of op.
func refundTur(op Operation) string { return debitTur(op) + "_iade" }

func knownOperation(op Operation) bool {
	switch op {
	case OpRenderMix, OpRenderFocus, OpRenderStems, OpLyricsGenerate, OpLyricsRewrite, OpMaster:
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
	ch, err := s.ledger.SpendAction(ctx, userID, op, actionID, s.set.Costs.Of(op), actionWindow, actionUses(op), s.now())
	if err != nil {
		return 0, false, err
	}
	return ch.Balance, ch.Charged, nil
}

// Refund reverses the debit of op made with ref (render or master). It runs
// in every mode: the ledger writes a refund only for an existing debit, so a
// debit made while enforcing is still refunded after a rollback or on a
// shadow pod.
func (s *Service) Refund(ctx context.Context, op Operation, ref string) error {
	tur := debitTur(op)
	userID, ok, err := s.ledger.MovementOwner(ctx, tur, ref)
	if err != nil || !ok {
		return err
	}
	_, _, err = s.ledger.Refund(ctx, userID, tur, refundTur(op), ref, s.now(), s.set.RefundGrace)
	return err
}

// RefundAction reverses the charge of an action, in every mode (see Refund).
func (s *Service) RefundAction(ctx context.Context, userID string, op Operation, actionID string) error {
	_, _, err := s.ledger.Refund(ctx, userID, turSoz, refundTur(op), userID+":"+actionID, s.now(), s.set.RefundGrace)
	return err
}

// HasPro reports whether the user has an active Pro plan (P4): the latest
// subscription is a pro product in status active, billing issue or grace,
// and, except in grace, its period has not ended.
func (s *Service) HasPro(ctx context.Context, userID string) (bool, error) {
	sub, err := s.ledger.LatestSubscription(ctx, userID)
	if err != nil || sub == nil {
		return false, err
	}
	if tierOf(sub.Product) != TierPro {
		return false, nil
	}
	now := s.now()
	switch sub.Status {
	case subActive, subBillingIssue:
		return sub.PeriodEnd.After(now), nil
	case subGrace:
		// The store's grace keeps the right while the status says so (the
		// webhook or the resync ends it), whatever the plan lot holds.
		return true, nil
	}
	return false, nil
}

// tierOf is the tier of an internal subscription product ("pro.monthly").
func tierOf(product string) Tier {
	switch {
	case strings.HasPrefix(product, "pro."):
		return TierPro
	case strings.HasPrefix(product, "plus."):
		return TierPlus
	}
	return ""
}

// periodOf is the billing period of an internal subscription product.
func periodOf(product string) string {
	if _, p, ok := strings.Cut(product, "."); ok {
		return p
	}
	return ""
}

// Plan is the caller's subscription as the API shows it.
type Plan struct {
	Tier        Tier      `json:"tier"`
	Period      string    `json:"period"`
	Status      string    `json:"status"`
	PeriodEnd   time.Time `json:"periodEnd"`
	AutoRenew   bool      `json:"autoRenew"`
	PendingTier *Tier     `json:"pendingTier"`
}

// LotView is one lot as the API shows it.
type LotView struct {
	Source    string     `json:"source"`
	Amount    int        `json:"amount"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// PlanCredits are the monthly credits per tier.
type PlanCredits struct {
	Plus int `json:"plus"`
	Pro  int `json:"pro"`
}

// CostTable is the price table as the API shows it.
type CostTable struct {
	Render RenderCosts `json:"render"`
	Lyrics LyricsCosts `json:"lyrics"`
	Master int         `json:"master"`
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

// Gift is the caller's latest unexpired campaign gift.
type Gift struct {
	Amount    int       `json:"amount"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Summary is the answer of GET /api/credits (contract 1.9.0).
type Summary struct {
	Balance           int         `json:"balance"`
	Debt              int         `json:"debt"`
	Lots              []LotView   `json:"lots"`
	Plan              *Plan       `json:"plan"`
	PlanCredits       PlanCredits `json:"planCredits"`
	Costs             CostTable   `json:"costs"`
	Gift              *Gift       `json:"gift"`
	StoreAccountToken string      `json:"storeAccountToken"`
	Enforced          bool        `json:"enforced"`
}

var planStatus = map[string]string{
	"aktif":        "active",
	"odeme_sorunu": "billing_issue",
	"ek_sure":      "grace",
	"bitti":        "expired",
	"iade":         "refunded",
}

// planOf shows a subscription as the API plan; nil when there is none or it
// has no usable tier, status or period.
func planOf(sub *Subscription) *Plan {
	if sub == nil {
		return nil
	}
	status, ok := planStatus[sub.Status]
	tier := tierOf(sub.Product)
	if !ok || tier == "" || sub.PeriodEnd.IsZero() {
		return nil
	}
	plan := &Plan{Tier: tier, Period: periodOf(sub.Product), Status: status, PeriodEnd: sub.PeriodEnd, AutoRenew: sub.AutoRenew}
	if sub.NextProduct != nil {
		if next := tierOf(*sub.NextProduct); next != "" && next != tier {
			plan.PendingTier = &next
		}
	}
	return plan
}

// Summary reads (creating when missing) the user's account, lots and plan.
// The balance is the sum of the unexpired lots minus the debt, computed now.
func (s *Service) Summary(ctx context.Context, userID string) (Summary, error) {
	acct, err := s.ledger.EnsureAccount(ctx, userID)
	if err != nil {
		return Summary{}, err
	}
	now := s.now()
	lots, err := s.ledger.Lots(ctx, userID, now)
	if err != nil {
		return Summary{}, err
	}
	gift, err := s.ledger.Gift(ctx, userID, now)
	if err != nil {
		return Summary{}, err
	}
	sub, err := s.ledger.LatestSubscription(ctx, userID)
	if err != nil {
		return Summary{}, err
	}
	c := s.set.Costs
	out := Summary{
		Debt:        acct.Debt,
		Lots:        []LotView{},
		PlanCredits: PlanCredits{Plus: s.set.PlanCredits[TierPlus], Pro: s.set.PlanCredits[TierPro]},
		Costs: CostTable{
			Render: RenderCosts{Mix: c.RenderMix, Focus: c.RenderFocus, Stems: c.RenderStems},
			Lyrics: LyricsCosts{Generate: c.LyricsGenerate, Rewrite: c.LyricsRewrite},
			Master: c.Master,
		},
		StoreAccountToken: acct.StoreToken.String(),
		Enforced:          s.set.Enforce,
	}
	total := 0
	for _, l := range lots {
		out.Lots = append(out.Lots, LotView{Source: l.Source.JSONSource(), Amount: l.Amount, ExpiresAt: l.ExpiresAt})
		total += l.Amount
	}
	out.Balance = total - acct.Debt
	if gift != nil && gift.ExpiresAt != nil {
		out.Gift = &Gift{Amount: gift.Amount, ExpiresAt: *gift.ExpiresAt}
	}
	out.Plan = planOf(sub)
	return out, nil
}

// HistoryItem is one movement as the API shows it.
type HistoryItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Operation *string   `json:"operation"`
	Amount    int       `json:"amount"`
	ProjectID *string   `json:"projectId"`
	Tier      *string   `json:"tier"`
	CreatedAt time.Time `json:"createdAt"`
}

// HistoryPage is one page of movements, newest first.
type HistoryPage struct {
	Items      []HistoryItem `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

// operationKinds are the kinds whose reason names the operation bought.
var operationKinds = map[string]bool{
	"render": true, "render_refund": true, "lyrics": true, "lyrics_refund": true,
	"master": true, "master_refund": true,
}

// projectKinds are the kinds that carry a project id; tierKinds a tier.
var (
	projectKinds = map[string]bool{"render": true, "render_refund": true, "master": true, "master_refund": true}
	tierKinds    = map[string]bool{"plan_refill": true, "plan_end": true, "plan_expire": true}
)

// History returns one page of the user's movements. cursor is the
// NextCursor of the previous page (empty = first page); kind is "", "spend"
// or "earn", anything else gives ErrBadFilter.
func (s *Service) History(ctx context.Context, userID, cursor, kind string) (HistoryPage, error) {
	filter := HistoryFilter(kind)
	switch filter {
	case FilterAll, FilterSpend, FilterEarn:
	default:
		return HistoryPage{}, ErrBadFilter
	}
	p, err := s.ledger.History(ctx, userID, cursor, filter)
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Items: []HistoryItem{}}
	if p.Next != "" {
		next := p.Next
		page.NextCursor = &next
	}
	for _, m := range p.Items {
		kind, op, project, tier := movementFields(m)
		page.Items = append(page.Items, HistoryItem{
			ID: "mv_" + strconv.FormatInt(m.ID, 10), Kind: kind, Operation: op, Amount: m.Amount,
			ProjectID: project, Tier: tier, CreatedAt: m.CreatedAt,
		})
	}
	return page, nil
}

// movementFields derives the API fields of a ledger movement: its kind, and
// the operation, project and tier that only some kinds carry.
func movementFields(m Movement) (kind string, operation, project, tier *string) {
	kind = MovementKind(m.Tur)
	if operationKinds[kind] {
		op := m.Reason
		if op == "" && strings.HasPrefix(kind, "master") {
			op = string(OpMaster)
		}
		if op != "" {
			operation = &op
		}
	}
	if projectKinds[kind] {
		project = m.ProjectID
	}
	if tierKinds[kind] {
		tier = m.Tier
	}
	return kind, operation, project, tier
}
