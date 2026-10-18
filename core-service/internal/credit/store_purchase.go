package credit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Purchase record statuses (kredi.satin_alma.durum).
const (
	// purchaseProcessing: an owner is set, the credit is being written.
	purchaseProcessing = "isleniyor"
	// purchaseCredited: the credit was written to the owner.
	purchaseCredited = "kredilendi"
	// purchaseOwnerless: recorded without an owner; no credit yet (6-G15).
	purchaseOwnerless = "sahipsiz"
	// purchaseRecorded: an owned subscription transaction that gave no
	// credit (expired, grace, billing issue).
	purchaseRecorded = "kaydedildi"
	// purchaseRefunded: the store refunded or revoked it. Terminal.
	purchaseRefunded = "iade"
)

// PurchaseRecord is one audit row of kredi.satin_alma.
type PurchaseRecord struct {
	Store   string
	OrderID string
	UserID  *string
	Product string
	Credits int
	Status  string
	Env     string
	Raw     json.RawMessage
}

// PurchaseStore is the store surface Purchases needs.
type PurchaseStore interface {
	Ledger
	AccountByStoreToken(ctx context.Context, token uuid.UUID) (*Account, error)
	SubscriptionByOriginal(ctx context.Context, store, originalID string) (*Subscription, error)
	UpsertSubscription(ctx context.Context, s Subscription) error
	RecordPurchase(ctx context.Context, r PurchaseRecord) (inserted bool, err error)
	PurchaseByOrder(ctx context.Context, store, id string) (*PurchaseRecord, error)
	DueRefills(ctx context.Context, now time.Time, limit int) ([]Subscription, error)
	GrantPlan(ctx context.Context, userID, ref string, amount int, tier Tier, end, now time.Time) error
	RefillStep(ctx context.Context, sub Subscription, now time.Time, ref string, amount int, tier Tier, next time.Time) (bool, error)
	OverdueSubscriptions(ctx context.Context, before time.Time, limit int) ([]Subscription, error)
	PruneActions(ctx context.Context, before time.Time) (int64, error)
}

var _ PurchaseStore = (*PostgresStore)(nil)

// AccountByStoreToken returns the living account holding token, or nil.
func (s *PostgresStore) AccountByStoreToken(ctx context.Context, token uuid.UUID) (*Account, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM kredi.hesap WHERE magaza_token = $1`, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the account by store token: %w", err)
	}
	return &a, nil
}

// SubscriptionByOriginal returns the subscription row, or nil.
func (s *PostgresStore) SubscriptionByOriginal(ctx context.Context, store, originalID string) (*Subscription, error) {
	var sub Subscription
	var periodEnd *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT `+subscriptionColumns+`
		FROM kredi.abonelik WHERE magaza = $1 AND orijinal_islem_id = $2`, store, originalID).
		Scan(&sub.Store, &sub.OriginalID, &sub.UserID, &sub.Product, &sub.Status, &periodEnd,
			&sub.AutoRenew, &sub.NextRefill, &sub.RefillAnchor, &sub.RefillCount, &sub.Env, &sub.NextProduct)
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

// UpsertSubscription writes the subscription row. An existing owner is
// never replaced (only account deletion clears it), so an ownerless write
// cannot detach a subscription from its owner.
func (s *PostgresStore) UpsertSubscription(ctx context.Context, sub Subscription) error {
	var periodEnd *time.Time
	if !sub.PeriodEnd.IsZero() {
		periodEnd = &sub.PeriodEnd
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO kredi.abonelik (kullanici_id, magaza, orijinal_islem_id, urun, durum, donem_sonu,
		                            otomatik_yenile, sonraki_dolum, dolum_capasi, dolum_sayisi, ortam, sonraki_urun)
		VALUES ($1::text::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (magaza, orijinal_islem_id) DO UPDATE SET
			kullanici_id    = COALESCE(kredi.abonelik.kullanici_id, EXCLUDED.kullanici_id),
			urun            = EXCLUDED.urun,
			durum           = EXCLUDED.durum,
			donem_sonu      = EXCLUDED.donem_sonu,
			otomatik_yenile = EXCLUDED.otomatik_yenile,
			sonraki_dolum   = EXCLUDED.sonraki_dolum,
			dolum_capasi    = EXCLUDED.dolum_capasi,
			dolum_sayisi    = EXCLUDED.dolum_sayisi,
			ortam           = EXCLUDED.ortam,
			sonraki_urun    = EXCLUDED.sonraki_urun`,
		sub.UserID, sub.Store, sub.OriginalID, sub.Product, sub.Status, periodEnd,
		sub.AutoRenew, sub.NextRefill, sub.RefillAnchor, sub.RefillCount, sub.Env, sub.NextProduct)
	if err != nil {
		return fmt.Errorf("could not write the subscription: %w", err)
	}
	return nil
}

// RecordPurchase inserts or updates the audit row of a purchase. An
// existing owner is never replaced, a refunded row stays refunded and a
// credited row only moves on to refunded. inserted is true for a new row.
func (s *PostgresStore) RecordPurchase(ctx context.Context, r PurchaseRecord) (bool, error) {
	var raw any
	if len(r.Raw) > 0 {
		raw = string(r.Raw)
	}
	var inserted bool
	err := s.pool.QueryRow(ctx, `
		INSERT INTO kredi.satin_alma (magaza, islem_id, kullanici_id, urun, kredi, durum, ortam, ham_yuk)
		VALUES ($1, $2, $3::text::uuid, $4, $5, $6, $7, $8::jsonb)
		ON CONFLICT (magaza, islem_id) DO UPDATE SET
			kullanici_id = COALESCE(kredi.satin_alma.kullanici_id, EXCLUDED.kullanici_id),
			durum = CASE
				WHEN kredi.satin_alma.durum = 'iade' THEN 'iade'
				WHEN kredi.satin_alma.durum = 'kredilendi' AND EXCLUDED.durum <> 'iade' THEN 'kredilendi'
				ELSE EXCLUDED.durum END,
			ortam = EXCLUDED.ortam,
			ham_yuk = COALESCE(EXCLUDED.ham_yuk, kredi.satin_alma.ham_yuk)
		RETURNING xmax = 0`,
		r.Store, r.OrderID, r.UserID, r.Product, r.Credits, r.Status, r.Env, raw).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("could not record the purchase: %w", err)
	}
	return inserted, nil
}

// PurchaseByOrder returns the audit row of a store transaction or order
// id, or nil.
func (s *PostgresStore) PurchaseByOrder(ctx context.Context, store, id string) (*PurchaseRecord, error) {
	var r PurchaseRecord
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT magaza, islem_id, kullanici_id::text, urun, kredi, durum, ortam, ham_yuk
		FROM kredi.satin_alma WHERE magaza = $1 AND islem_id = $2`, store, id).
		Scan(&r.Store, &r.OrderID, &r.UserID, &r.Product, &r.Credits, &r.Status, &r.Env, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the purchase: %w", err)
	}
	r.Raw = raw
	return &r, nil
}

const subscriptionColumns = `magaza, orijinal_islem_id, kullanici_id::text, urun, durum, donem_sonu,
	otomatik_yenile, sonraki_dolum, dolum_capasi, dolum_sayisi, ortam, sonraki_urun`

func (s *PostgresStore) querySubscriptions(ctx context.Context, where string, args ...any) ([]Subscription, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+subscriptionColumns+` FROM kredi.abonelik WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("could not list subscriptions: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var sub Subscription
		var periodEnd *time.Time
		if err := rows.Scan(&sub.Store, &sub.OriginalID, &sub.UserID, &sub.Product, &sub.Status, &periodEnd,
			&sub.AutoRenew, &sub.NextRefill, &sub.RefillAnchor, &sub.RefillCount, &sub.Env, &sub.NextProduct); err != nil {
			return nil, fmt.Errorf("could not read a subscription: %w", err)
		}
		if periodEnd != nil {
			sub.PeriodEnd = *periodEnd
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not list subscriptions: %w", err)
	}
	return out, nil
}

// DueRefills lists owned, active yearly subscriptions whose next refill
// is due, with the period not yet ended and the refill inside it. At most
// limit rows are returned, oldest due first.
func (s *PostgresStore) DueRefills(ctx context.Context, now time.Time, limit int) ([]Subscription, error) {
	return s.querySubscriptions(ctx, `
		durum = 'aktif' AND urun LIKE '%.yearly' AND kullanici_id IS NOT NULL
		AND dolum_capasi IS NOT NULL AND sonraki_dolum <= $1
		AND (donem_sonu IS NULL OR ($1 < donem_sonu AND sonraki_dolum < donem_sonu))
		ORDER BY sonraki_dolum LIMIT $2`, now, limit)
}

// RefillStep takes one refill step in a single transaction: it locks the
// account, then the subscription row, and only if the row is still active
// with the schedule of sub and now is inside the CURRENT period, it grants the
// plan lot (ending the previous one, then a new lot of amount valid to the
// current period end, under ref, idempotent) and moves the schedule to next. It reports whether the step was taken; a changed
// row (a concurrent renewal, refill or ending) is not an error.
func (s *PostgresStore) RefillStep(ctx context.Context, sub Subscription, now time.Time, ref string, amount int, tier Tier, next time.Time) (bool, error) {
	if sub.UserID == nil || sub.NextRefill == nil {
		return false, nil
	}
	var taken bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		_, err := lockAccount(ctx, tx, *sub.UserID, false)
		if err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		var status string
		var count int
		var due, periodEnd *time.Time
		err = tx.QueryRow(ctx, `
			SELECT durum, dolum_sayisi, sonraki_dolum, donem_sonu FROM kredi.abonelik
			WHERE magaza = $1 AND orijinal_islem_id = $2 AND kullanici_id = $3::text::uuid FOR UPDATE`,
			sub.Store, sub.OriginalID, *sub.UserID).Scan(&status, &count, &due, &periodEnd)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("could not lock the subscription: %w", err)
		}
		if status != subActive || count != sub.RefillCount || due == nil || !due.Equal(*sub.NextRefill) ||
			(periodEnd != nil && (!now.Before(*periodEnd) || !due.Before(*periodEnd))) {
			return nil
		}
		if periodEnd == nil {
			return nil
		}
		if err := endLivePlan(ctx, tx, *sub.UserID, ref, now); err != nil {
			return err
		}
		// The lot lasts until the next monthly refill, at most the period end.
		lotEnd := *periodEnd
		if next.Before(lotEnd) {
			lotEnd = next
		}
		if _, err := grantTx(ctx, tx, *sub.UserID, turPlanDolum, ref,
			Grant{Source: LotPlan, Amount: amount, ExpiresAt: &lotEnd, Tier: tier}, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE kredi.abonelik SET dolum_sayisi = dolum_sayisi + 1, sonraki_dolum = $3
			WHERE magaza = $1 AND orijinal_islem_id = $2`, sub.Store, sub.OriginalID, next); err != nil {
			return fmt.Errorf("could not advance the refill: %w", err)
		}
		taken = true
		return nil
	})
	return taken, err
}

// GrantPlan starts a plan period: it ends the user's live plan lot (P3, if
// any) and grants a new plan lot of amount valid until end, both under ref
// and in one transaction. A repeated ref changes nothing.
func (s *PostgresStore) GrantPlan(ctx context.Context, userID, ref string, amount int, tier Tier, end, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAccount(ctx, tx, userID, true); err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		if done, err := movementDone(ctx, tx, userID, turPlanDolum, ref); err != nil || done {
			return err
		}
		if err := endLivePlan(ctx, tx, userID, ref, now); err != nil {
			return err
		}
		_, err := grantTx(ctx, tx, userID, turPlanDolum, ref,
			Grant{Source: LotPlan, Amount: amount, ExpiresAt: &end, Tier: tier}, now)
		return err
	})
}

// OverdueSubscriptions lists subscriptions that are not ended or refunded
// and whose paid period ended before the given time, oldest first, at most
// limit rows.
func (s *PostgresStore) OverdueSubscriptions(ctx context.Context, before time.Time, limit int) ([]Subscription, error) {
	return s.querySubscriptions(ctx, `
		durum NOT IN ('bitti', 'iade') AND donem_sonu < $1 ORDER BY donem_sonu LIMIT $2`, before, limit)
}

// PruneActions deletes spend-action rows created before the given time.
func (s *PostgresStore) PruneActions(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM kredi.eylem WHERE olusturuldu < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("could not prune actions: %w", err)
	}
	return tag.RowsAffected(), nil
}
