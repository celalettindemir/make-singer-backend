package credit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ledger movement kinds (kredi.hareket.tur).
const (
	turSoz        = "soz"
	turHediye     = "hediye"
	turPlanDolum  = "plan_dolum"
	turPlanBitis  = "plan_bitis"
	turMagazaIade = "magaza_iade"
)

// ErrActionExpired is returned by SpendAction when the action's window or
// request budget is used up, or when the action was refunded or dropped.
var ErrActionExpired = errors.New("credit: action expired")

// InsufficientError reports a debit the balance cannot cover.
type InsufficientError struct {
	Required int
	Balance  int
}

func (e *InsufficientError) Error() string {
	return fmt.Sprintf("credit: insufficient balance (required %d, balance %d)", e.Required, e.Balance)
}

// Account is a user's credit account.
type Account struct {
	UserID         string
	Plan           int
	Permanent      int
	PlanPeriodEnd  *time.Time
	StoreToken     uuid.UUID
	WelcomeClaimed bool
}

// Balance is the total spendable balance.
func (a Account) Balance() int { return a.Plan + a.Permanent }

// ActionCharge is the outcome of SpendAction.
type ActionCharge struct {
	Account Account
	Charged bool
}

// Movement is one ledger row as shown in the history.
type Movement struct {
	ID        int64
	Tur       string
	Ref       string
	Reason    string
	Amount    int
	CreatedAt time.Time
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

const accountColumns = `kullanici_id::text, plan_bakiye, kalici_bakiye, plan_donem_sonu, magaza_token, hediye_alindi`

// lockAccount reads and row-locks the account; with create it inserts the
// row first when missing. A missing row without create yields pgx.ErrNoRows.
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
	if err := row.Scan(&a.UserID, &a.Plan, &a.Permanent, &a.PlanPeriodEnd, &a.StoreToken, &a.WelcomeClaimed); err != nil {
		return Account{}, err
	}
	return a, nil
}

// movementExists reports whether (tur, ref) is already in the ledger.
func movementExists(ctx context.Context, tx pgx.Tx, tur, ref string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM kredi.hareket WHERE tur = $1 AND ref = $2)`, tur, ref).Scan(&exists)
	return exists, err
}

// apply writes a ledger row and moves the buckets. It returns false (and
// changes nothing) when (tur, ref) already exists. The caller holds the
// account lock.
func apply(ctx context.Context, tx pgx.Tx, acct Account, tur, ref, reason string, planDelta, permDelta int) (Account, bool, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO kredi.hareket (kullanici_id, tur, ref, plan_delta, kalici_delta, sebep)
		VALUES ($1::text::uuid, $2, $3, $4, $5, $6)
		ON CONFLICT (tur, ref) DO NOTHING RETURNING id`,
		acct.UserID, tur, ref, planDelta, permDelta, reason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return acct, false, nil
	}
	if err != nil {
		return Account{}, false, fmt.Errorf("could not write the ledger movement: %w", err)
	}
	acct.Plan += planDelta
	acct.Permanent += permDelta
	if _, err := tx.Exec(ctx, `
		UPDATE kredi.hesap SET plan_bakiye = $2, kalici_bakiye = $3, guncellendi = now()
		WHERE kullanici_id = $1::text::uuid`, acct.UserID, acct.Plan, acct.Permanent); err != nil {
		return Account{}, false, fmt.Errorf("could not update the credit account: %w", err)
	}
	return acct, true, nil
}

// split divides amount between the buckets: plan first, the rest from the
// permanent bucket (which may go negative).
func split(a Account, amount int) (fromPlan, fromPerm int) {
	fromPlan = min(max(a.Plan, 0), amount)
	return fromPlan, amount - fromPlan
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

// debitTx runs the debit inside tx. The caller holds the account lock.
func debitTx(ctx context.Context, tx pgx.Tx, acct Account, tur, ref, reason string, amount int, allowNegative bool) (Account, bool, error) {
	exists, err := movementExists(ctx, tx, tur, ref)
	if err != nil {
		return Account{}, false, fmt.Errorf("could not check the ledger: %w", err)
	}
	if exists {
		return acct, false, nil
	}
	if !allowNegative && acct.Balance() < amount {
		return Account{}, false, &InsufficientError{Required: amount, Balance: acct.Balance()}
	}
	fromPlan, fromPerm := split(acct, amount)
	return apply(ctx, tx, acct, tur, ref, reason, -fromPlan, -fromPerm)
}

// Debit takes amount from the plan bucket first, then the permanent bucket.
// The bool is false when (tur, ref) already exists.
func (s *PostgresStore) Debit(ctx context.Context, userID string, tur, ref, reason string, amount int, allowNegative bool) (Account, bool, error) {
	var acct Account
	var done bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := lockAccount(ctx, tx, userID, true)
		if err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		acct, done, err = debitTx(ctx, tx, a, tur, ref, reason, amount, allowNegative)
		return err
	})
	return acct, done, err
}

// Refund reverses the debit (debitTur, ref) with a refundTur movement of the
// negated deltas. It is a no-op when the debit is missing (for example after
// the account was deleted) or the refund already exists, and it never
// creates an account.
func (s *PostgresStore) Refund(ctx context.Context, debitTur, refundTur, ref string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var userID, reason string
		var planDelta, permDelta int
		err := tx.QueryRow(ctx, `
			SELECT kullanici_id::text, plan_delta, kalici_delta, sebep
			FROM kredi.hareket WHERE tur = $1 AND ref = $2`, debitTur, ref).
			Scan(&userID, &planDelta, &permDelta, &reason)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("could not read the debit: %w", err)
		}
		acct, err := lockAccount(ctx, tx, userID, false)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		if _, _, err := apply(ctx, tx, acct, refundTur, ref, reason, -planDelta, -permDelta); err != nil {
			return err
		}
		if debitTur == turSoz {
			if _, err := tx.Exec(ctx, `DELETE FROM kredi.eylem WHERE ref = $1`, ref); err != nil {
				return fmt.Errorf("could not close the action: %w", err)
			}
		}
		return nil
	})
}

// Credit adds amount to the permanent bucket. The bool is false when
// (tur, ref) already exists.
func (s *PostgresStore) Credit(ctx context.Context, userID, tur, ref, reason string, amount int) (Account, bool, error) {
	var acct Account
	var done bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := lockAccount(ctx, tx, userID, true)
		if err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		acct, done, err = apply(ctx, tx, a, tur, ref, reason, 0, amount)
		if err == nil && done && tur == turHediye {
			_, err = tx.Exec(ctx, `UPDATE kredi.hesap SET hediye_alindi = true WHERE kullanici_id = $1::text::uuid`, userID)
			acct.WelcomeClaimed = true
		}
		return err
	})
	return acct, done, err
}

// SetPlan sets the plan bucket: to amount for plan_dolum, to 0 for
// plan_bitis (P5). It records periodEnd as the plan period end. The bool is
// false when (tur, ref) already exists.
func (s *PostgresStore) SetPlan(ctx context.Context, userID, tur, ref string, amount int, periodEnd *time.Time) (Account, bool, error) {
	if tur != turPlanDolum && tur != turPlanBitis {
		return Account{}, false, fmt.Errorf("credit: SetPlan does not accept tur %q", tur)
	}
	target := amount
	if tur == turPlanBitis {
		target = 0
	}
	var acct Account
	var done bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := lockAccount(ctx, tx, userID, true)
		if err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		acct, done, err = apply(ctx, tx, a, tur, ref, "", target-a.Plan, 0)
		if err != nil || !done {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE kredi.hesap SET plan_donem_sonu = $2 WHERE kullanici_id = $1::text::uuid`, userID, periodEnd); err != nil {
			return fmt.Errorf("could not set the plan period end: %w", err)
		}
		acct.PlanPeriodEnd = periodEnd
		return nil
	})
	return acct, done, err
}

// ClawBack removes amount after a store refund: plan bucket first, then the
// permanent bucket, which may go negative. The bool is false when the
// refund was already applied.
func (s *PostgresStore) ClawBack(ctx context.Context, userID, ref string, amount int) (Account, bool, error) {
	var acct Account
	var done bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := lockAccount(ctx, tx, userID, true)
		if err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		fromPlan, fromPerm := split(a, amount)
		acct, done, err = apply(ctx, tx, a, turMagazaIade, ref, "", -fromPlan, -fromPerm)
		return err
	})
	return acct, done, err
}

// SpendAction charges the first request of an action and lets follow-ups
// (up to maxUses requests within window) through free. A refunded or
// dropped action, an elapsed window or an exhausted budget gives
// ErrActionExpired.
func (s *PostgresStore) SpendAction(ctx context.Context, userID string, op Operation, actionID string, amount int, window time.Duration, maxUses int, allowNegative bool) (ActionCharge, error) {
	ref := userID + ":" + actionID
	var out ActionCharge
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		acct, err := lockAccount(ctx, tx, userID, true)
		if err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		var uses int
		var elapsed bool
		err = tx.QueryRow(ctx, `
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
			out = ActionCharge{Account: acct}
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("could not read the action: %w", err)
		}
		updated, charged, err := debitTx(ctx, tx, acct, turSoz, ref, string(op), amount, allowNegative)
		if err != nil {
			return err
		}
		if !charged {
			// The charge exists but the action row is gone: refunded or dropped.
			return ErrActionExpired
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO kredi.eylem (ref, kullanici_id, tur, kullanim) VALUES ($1, $2::text::uuid, $3, 1)`,
			ref, userID, string(op)); err != nil {
			return fmt.Errorf("could not record the action: %w", err)
		}
		out = ActionCharge{Account: updated, Charged: true}
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

// History returns the user's movements, newest first, with id below
// beforeID (all when beforeID <= 0).
func (s *PostgresStore) History(ctx context.Context, userID string, beforeID int64, limit int) ([]Movement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tur, ref, sebep, plan_delta + kalici_delta, olusturuldu
		FROM kredi.hareket
		WHERE kullanici_id = $1::text::uuid AND ($2::bigint <= 0 OR id < $2)
		ORDER BY id DESC LIMIT $3`, userID, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("could not read the credit history: %w", err)
	}
	defer rows.Close()
	var out []Movement
	for rows.Next() {
		var m Movement
		if err := rows.Scan(&m.ID, &m.Tur, &m.Ref, &m.Reason, &m.Amount, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("could not read a credit movement: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteUserData removes the user's account, movements and actions and
// anonymises their subscriptions and purchases.
func (s *PostgresStore) DeleteUserData(ctx context.Context, userID string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`DELETE FROM kredi.eylem WHERE kullanici_id = $1::text::uuid`,
			`DELETE FROM kredi.hareket WHERE kullanici_id = $1::text::uuid`,
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
}

// LatestSubscription returns the user's subscription with the latest period
// end, or nil when the user has none.
func (s *PostgresStore) LatestSubscription(ctx context.Context, userID string) (*Subscription, error) {
	var sub Subscription
	var periodEnd *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT magaza, orijinal_islem_id, kullanici_id::text, urun, durum, donem_sonu,
		       otomatik_yenile, sonraki_dolum, dolum_capasi, dolum_sayisi, ortam
		FROM kredi.abonelik WHERE kullanici_id = $1::text::uuid
		ORDER BY donem_sonu DESC NULLS LAST, orijinal_islem_id LIMIT 1`, userID).
		Scan(&sub.Store, &sub.OriginalID, &sub.UserID, &sub.Product, &sub.Status, &periodEnd,
			&sub.AutoRenew, &sub.NextRefill, &sub.RefillAnchor, &sub.RefillCount, &sub.Env)
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
