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
		SELECT magaza, orijinal_islem_id, kullanici_id::text, urun, durum, donem_sonu,
		       otomatik_yenile, sonraki_dolum, dolum_capasi, dolum_sayisi, ortam
		FROM kredi.abonelik WHERE magaza = $1 AND orijinal_islem_id = $2`, store, originalID).
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
		                            otomatik_yenile, sonraki_dolum, dolum_capasi, dolum_sayisi, ortam)
		VALUES ($1::text::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (magaza, orijinal_islem_id) DO UPDATE SET
			kullanici_id    = COALESCE(kredi.abonelik.kullanici_id, EXCLUDED.kullanici_id),
			urun            = EXCLUDED.urun,
			durum           = EXCLUDED.durum,
			donem_sonu      = EXCLUDED.donem_sonu,
			otomatik_yenile = EXCLUDED.otomatik_yenile,
			sonraki_dolum   = EXCLUDED.sonraki_dolum,
			dolum_capasi    = EXCLUDED.dolum_capasi,
			dolum_sayisi    = EXCLUDED.dolum_sayisi,
			ortam           = EXCLUDED.ortam`,
		sub.UserID, sub.Store, sub.OriginalID, sub.Product, sub.Status, periodEnd,
		sub.AutoRenew, sub.NextRefill, sub.RefillAnchor, sub.RefillCount, sub.Env)
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
