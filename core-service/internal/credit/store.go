package credit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrActionExpired is returned by SpendAction when the action's window or
// request budget is used up, or when the action was refunded or dropped.
var ErrActionExpired = errors.New("credit: action expired")

// ErrBadCursor is returned by History for a damaged cursor.
var ErrBadCursor = errors.New("credit: bad history cursor")

// ErrBadFilter is returned by History for an unknown filter.
var ErrBadFilter = errors.New("credit: bad history filter")

// InsufficientError reports a debit the balance cannot cover.
type InsufficientError struct {
	Required int
	Balance  int
	// Debt is the open store-refund debt (>= 0); a debt refuses spending
	// even when Balance covers Required.
	Debt int
}

func (e *InsufficientError) Error() string {
	return fmt.Sprintf("credit: insufficient balance (required %d, balance %d)", e.Required, e.Balance)
}

// Account is a user's credit account. The balance is never stored: read it
// with Balance.
type Account struct {
	UserID     string
	Debt       int
	StoreToken uuid.UUID
}

// ActionCharge is the outcome of SpendAction.
type ActionCharge struct {
	Balance int
	Charged bool
}

// Movement is one ledger row as shown in the history.
type Movement struct {
	ID        int64
	Tur       string
	Ref       string
	Reason    string
	Amount    int
	ProjectID *string
	Tier      *string
	CreatedAt time.Time
}

// HistoryFilter narrows the history to spends or earnings.
type HistoryFilter string

// History filters.
const (
	FilterAll   HistoryFilter = ""
	FilterSpend HistoryFilter = "spend"
	FilterEarn  HistoryFilter = "earn"
)

// Page is one page of movements, newest first. Next is the opaque cursor
// of the following page, empty on the last one.
type Page struct {
	Items []Movement
	Next  string
}

// PostgresStore is the ledger store on the kredi schema.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore returns a store over pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("could not start a credit transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("could not commit the credit transaction: %w", err)
	}
	return nil
}

const accountColumns = `kullanici_id::text, borc, magaza_token`

// lockAccount reads and row-locks the account; with create it inserts the
// row first when missing. A missing row without create yields pgx.ErrNoRows.
// Every write of a user takes this lock first, so writes of one user are
// serialised and the lock order is always hesap, then dilim.
func lockAccount(ctx context.Context, tx pgx.Tx, userID string, create bool) (Account, error) {
	if create {
		if _, err := tx.Exec(ctx,
			`INSERT INTO kredi.hesap (kullanici_id) VALUES ($1::text::uuid) ON CONFLICT DO NOTHING`, userID); err != nil {
			return Account{}, fmt.Errorf("could not ensure the credit account: %w", err)
		}
	}
	return scanAccount(tx.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM kredi.hesap WHERE kullanici_id = $1::text::uuid FOR UPDATE`, userID))
}

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	if err := row.Scan(&a.UserID, &a.Debt, &a.StoreToken); err != nil {
		return Account{}, err
	}
	return a, nil
}

// EnsureAccount returns the user's account, creating it when missing.
func (s *PostgresStore) EnsureAccount(ctx context.Context, userID string) (Account, error) {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO kredi.hesap (kullanici_id) VALUES ($1::text::uuid) ON CONFLICT DO NOTHING`, userID); err != nil {
		return Account{}, fmt.Errorf("could not ensure the credit account: %w", err)
	}
	a, err := scanAccount(s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM kredi.hesap WHERE kullanici_id = $1::text::uuid`, userID))
	if err != nil {
		return Account{}, fmt.Errorf("could not read the credit account: %w", err)
	}
	return a, nil
}

// SpendAction charges the first request of an action and lets follow-ups
// (up to maxUses requests within window) through free. A refunded or
// dropped action, an elapsed window or an exhausted budget gives
// ErrActionExpired. The first request debits the lots like Debit (tur soz,
// ref "<user>:<action>"); a balance below amount gives *InsufficientError.
func (s *PostgresStore) SpendAction(ctx context.Context, userID string, op Operation, actionID string, amount int, window time.Duration, maxUses int, now time.Time) (ActionCharge, error) {
	ref := userID + ":" + actionID
	var out ActionCharge
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAccount(ctx, tx, userID, true); err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		var uses int
		var elapsed bool
		err := tx.QueryRow(ctx, `
			SELECT kullanim, olusturuldu + make_interval(secs => $2) < now()
			FROM kredi.eylem WHERE ref = $1`, ref, window.Seconds()).Scan(&uses, &elapsed)
		switch {
		case err == nil:
			if elapsed || uses >= maxUses {
				return ErrActionExpired
			}
			if _, err := tx.Exec(ctx, `UPDATE kredi.eylem SET kullanim = kullanim + 1 WHERE ref = $1`, ref); err != nil {
				return fmt.Errorf("could not count the action request: %w", err)
			}
			bal, err := balanceOf(ctx, tx, userID, now)
			if err != nil {
				return err
			}
			out = ActionCharge{Balance: bal}
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("could not read the action: %w", err)
		}
		exists, err := movementDone(ctx, tx, userID, turSoz, ref)
		if err != nil {
			return err
		}
		if exists {
			// The charge exists but the action row is gone: refunded or dropped.
			return ErrActionExpired
		}
		bal, debt, short, err := spendTx(ctx, tx, movement{userID: userID, tur: turSoz, ref: ref, amount: amount, reason: string(op)}, now)
		if err != nil {
			return err
		}
		if short {
			return &InsufficientError{Required: amount, Balance: bal, Debt: debt}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kredi.eylem (ref, kullanici_id, tur, kullanim) VALUES ($1, $2::text::uuid, $3, 1)`,
			ref, userID, string(op)); err != nil {
			return fmt.Errorf("could not record the action: %w", err)
		}
		out = ActionCharge{Balance: bal, Charged: true}
		return nil
	})
	if err != nil {
		return ActionCharge{}, err
	}
	return out, nil
}

// DropAction closes an action so later requests with its id expire.
func (s *PostgresStore) DropAction(ctx context.Context, ref string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM kredi.eylem WHERE ref = $1`, ref); err != nil {
		return fmt.Errorf("could not drop the action: %w", err)
	}
	return nil
}

// historyCursor is the decoded form of a history cursor: the position of
// the last movement of the previous page in (olusturuldu DESC, id DESC).
type historyCursor struct {
	T  time.Time `json:"t"`
	ID int64     `json:"id"`
}

func encodeCursor(m Movement) string {
	raw, _ := json.Marshal(historyCursor{T: m.CreatedAt.UTC(), ID: m.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (historyCursor, error) {
	var c historyCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.ID <= 0 || c.T.IsZero() {
		return historyCursor{}, ErrBadCursor
	}
	return c, nil
}

// HistoryPageSize is the number of movements per history page.
const HistoryPageSize = 20

// History returns one page of the user's movements, newest first. cursor is
// the Next of the previous page (empty = first page, a damaged one gives
// ErrBadCursor); filter narrows to spends or earnings.
func (s *PostgresStore) History(ctx context.Context, userID, cursor string, filter HistoryFilter) (Page, error) {
	var cur *historyCursor
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		cur = &c
	}
	cond := ""
	switch filter {
	case FilterAll:
	case FilterSpend:
		cond = " AND tutar < 0"
	case FilterEarn:
		cond = " AND tutar > 0"
	default:
		return Page{}, ErrBadFilter
	}
	var curT *time.Time
	var curID int64
	if cur != nil {
		curT, curID = &cur.T, cur.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tur, ref, sebep, tutar, proje_id::text, kademe, olusturuldu
		FROM kredi.hareket
		WHERE kullanici_id = $1::text::uuid`+cond+`
		  AND ($2::timestamptz IS NULL OR (olusturuldu, id) < ($2::timestamptz, $3::bigint))
		ORDER BY olusturuldu DESC, id DESC LIMIT $4`, userID, curT, curID, HistoryPageSize+1)
	if err != nil {
		return Page{}, fmt.Errorf("could not read the credit history: %w", err)
	}
	defer rows.Close()
	var page Page
	for rows.Next() {
		var m Movement
		if err := rows.Scan(&m.ID, &m.Tur, &m.Ref, &m.Reason, &m.Amount, &m.ProjectID, &m.Tier, &m.CreatedAt); err != nil {
			return Page{}, fmt.Errorf("could not read a credit movement: %w", err)
		}
		page.Items = append(page.Items, m)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("could not read the credit history: %w", err)
	}
	if len(page.Items) > HistoryPageSize {
		page.Items = page.Items[:HistoryPageSize]
		page.Next = encodeCursor(page.Items[len(page.Items)-1])
	}
	return page, nil
}

// DeleteUserData removes the user's account, lots, movements and actions and
// anonymises their subscriptions and purchases.
func (s *PostgresStore) DeleteUserData(ctx context.Context, userID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// Lock the account first, like every other write, so a concurrent
		// spend or refund cannot deadlock with this or slip rows in.
		if _, err := tx.Exec(ctx,
			`SELECT 1 FROM kredi.hesap WHERE kullanici_id = $1::text::uuid FOR UPDATE`, userID); err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		for _, q := range []string{
			`DELETE FROM kredi.eylem WHERE kullanici_id = $1::text::uuid`,
			`DELETE FROM kredi.dilim_hareket WHERE hareket_id IN
				(SELECT id FROM kredi.hareket WHERE kullanici_id = $1::text::uuid)`,
			`DELETE FROM kredi.dilim WHERE kullanici_id = $1::text::uuid`,
			`DELETE FROM kredi.hareket WHERE kullanici_id = $1::text::uuid`,
			`UPDATE kredi.kampanya_katilim SET kullanici_id = NULL, dilim_id = NULL WHERE kullanici_id = $1::text::uuid`,
			`DELETE FROM kredi.hesap WHERE kullanici_id = $1::text::uuid`,
			`UPDATE kredi.abonelik SET kullanici_id = NULL WHERE kullanici_id = $1::text::uuid`,
			`UPDATE kredi.satin_alma SET kullanici_id = NULL WHERE kullanici_id = $1::text::uuid`,
		} {
			if _, err := tx.Exec(ctx, q, userID); err != nil {
				return fmt.Errorf("could not delete the user's credit data: %w", err)
			}
		}
		return nil
	})
}

// ReadAccount returns the user's account without creating it. The bool is
// false when the user has no account row.
func (s *PostgresStore) ReadAccount(ctx context.Context, userID string) (Account, bool, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM kredi.hesap WHERE kullanici_id = $1::text::uuid`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, fmt.Errorf("could not read the credit account: %w", err)
	}
	return a, true, nil
}

// Subscription is one store subscription row (kredi.abonelik).
type Subscription struct {
	Store        string
	OriginalID   string
	UserID       *string
	Product      string
	Status       string
	PeriodEnd    time.Time
	AutoRenew    bool
	NextRefill   *time.Time
	RefillAnchor *time.Time
	RefillCount  int
	Env          string
	// NextProduct is the internal product that starts at the next renewal
	// after a downgrade (sonraki_urun), or nil.
	NextProduct *string
	// GraceEnd is the end of the store's grace period (ek_sure_bitis) while
	// the status is ek_sure and the store told it, else nil.
	GraceEnd *time.Time
}

// LatestSubscription returns the user's subscription with the latest period
// end, or nil when the user has none.
func (s *PostgresStore) LatestSubscription(ctx context.Context, userID string) (*Subscription, error) {
	var sub Subscription
	var periodEnd *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT `+subscriptionColumns+`
		FROM kredi.abonelik WHERE kullanici_id = $1::text::uuid
		ORDER BY donem_sonu DESC NULLS LAST, orijinal_islem_id LIMIT 1`, userID).
		Scan(&sub.Store, &sub.OriginalID, &sub.UserID, &sub.Product, &sub.Status, &periodEnd,
			&sub.AutoRenew, &sub.NextRefill, &sub.RefillAnchor, &sub.RefillCount, &sub.Env, &sub.NextProduct, &sub.GraceEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the subscription: %w", err)
	}
	if periodEnd != nil {
		sub.PeriodEnd = *periodEnd
	}
	return &sub, nil
}
