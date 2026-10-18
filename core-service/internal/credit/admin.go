package credit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/identity"
)

// Admin errors.
var (
	// ErrUserNotFound is returned for an unknown or deleted user.
	ErrUserNotFound = errors.New("credit: user not found")
	// ErrQueryTooShort is returned for a search text under 3 characters.
	ErrQueryTooShort = errors.New("credit: search text too short")
	// ErrAdjustInvalid is returned for a credit adjustment that fails
	// validation; the error is a *FieldError naming the field.
	ErrAdjustInvalid = errors.New("credit: adjustment invalid")
)

// BalanceTooLowError carries the balance of a refused deduction. It matches
// ErrBalanceTooLow.
type BalanceTooLowError struct{ Balance int }

func (e *BalanceTooLowError) Error() string { return ErrBalanceTooLow.Error() }

// Unwrap lets errors.Is find ErrBalanceTooLow.
func (e *BalanceTooLowError) Unwrap() error { return ErrBalanceTooLow }

// AdminPageSize is the page size of every admin list.
const AdminPageSize = 20

// minSearchLen is the shortest search text the user search accepts.
const minSearchLen = 3

// ShortID is the first 6 hex characters of a user id without dashes, upper
// case ("8F3A21").
func ShortID(userID string) string {
	hex := strings.ToUpper(strings.ReplaceAll(userID, "-", ""))
	if len(hex) > 6 {
		hex = hex[:6]
	}
	return hex
}

// UserDirectory is the read-only identity query the admin API uses for
// e-mails and the user list (the credit schema has no foreign key to users).
type UserDirectory interface {
	Search(ctx context.Context, q identity.UserSearch) ([]identity.DirectoryUser, error)
	ByID(ctx context.Context, id string) (identity.DirectoryUser, bool, error)
	Emails(ctx context.Context, ids []string) (map[string]string, error)
}

// AdminLedger is the part of the ledger the admin API uses.
type AdminLedger interface {
	Lots(ctx context.Context, userID string, now time.Time) ([]Lot, error)
	ReadAccount(ctx context.Context, userID string) (Account, bool, error)
	LatestSubscription(ctx context.Context, userID string) (*Subscription, error)
	Grant(ctx context.Context, userID, tur, ref string, g Grant, now time.Time) (int, error)
	DeductAdmin(ctx context.Context, userID, ref string, amount int, by, note string, now time.Time) (int, error)
	MovementOwner(ctx context.Context, tur, ref string) (string, bool, error)
}

// UserSummary is what the user list shows besides the e-mail.
type UserSummary struct {
	Balance      int
	Plan         *Plan
	CampaignName *string
}

// AdminMovementRow is one ledger movement of any user.
type AdminMovementRow struct {
	Movement
	UserID     string
	By, Note   *string
	CampaignID *int64
}

// AdminMovementFilter narrows the admin movement list. The filters combine.
type AdminMovementFilter struct {
	// Tur is a ledger kind (kredi.hareket.tur); Ref one movement reference.
	Tur, Ref   string
	CampaignID *int64
	UserID     string
	// From is the first instant, To the first instant after the range.
	From, To *time.Time
	Cursor   string
	Limit    int
}

// AdminStore is the admin queries over the ledger tables.
type AdminStore interface {
	UserSummaries(ctx context.Context, ids []string, now time.Time) (map[string]UserSummary, error)
	AdminMovements(ctx context.Context, f AdminMovementFilter) ([]AdminMovementRow, string, error)
}

// Admin serves the admin panel's user and movement views and manual credit
// adjustments.
type Admin struct {
	ledger AdminLedger
	store  AdminStore
	dir    UserDirectory
	now    func() time.Time
}

// NewAdmin builds the admin service. A nil clock is time.Now.
func NewAdmin(ledger AdminLedger, store AdminStore, dir UserDirectory, now func() time.Time) *Admin {
	if now == nil {
		now = time.Now
	}
	return &Admin{ledger: ledger, store: store, dir: dir, now: func() time.Time { return now().UTC() }}
}

// AdminPlanBrief is the plan shown in the user list.
type AdminPlanBrief struct {
	Tier   Tier   `json:"tier"`
	Status string `json:"status"`
}

// AdminUserItem is one row of the user list.
type AdminUserItem struct {
	UserID       string          `json:"userId"`
	ShortID      string          `json:"shortId"`
	Email        string          `json:"email"`
	Balance      int             `json:"balance"`
	Plan         *AdminPlanBrief `json:"plan"`
	CampaignName *string         `json:"campaignName"`
	CreatedAt    time.Time       `json:"createdAt"`
}

// AdminUserPage is one page of the user list.
type AdminUserPage struct {
	Items      []AdminUserItem `json:"items"`
	NextCursor *string         `json:"nextCursor"`
}

// AdminMovement is one movement of any user as the admin API shows it.
type AdminMovement struct {
	ID         string    `json:"id"`
	UserID     string    `json:"userId"`
	ShortID    string    `json:"shortId"`
	Email      *string   `json:"email"`
	Kind       string    `json:"kind"`
	Operation  *string   `json:"operation"`
	Amount     int       `json:"amount"`
	CreatedAt  time.Time `json:"createdAt"`
	By         *string   `json:"by"`
	Note       *string   `json:"note"`
	ProjectID  *string   `json:"projectId"`
	Tier       *string   `json:"tier"`
	CampaignID *int64    `json:"campaignId"`
}

// AdminMovementPage is one page of movements, newest first.
type AdminMovementPage struct {
	Items      []AdminMovement `json:"items"`
	NextCursor *string         `json:"nextCursor"`
}

// AdminUserDetail is the credit detail of one user.
type AdminUserDetail struct {
	User       AdminUserItem   `json:"user"`
	Debt       int             `json:"debt"`
	Lots       []LotView       `json:"lots"`
	Plan       *Plan           `json:"plan"`
	Movements  []AdminMovement `json:"movements"`
	NextCursor *string         `json:"nextCursor"`
}

// Adjustment is a manual credit change: amount > 0 adds a lot, amount < 0
// takes credits away.
type Adjustment struct {
	Amount int
	Note   string
	// ValidDays is the life of an added lot; nil: never expires. Only for
	// amount > 0.
	ValidDays *int
	// RequestID is the idempotency key (a UUID).
	RequestID string
}

// AdjustResult is the balance after the change and the movement it wrote.
type AdjustResult struct {
	Balance  int           `json:"balance"`
	Movement AdminMovement `json:"movement"`
}

// Bounds of an adjustment (contract AdjustCreditsRequest).
const (
	maxAdjustAmount = 100000
	minNoteLen      = 3
	maxNoteLen      = 500
	maxAdjustDays   = 3650
)

func adjustField(field string) error { return &FieldError{Field: field, base: ErrAdjustInvalid} }

func (in Adjustment) validate() error {
	note := strings.TrimSpace(in.Note)
	switch {
	case in.Amount == 0 || in.Amount > maxAdjustAmount || in.Amount < -maxAdjustAmount:
		return adjustField("amount")
	case utf8.RuneCountInString(note) < minNoteLen || utf8.RuneCountInString(note) > maxNoteLen:
		return adjustField("note")
	case in.ValidDays != nil && (in.Amount < 0 || *in.ValidDays < 1 || *in.ValidDays > maxAdjustDays):
		return adjustField("validDays")
	}
	if u, err := uuid.Parse(in.RequestID); err != nil || u.String() != strings.ToLower(in.RequestID) {
		return adjustField("requestId")
	}
	return nil
}

// userCursor is the position of the last user of a page in (created_at,
// id) descending order.
type userCursor struct {
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

func encodeUserCursor(u identity.DirectoryUser) string {
	raw, _ := json.Marshal(userCursor{T: u.CreatedAt.UTC(), ID: u.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeUserCursor(s string) (userCursor, error) {
	var c userCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.T.IsZero() {
		return userCursor{}, ErrBadCursor
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return userCursor{}, ErrBadCursor
	}
	return c, nil
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return s != ""
}

// searchOf reads the admin search text: a full user id, a short id prefix
// (with or without "#", at least 3 characters) or part of an e-mail. ok is
// false when nothing can match.
func searchOf(q string) (s identity.UserSearch, ok bool, err error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return s, true, nil
	}
	hashed := strings.HasPrefix(q, "#")
	text := strings.TrimPrefix(q, "#")
	if utf8.RuneCountInString(text) < minSearchLen {
		return s, false, ErrQueryTooShort
	}
	if !hashed && len(q) == 36 {
		if id, perr := uuid.Parse(q); perr == nil {
			s.ID = id.String()
			return s, true, nil
		}
	}
	if isHex(text) {
		s.ShortPrefix = strings.ToLower(text)
	}
	if !hashed {
		s.Email = strings.ToLower(text)
	}
	return s, s.ShortPrefix != "" || s.Email != "", nil
}

// Users searches the users, newest account first, 20 per page. q is empty
// (everyone), a full user id, a short id prefix or part of an e-mail; a
// text under 3 characters gives ErrQueryTooShort and a damaged cursor
// ErrBadCursor.
func (a *Admin) Users(ctx context.Context, q, cursor string) (AdminUserPage, error) {
	search, ok, err := searchOf(q)
	if err != nil {
		return AdminUserPage{}, err
	}
	if cursor != "" {
		c, err := decodeUserCursor(cursor)
		if err != nil {
			return AdminUserPage{}, err
		}
		search.AfterTime, search.AfterID = c.T, c.ID
	}
	page := AdminUserPage{Items: []AdminUserItem{}}
	if !ok {
		return page, nil
	}
	search.Limit = AdminPageSize + 1
	users, err := a.dir.Search(ctx, search)
	if err != nil {
		return AdminUserPage{}, err
	}
	if len(users) > AdminPageSize {
		users = users[:AdminPageSize]
		next := encodeUserCursor(users[len(users)-1])
		page.NextCursor = &next
	}
	items, err := a.userItems(ctx, users)
	if err != nil {
		return AdminUserPage{}, err
	}
	page.Items = items
	return page, nil
}

func (a *Admin) userItems(ctx context.Context, users []identity.DirectoryUser) ([]AdminUserItem, error) {
	items := make([]AdminUserItem, 0, len(users))
	if len(users) == 0 {
		return items, nil
	}
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	sums, err := a.store.UserSummaries(ctx, ids, a.now())
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		s := sums[u.ID]
		item := AdminUserItem{
			UserID: u.ID, ShortID: ShortID(u.ID), Email: u.Email, Balance: s.Balance,
			CampaignName: s.CampaignName, CreatedAt: u.CreatedAt.UTC(),
		}
		if s.Plan != nil {
			item.Plan = &AdminPlanBrief{Tier: s.Plan.Tier, Status: s.Plan.Status}
		}
		items = append(items, item)
	}
	return items, nil
}

// User returns the credit detail of one user. ErrUserNotFound for an
// unknown or deleted user.
func (a *Admin) User(ctx context.Context, userID string) (AdminUserDetail, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return AdminUserDetail{}, ErrUserNotFound
	}
	u, found, err := a.dir.ByID(ctx, userID)
	if err != nil {
		return AdminUserDetail{}, err
	}
	if !found {
		return AdminUserDetail{}, ErrUserNotFound
	}
	items, err := a.userItems(ctx, []identity.DirectoryUser{u})
	if err != nil {
		return AdminUserDetail{}, err
	}
	now := a.now()
	acct, _, err := a.ledger.ReadAccount(ctx, userID)
	if err != nil {
		return AdminUserDetail{}, err
	}
	lots, err := a.ledger.Lots(ctx, userID, now)
	if err != nil {
		return AdminUserDetail{}, err
	}
	sub, err := a.ledger.LatestSubscription(ctx, userID)
	if err != nil {
		return AdminUserDetail{}, err
	}
	d := AdminUserDetail{User: items[0], Debt: acct.Debt, Lots: []LotView{}, Plan: planOf(sub)}
	for _, l := range lots {
		d.Lots = append(d.Lots, LotView{Source: l.Source.JSONSource(), Amount: l.Amount, ExpiresAt: l.ExpiresAt})
	}
	page, err := a.Movements(ctx, MovementQuery{UserID: userID})
	if err != nil {
		return AdminUserDetail{}, err
	}
	d.Movements, d.NextCursor = page.Items, page.NextCursor
	return d, nil
}

// MovementQuery filters the admin movement list. Kind is an API kind
// ("admin_adjust"); From and To are calendar days (inclusive, read in
// Europe/Istanbul).
type MovementQuery struct {
	Cursor     string
	Kind       string
	CampaignID *int64
	UserID     string
	From, To   *time.Time
}

// kindTur is movementKinds reversed: API kind to ledger kind.
var kindTur = func() map[string]string {
	m := make(map[string]string, len(movementKinds))
	for tur, kind := range movementKinds {
		m[kind] = tur
	}
	return m
}()

// Movements lists movements of all users, newest first, 20 per page. An
// unknown kind gives ErrBadFilter and a damaged cursor ErrBadCursor.
func (a *Admin) Movements(ctx context.Context, q MovementQuery) (AdminMovementPage, error) {
	f := AdminMovementFilter{CampaignID: q.CampaignID, UserID: q.UserID, Cursor: q.Cursor, Limit: AdminPageSize}
	if q.Kind != "" {
		tur, ok := kindTur[q.Kind]
		if !ok {
			return AdminMovementPage{}, ErrBadFilter
		}
		f.Tur = tur
	}
	if q.UserID != "" {
		if _, err := uuid.Parse(q.UserID); err != nil {
			return AdminMovementPage{}, ErrBadFilter
		}
	}
	if q.From != nil {
		from := dayStart(*q.From)
		f.From = &from
	}
	if q.To != nil {
		to := dayStart(*q.To).AddDate(0, 0, 1)
		f.To = &to
	}
	rows, next, err := a.store.AdminMovements(ctx, f)
	if err != nil {
		return AdminMovementPage{}, err
	}
	items, err := a.movementViews(ctx, rows)
	if err != nil {
		return AdminMovementPage{}, err
	}
	page := AdminMovementPage{Items: items}
	if next != "" {
		page.NextCursor = &next
	}
	return page, nil
}

func (a *Admin) movementViews(ctx context.Context, rows []AdminMovementRow) ([]AdminMovement, error) {
	out := make([]AdminMovement, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, r := range rows {
		if !seen[r.UserID] {
			seen[r.UserID] = true
			ids = append(ids, r.UserID)
		}
	}
	emails, err := a.dir.Emails(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		kind, op, project, tier := movementFields(r.Movement)
		m := AdminMovement{
			ID: "mv_" + strconv.FormatInt(r.ID, 10), UserID: r.UserID, ShortID: ShortID(r.UserID),
			Kind: kind, Operation: op, Amount: r.Amount, CreatedAt: r.CreatedAt.UTC(),
			ProjectID: project, Tier: tier,
		}
		if email, ok := emails[r.UserID]; ok {
			m.Email = &email
		}
		if r.Tur == turYonetici {
			m.By, m.Note = r.By, r.Note
		}
		if r.CampaignID != nil {
			id := *r.CampaignID
			m.CampaignID = &id
		}
		out = append(out, m)
	}
	return out, nil
}

// Adjust adds or takes away credits by hand and records the admin on an
// admin_adjust movement. The request id is the idempotency key: the same id
// again writes nothing more and answers the first movement. ErrUserNotFound
// for an unknown user, *FieldError (ErrAdjustInvalid) for bad input and
// *BalanceTooLowError for a deduction beyond the balance.
func (a *Admin) Adjust(ctx context.Context, userID string, in Adjustment, by string) (AdjustResult, error) {
	if err := in.validate(); err != nil {
		return AdjustResult{}, err
	}
	if _, err := uuid.Parse(userID); err != nil {
		return AdjustResult{}, ErrUserNotFound
	}
	_, found, err := a.dir.ByID(ctx, userID)
	if err != nil {
		return AdjustResult{}, err
	}
	if !found {
		return AdjustResult{}, ErrUserNotFound
	}
	ref := strings.ToLower(in.RequestID)
	note := strings.TrimSpace(in.Note)
	// One request id belongs to one user: a replay on another user is a
	// client bug, not a free pass.
	if owner, exists, err := a.ledger.MovementOwner(ctx, turYonetici, ref); err != nil {
		return AdjustResult{}, err
	} else if exists && owner != userID {
		return AdjustResult{}, adjustField("requestId")
	}
	now := a.now()
	var balance int
	if in.Amount > 0 {
		g := Grant{Source: LotAdmin, Amount: in.Amount, By: by, Note: note}
		if in.ValidDays != nil {
			exp := now.AddDate(0, 0, *in.ValidDays)
			g.ExpiresAt = &exp
		}
		balance, err = a.ledger.Grant(ctx, userID, turYonetici, ref, g, now)
	} else {
		balance, err = a.ledger.DeductAdmin(ctx, userID, ref, -in.Amount, by, note, now)
		if errors.Is(err, ErrBalanceTooLow) {
			return AdjustResult{}, &BalanceTooLowError{Balance: balance}
		}
	}
	if err != nil {
		return AdjustResult{}, err
	}
	rows, _, err := a.store.AdminMovements(ctx, AdminMovementFilter{Tur: turYonetici, Ref: ref, Limit: 1})
	if err != nil {
		return AdjustResult{}, err
	}
	if len(rows) == 0 {
		return AdjustResult{}, fmt.Errorf("credit: adjustment %s has no movement", ref)
	}
	views, err := a.movementViews(ctx, rows[:1])
	if err != nil {
		return AdjustResult{}, err
	}
	return AdjustResult{Balance: balance, Movement: views[0]}, nil
}
