package credit

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Lot movement kinds written by the lot primitives (kredi.hareket.tur).
const (
	turSoz          = "soz"
	turPlanDolum    = "plan_dolum"
	turPlanBitis    = "plan_bitis"
	turMagazaIade   = "magaza_iade"
	turYonetici     = "yonetici"
	turPlanSure     = "plan_sure"
	turKampanyaSure = "kampanya_sure"
	turYoneticiSure = "yonetici_sure"
)

// expiryTur maps a lot source to its expiry movement kind. Packs never
// expire.
var expiryTur = map[LotSource]string{
	LotPlan:     turPlanSure,
	LotCampaign: turKampanyaSure,
	LotAdmin:    turYoneticiSure,
}

// ErrBalanceTooLow is returned by DeductAdmin when the deduction would take
// the balance below zero.
var ErrBalanceTooLow = errors.New("credit: balance too low")

// ErrLotNotFound is returned by ClawBackPack when the user has no lot for
// the pack.
var ErrLotNotFound = errors.New("credit: lot not found")

// Grant describes a credit entry: one new lot.
type Grant struct {
	Source     LotSource
	Amount     int
	ExpiresAt  *time.Time
	By, Note   string
	CampaignID *int64
	// Tier is the plan tier of a plan lot (recorded on plan_dolum).
	Tier Tier
}

// Lot is one live, non-empty lot as shown to the user.
type Lot struct {
	Source    LotSource
	Amount    int
	ExpiresAt *time.Time
	// Refund marks a grace lot a refund created (P1). It is never the plan
	// lot of P3 and never a campaign gift.
	Refund bool
}

// liveLot is the predicate of a lot that counts: not swept or ended, and
// not past its end at $2 (the caller's now). $1 is the user id.
const liveLot = `kullanici_id = $1::text::uuid AND NOT sure_islendi AND (bitis IS NULL OR bitis > $2)`

// spendOrder is 6v2 §2.2: soonest end first, no end last; ties plan,
// kampanya, yonetici, paket; then lot id.
const spendOrder = `ORDER BY bitis ASC NULLS LAST,
	CASE kaynak WHEN 'plan' THEN 0 WHEN 'kampanya' THEN 1 WHEN 'yonetici' THEN 2 ELSE 3 END,
	id`

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// balanceOf is SUM(kalan) of live lots minus the debt. It never creates an
// account.
func balanceOf(ctx context.Context, q queryRower, userID string, now time.Time) (int, error) {
	var bal int
	err := q.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(kalan) FROM kredi.dilim WHERE `+liveLot+`), 0)
		     - COALESCE((SELECT borc FROM kredi.hesap WHERE kullanici_id = $1::text::uuid), 0)`,
		userID, now).Scan(&bal)
	if err != nil {
		return 0, fmt.Errorf("could not read the credit balance: %w", err)
	}
	return bal, nil
}

// movementDone reports whether (tur, ref) is already in the ledger. A row of
// another user is an error: refs are never shared between users.
func movementDone(ctx context.Context, tx pgx.Tx, userID, tur, ref string) (bool, error) {
	var owner string
	err := tx.QueryRow(ctx,
		`SELECT kullanici_id::text FROM kredi.hareket WHERE tur = $1 AND ref = $2`, tur, ref).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not check the ledger: %w", err)
	}
	if owner != userID {
		return false, fmt.Errorf("credit: movement %s/%s belongs to another user", tur, ref)
	}
	return true, nil
}

type movement struct {
	userID, tur, ref string
	amount           int
	by, note         string
	campaignID       *int64
	reason           string
	projectID        string
	tier             Tier
	// allowDebt lets the spend through while the account owes debt (admin
	// deductions only).
	allowDebt bool
}

// Meta is the extra data a debit records on its movement: the operation
// (sebep, shown as the history operation) and the project it was spent on.
type Meta struct {
	Reason    string
	ProjectID string
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// insertMovement writes the ledger row; tutar is the signed balance change.
func insertMovement(ctx context.Context, tx pgx.Tx, m movement) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO kredi.hareket (kullanici_id, tur, ref, tutar, yapan, notu, kampanya_id, sebep, proje_id, kademe)
		VALUES ($1::text::uuid, $2, $3, $4, $5, $6, $7, $8, $9::text::uuid, $10) RETURNING id`,
		m.userID, m.tur, m.ref, m.amount, nullText(m.by), nullText(m.note), m.campaignID,
		m.reason, nullText(m.projectID), nullText(string(m.tier))).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("could not write the ledger movement: %w", err)
	}
	return id, nil
}

// lotDelta changes a lot's remainder and records it against the movement.
func lotDelta(ctx context.Context, tx pgx.Tx, movementID, lotID int64, delta int) error {
	if _, err := tx.Exec(ctx,
		`UPDATE kredi.dilim SET kalan = kalan + $2 WHERE id = $1`, lotID, delta); err != nil {
		return fmt.Errorf("could not update the lot: %w", err)
	}
	return recordLotDelta(ctx, tx, movementID, lotID, delta)
}

func recordLotDelta(ctx context.Context, tx pgx.Tx, movementID, lotID int64, delta int) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO kredi.dilim_hareket (hareket_id, dilim_id, delta) VALUES ($1, $2, $3)`,
		movementID, lotID, delta); err != nil {
		return fmt.Errorf("could not record the lot movement: %w", err)
	}
	return nil
}

// spendLots takes amount from the live lots in spend order. The caller
// holds the account lock and has checked the balance covers amount.
func spendLots(ctx context.Context, tx pgx.Tx, userID string, movementID int64, amount int, now time.Time) error {
	rows, err := tx.Query(ctx, `
		SELECT id, kalan FROM kredi.dilim
		WHERE `+liveLot+` AND kalan > 0 `+spendOrder+` FOR UPDATE`, userID, now)
	if err != nil {
		return fmt.Errorf("could not read the lots: %w", err)
	}
	type lot struct {
		id     int64
		remain int
	}
	var lots []lot
	for rows.Next() {
		var l lot
		if err := rows.Scan(&l.id, &l.remain); err != nil {
			rows.Close()
			return fmt.Errorf("could not read a lot: %w", err)
		}
		lots = append(lots, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("could not read the lots: %w", err)
	}
	left := amount
	for _, l := range lots {
		if left == 0 {
			break
		}
		take := min(l.remain, left)
		if err := lotDelta(ctx, tx, movementID, l.id, -take); err != nil {
			return err
		}
		left -= take
	}
	if left > 0 {
		return fmt.Errorf("credit: lots short by %d after the balance check", left)
	}
	return nil
}

// spendTx is the shared body of Debit, DeductAdmin and SpendAction. The
// caller holds the account lock. short is true (and nothing is written)
// when the balance does not cover amount.
func spendTx(ctx context.Context, tx pgx.Tx, m movement, now time.Time) (balance int, short bool, err error) {
	if m.amount <= 0 {
		return 0, false, fmt.Errorf("credit: spend amount must be positive, got %d", m.amount)
	}
	bal, err := balanceOf(ctx, tx, m.userID, now)
	if err != nil {
		return 0, false, err
	}
	done, err := movementDone(ctx, tx, m.userID, m.tur, m.ref)
	if err != nil || done {
		return bal, false, err
	}
	if bal < m.amount {
		return bal, true, nil
	}
	if !m.allowDebt {
		// P2 / R-CRD-12: nothing is spent while debt is owed.
		var debt int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT borc FROM kredi.hesap WHERE kullanici_id = $1::text::uuid), 0)`, m.userID).Scan(&debt); err != nil {
			return 0, false, fmt.Errorf("could not read the debt: %w", err)
		}
		if debt > 0 {
			return bal, true, nil
		}
	}
	amount := m.amount
	m.amount = -amount
	id, err := insertMovement(ctx, tx, m)
	if err != nil {
		return 0, false, err
	}
	if err := spendLots(ctx, tx, m.userID, id, amount, now); err != nil {
		return 0, false, err
	}
	return bal - amount, false, nil
}

func (s *PostgresStore) spend(ctx context.Context, m movement, now time.Time) (balance int, short bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAccount(ctx, tx, m.userID, true); err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		var err error
		balance, short, err = spendTx(ctx, tx, m, now)
		return err
	})
	return balance, short, err
}

// Balance returns the spendable balance at now (live lots minus debt). It
// never creates an account.
func (s *PostgresStore) Balance(ctx context.Context, userID string, now time.Time) (int, error) {
	return balanceOf(ctx, s.pool, userID, now)
}

// Debit takes amount from the live lots in spend order under (tur, ref). A
// repeated (tur, ref) returns the current balance and changes nothing. A
// balance (debt counted) below amount gives *InsufficientError.
func (s *PostgresStore) Debit(ctx context.Context, userID, tur, ref string, amount int, meta Meta, now time.Time) (int, error) {
	bal, short, err := s.spend(ctx,
		movement{userID: userID, tur: tur, ref: ref, amount: amount, reason: meta.Reason, projectID: meta.ProjectID}, now)
	if err != nil {
		return 0, err
	}
	if short {
		return 0, &InsufficientError{Required: amount, Balance: bal}
	}
	return bal, nil
}

// DeductAdmin removes amount in spend order as an admin adjustment
// (yonetici). It gives ErrBalanceTooLow when the balance would go below 0.
func (s *PostgresStore) DeductAdmin(ctx context.Context, userID, ref string, amount int, by, note string, now time.Time) (int, error) {
	bal, short, err := s.spend(ctx,
		movement{userID: userID, tur: turYonetici, ref: ref, amount: amount, by: by, note: note, allowDebt: true}, now)
	if err != nil {
		return 0, err
	}
	if short {
		return bal, ErrBalanceTooLow
	}
	return bal, nil
}

// Refund reverses the debit (debitTur, ref) of userID under (refundTur,
// ref): every lot the debit took from gets its part back. A lot that is no
// longer live (expired or ended) is not touched; its part becomes a new lot
// of the same source ending at now+grace, ref "<ref>:<lot id>" (P1).
// refunded is false (nothing changes) when the debit is missing, the
// refund already exists or the user has no account.
func (s *PostgresStore) Refund(ctx context.Context, userID, debitTur, refundTur, ref string, now time.Time, grace time.Duration) (balance int, refunded bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAccount(ctx, tx, userID, false); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		bal, err := balanceOf(ctx, tx, userID, now)
		if err != nil {
			return err
		}
		balance = bal
		var debitID int64
		var reason string
		var projectID *string
		err = tx.QueryRow(ctx, `
			SELECT id, sebep, proje_id::text FROM kredi.hareket
			WHERE tur = $1 AND ref = $2 AND kullanici_id = $3::text::uuid`, debitTur, ref, userID).
			Scan(&debitID, &reason, &projectID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("could not read the debit: %w", err)
		}
		if debitTur == turSoz {
			// A refunded lyrics action is closed, even when the refund was
			// already written.
			if _, err := tx.Exec(ctx, `DELETE FROM kredi.eylem WHERE ref = $1`, ref); err != nil {
				return fmt.Errorf("could not close the action: %w", err)
			}
		}
		done, err := movementDone(ctx, tx, userID, refundTur, ref)
		if err != nil || done {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT d.id, -dh.delta, d.kaynak,
			       d.sure_islendi OR (d.bitis IS NOT NULL AND d.bitis <= $2)
			FROM kredi.dilim_hareket dh JOIN kredi.dilim d ON d.id = dh.dilim_id
			WHERE dh.hareket_id = $1 AND dh.delta < 0
			ORDER BY d.id FOR UPDATE OF d`, debitID, now)
		if err != nil {
			return fmt.Errorf("could not read the debited lots: %w", err)
		}
		type part struct {
			lotID   int64
			amount  int
			source  string
			expired bool
		}
		var parts []part
		total := 0
		for rows.Next() {
			var p part
			if err := rows.Scan(&p.lotID, &p.amount, &p.source, &p.expired); err != nil {
				rows.Close()
				return fmt.Errorf("could not read a debited lot: %w", err)
			}
			parts = append(parts, p)
			total += p.amount
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("could not read the debited lots: %w", err)
		}

		id, err := insertMovement(ctx, tx, movement{
			userID: userID, tur: refundTur, ref: ref, amount: total,
			reason: reason, projectID: deref(projectID),
		})
		if err != nil {
			return err
		}
		for _, p := range parts {
			if !p.expired {
				if err := lotDelta(ctx, tx, id, p.lotID, p.amount); err != nil {
					return err
				}
				continue
			}
			var graceID int64
			if err := tx.QueryRow(ctx, `
				INSERT INTO kredi.dilim (kullanici_id, kaynak, ref, ilk_miktar, kalan, bitis, iade)
				VALUES ($1::text::uuid, $2, $3, $4, $4, $5, true) RETURNING id`,
				userID, p.source, ref+":"+strconv.FormatInt(p.lotID, 10), p.amount, now.Add(grace)).Scan(&graceID); err != nil {
				return fmt.Errorf("could not write the refund lot: %w", err)
			}
			if err := recordLotDelta(ctx, tx, id, graceID, p.amount); err != nil {
				return err
			}
		}
		balance, refunded = bal+total, true
		return nil
	})
	return balance, refunded, err
}

// Grant writes a new lot under (tur, ref), lot ref = ref. Debt is paid
// first (P2): the lot keeps ilk_miktar = Amount and starts with kalan =
// Amount − paid; the dilim_hareket row records that net start. A repeated
// (tur, ref) returns the current balance and changes nothing.
func (s *PostgresStore) Grant(ctx context.Context, userID, tur, ref string, g Grant, now time.Time) (int, error) {
	var balance int
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		balance, err = grantTx(ctx, tx, userID, tur, ref, g, now)
		return err
	})
	return balance, err
}

// grantTx is Grant inside tx. It takes the account lock itself (taking it
// again in the same transaction is harmless).
func grantTx(ctx context.Context, tx pgx.Tx, userID, tur, ref string, g Grant, now time.Time) (int, error) {
	if g.Amount <= 0 {
		return 0, fmt.Errorf("credit: grant amount must be positive, got %d", g.Amount)
	}
	if !g.Source.valid() {
		return 0, fmt.Errorf("credit: unknown lot source %q", g.Source)
	}
	if g.Source == LotPack && g.ExpiresAt != nil {
		return 0, errors.New("credit: pack lots never expire")
	}
	acct, err := lockAccount(ctx, tx, userID, true)
	if err != nil {
		return 0, fmt.Errorf("could not lock the credit account: %w", err)
	}
	bal, err := balanceOf(ctx, tx, userID, now)
	if err != nil {
		return 0, err
	}
	done, err := movementDone(ctx, tx, userID, tur, ref)
	if err != nil || done {
		return bal, err
	}
	id, err := insertMovement(ctx, tx, movement{
		userID: userID, tur: tur, ref: ref, amount: g.Amount,
		by: g.By, note: g.Note, campaignID: g.CampaignID, tier: g.Tier,
	})
	if err != nil {
		return 0, err
	}
	paid := min(acct.Debt, g.Amount)
	if paid > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE kredi.hesap SET borc = borc - $2, guncellendi = now() WHERE kullanici_id = $1::text::uuid`,
			userID, paid); err != nil {
			return 0, fmt.Errorf("could not pay the debt: %w", err)
		}
	}
	var lotID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO kredi.dilim (kullanici_id, kaynak, ref, ilk_miktar, kalan, bitis)
		VALUES ($1::text::uuid, $2, $3, $4, $5, $6) RETURNING id`,
		userID, string(g.Source), ref, g.Amount, g.Amount-paid, g.ExpiresAt).Scan(&lotID); err != nil {
		return 0, fmt.Errorf("could not write the lot: %w", err)
	}
	if err := recordLotDelta(ctx, tx, id, lotID, g.Amount-paid); err != nil {
		return 0, err
	}
	return bal + g.Amount, nil
}

// endLivePlan ends the user's live plan lots under (plan_bitis, ref) when
// there are any; with none it writes nothing, so a first refill leaves no
// empty plan_end row. The caller holds the account lock.
func endLivePlan(ctx context.Context, tx pgx.Tx, userID, ref string, now time.Time) error {
	var live bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM kredi.dilim WHERE `+liveLot+` AND kaynak = 'plan' AND NOT iade)`, userID, now).Scan(&live); err != nil {
		return fmt.Errorf("could not read the plan lots: %w", err)
	}
	if !live {
		return nil
	}
	_, err := endPlanLotsTx(ctx, tx, userID, turPlanBitis, ref, now)
	return err
}

// lotTier is the plan tier recorded on the grant movement of lot lotID, or
// "" when there is none (a refund lot, an old row).
func lotTier(ctx context.Context, tx pgx.Tx, lotID int64) (Tier, error) {
	var tier *string
	err := tx.QueryRow(ctx, `
		SELECT h.kademe FROM kredi.dilim_hareket dh JOIN kredi.hareket h ON h.id = dh.hareket_id
		WHERE dh.dilim_id = $1 AND h.tur = 'plan_dolum' LIMIT 1`, lotID).Scan(&tier)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read the lot tier: %w", err)
	}
	return Tier(deref(tier)), nil
}

// EndPlanLots ends every live plan lot at once under (tur, ref): remainders
// go to zero and the lots are marked done, so neither the sweep nor a
// refund touches them again. The movement is written even when nothing
// remains (it records the end) and carries the tier of the ended lot.
func (s *PostgresStore) EndPlanLots(ctx context.Context, userID, tur, ref string, now time.Time) (int, error) {
	var balance int
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		balance, err = endPlanLotsTx(ctx, tx, userID, tur, ref, now)
		return err
	})
	return balance, err
}

// endPlanLotsTx is EndPlanLots inside tx.
func endPlanLotsTx(ctx context.Context, tx pgx.Tx, userID, tur, ref string, now time.Time) (int, error) {
	if _, err := lockAccount(ctx, tx, userID, true); err != nil {
		return 0, fmt.Errorf("could not lock the credit account: %w", err)
	}
	bal, err := balanceOf(ctx, tx, userID, now)
	if err != nil {
		return 0, err
	}
	done, err := movementDone(ctx, tx, userID, tur, ref)
	if err != nil || done {
		return bal, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, kalan FROM kredi.dilim
		WHERE `+liveLot+` AND kaynak = 'plan' AND NOT iade ORDER BY id FOR UPDATE`, userID, now)
	if err != nil {
		return 0, fmt.Errorf("could not read the plan lots: %w", err)
	}
	var ids []int64
	var remains []int
	total := 0
	for rows.Next() {
		var id int64
		var remain int
		if err := rows.Scan(&id, &remain); err != nil {
			rows.Close()
			return 0, fmt.Errorf("could not read a plan lot: %w", err)
		}
		ids, remains = append(ids, id), append(remains, remain)
		total += remain
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("could not read the plan lots: %w", err)
	}
	var tier Tier
	if len(ids) > 0 {
		if tier, err = lotTier(ctx, tx, ids[len(ids)-1]); err != nil {
			return 0, err
		}
	}
	mid, err := insertMovement(ctx, tx, movement{userID: userID, tur: tur, ref: ref, amount: -total, tier: tier})
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		if remains[i] > 0 {
			if err := lotDelta(ctx, tx, mid, id, -remains[i]); err != nil {
				return 0, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE kredi.dilim SET sure_islendi = true WHERE id = $1`, id); err != nil {
			return 0, fmt.Errorf("could not end the plan lot: %w", err)
		}
	}
	return bal - total, nil
}

// ExtendPlanLot moves the end of the user's current plan lot (the newest
// one not yet swept or ended) to until (grace, P3). It is the only update
// of bitis. No plan lot is not an error.
func (s *PostgresStore) ExtendPlanLot(ctx context.Context, userID string, until time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAccount(ctx, tx, userID, false); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE kredi.dilim SET bitis = $2
			WHERE id = (SELECT id FROM kredi.dilim
			            WHERE kullanici_id = $1::text::uuid AND kaynak = 'plan' AND NOT iade AND NOT sure_islendi
			            ORDER BY bitis DESC NULLS FIRST, id DESC LIMIT 1)
			  AND bitis IS NOT NULL AND bitis < $2`, userID, until); err != nil {
			return fmt.Errorf("could not extend the plan lot: %w", err)
		}
		return nil
	})
}

// ClawBackPack takes amount back after a pack refund (magaza_iade,
// refundRef): from that pack's lot first; the shortfall becomes debt and
// the user's other lots are not touched (P2). ErrLotNotFound when the user
// has no lot for packRef.
func (s *PostgresStore) ClawBackPack(ctx context.Context, userID, packRef, refundRef string, amount int, now time.Time) (int, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("credit: clawback amount must be positive, got %d", amount)
	}
	var balance int
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAccount(ctx, tx, userID, true); err != nil {
			return fmt.Errorf("could not lock the credit account: %w", err)
		}
		bal, err := balanceOf(ctx, tx, userID, now)
		if err != nil {
			return err
		}
		balance = bal
		done, err := movementDone(ctx, tx, userID, turMagazaIade, refundRef)
		if err != nil || done {
			return err
		}
		var lotID int64
		var remain int
		err = tx.QueryRow(ctx, `
			SELECT id, kalan FROM kredi.dilim
			WHERE kullanici_id = $1::text::uuid AND kaynak = 'paket' AND ref = $2 FOR UPDATE`,
			userID, packRef).Scan(&lotID, &remain)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLotNotFound
		}
		if err != nil {
			return fmt.Errorf("could not read the pack lot: %w", err)
		}
		mid, err := insertMovement(ctx, tx, movement{userID: userID, tur: turMagazaIade, ref: refundRef, amount: -amount})
		if err != nil {
			return err
		}
		take := min(remain, amount)
		if take > 0 {
			if err := lotDelta(ctx, tx, mid, lotID, -take); err != nil {
				return err
			}
		}
		if short := amount - take; short > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE kredi.hesap SET borc = borc + $2, guncellendi = now() WHERE kullanici_id = $1::text::uuid`,
				userID, short); err != nil {
				return fmt.Errorf("could not record the debt: %w", err)
			}
		}
		balance = bal - amount
		return nil
	})
	return balance, err
}

// ExpireLots sweeps up to limit lots whose end passed at now: each gets an
// expiry movement (plan_sure, kampanya_sure, yonetici_sure; ref = lot id,
// tutar = −kalan) and is marked done; an empty lot is only marked. Each lot
// runs in its own transaction under its owner's account lock. It returns
// the number of lots swept. Balance and spend already ignore these lots, so
// a late sweep changes no balance.
func (s *PostgresStore) ExpireLots(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, kullanici_id::text FROM kredi.dilim
		WHERE NOT sure_islendi AND bitis IS NOT NULL AND bitis <= $1 AND kaynak <> 'paket'
		ORDER BY bitis, id LIMIT $2`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("could not list the expired lots: %w", err)
	}
	type due struct {
		id     int64
		userID string
	}
	var lots []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.userID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("could not read an expired lot: %w", err)
		}
		lots = append(lots, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("could not list the expired lots: %w", err)
	}
	swept := 0
	for _, d := range lots {
		did := false
		err := s.inTx(ctx, func(tx pgx.Tx) error {
			if _, err := lockAccount(ctx, tx, d.userID, false); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil // deleted user: never recreate the account
				}
				return fmt.Errorf("could not lock the credit account: %w", err)
			}
			var remain int
			var source string
			var stillDue bool
			err := tx.QueryRow(ctx, `
				SELECT kalan, kaynak, NOT sure_islendi AND bitis IS NOT NULL AND bitis <= $2
				FROM kredi.dilim WHERE id = $1 FOR UPDATE`, d.id, now).Scan(&remain, &source, &stillDue)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("could not read the lot: %w", err)
			}
			if !stillDue {
				return nil // extended or ended meanwhile
			}
			if remain > 0 {
				tur := expiryTur[LotSource(source)]
				ref := strconv.FormatInt(d.id, 10)
				done, err := movementDone(ctx, tx, d.userID, tur, ref)
				if err != nil {
					return err
				}
				if !done {
					var tier Tier
					if source == string(LotPlan) {
						if tier, err = lotTier(ctx, tx, d.id); err != nil {
							return err
						}
					}
					mid, err := insertMovement(ctx, tx, movement{userID: d.userID, tur: tur, ref: ref, amount: -remain, tier: tier})
					if err != nil {
						return err
					}
					if err := lotDelta(ctx, tx, mid, d.id, -remain); err != nil {
						return err
					}
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE kredi.dilim SET sure_islendi = true WHERE id = $1`, d.id); err != nil {
				return fmt.Errorf("could not mark the lot expired: %w", err)
			}
			did = true
			return nil
		})
		if err != nil {
			// One bad lot must not stop the sweep; it is retried next run.
			log.Printf("credits: expiring lot %d failed: %v", d.id, err)
			continue
		}
		if did {
			swept++
		}
	}
	return swept, nil
}

// Lots returns the user's live, non-empty lots in spend order.
func (s *PostgresStore) Lots(ctx context.Context, userID string, now time.Time) ([]Lot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kaynak, kalan, bitis, iade FROM kredi.dilim
		WHERE `+liveLot+` AND kalan > 0 `+spendOrder, userID, now)
	if err != nil {
		return nil, fmt.Errorf("could not read the lots: %w", err)
	}
	defer rows.Close()
	var out []Lot
	for rows.Next() {
		var l Lot
		var source string
		if err := rows.Scan(&source, &l.Amount, &l.ExpiresAt, &l.Refund); err != nil {
			return nil, fmt.Errorf("could not read a lot: %w", err)
		}
		l.Source = LotSource(source)
		out = append(out, l)
	}
	return out, rows.Err()
}

// Gift returns the user's latest campaign gift lot that has not expired
// (amount = what the campaign granted, even when spent), or nil. A refund
// grace lot is never a gift.
func (s *PostgresStore) Gift(ctx context.Context, userID string, now time.Time) (*Lot, error) {
	var l Lot
	err := s.pool.QueryRow(ctx, `
		SELECT ilk_miktar, bitis FROM kredi.dilim
		WHERE `+liveLot+` AND kaynak = 'kampanya' AND NOT iade
		ORDER BY id DESC LIMIT 1`, userID, now).Scan(&l.Amount, &l.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the gift lot: %w", err)
	}
	l.Source = LotCampaign
	return &l, nil
}

// MovementOwner returns the user a ledger movement (tur, ref) belongs to.
// The bool is false when there is no such movement.
func (s *PostgresStore) MovementOwner(ctx context.Context, tur, ref string) (string, bool, error) {
	var owner string
	err := s.pool.QueryRow(ctx,
		`SELECT kullanici_id::text FROM kredi.hareket WHERE tur = $1 AND ref = $2`, tur, ref).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("could not read the movement: %w", err)
	}
	return owner, true, nil
}
