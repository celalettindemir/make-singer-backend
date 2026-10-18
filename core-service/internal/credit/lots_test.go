package credit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

var lotNow = time.Date(2031, 3, 10, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time {
	t := lotNow.Add(d)
	return &t
}

const day = 24 * time.Hour

func grant(t *testing.T, s *PostgresStore, user, tur, ref string, src LotSource, amount int, exp *time.Time) {
	t.Helper()
	if _, err := s.Grant(context.Background(), user, tur, ref, Grant{Source: src, Amount: amount, ExpiresAt: exp}, lotNow); err != nil {
		t.Fatalf("Grant %s: %v", ref, err)
	}
}

// remain returns the kalan of the lot (kaynak, ref).
func remain(t *testing.T, s *PostgresStore, src LotSource, ref string) int {
	t.Helper()
	return count(t, s, `SELECT kalan FROM kredi.dilim WHERE kaynak = $1 AND ref = $2`, string(src), ref)
}

func balance(t *testing.T, s *PostgresStore, user string, now time.Time) int {
	t.Helper()
	b, err := s.Balance(context.Background(), user, now)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	return b
}

func setDebt(t *testing.T, s *PostgresStore, user string, debt int) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), `
		INSERT INTO kredi.hesap (kullanici_id, borc) VALUES ($1::uuid, $2)
		ON CONFLICT (kullanici_id) DO UPDATE SET borc = EXCLUDED.borc`, user, debt); err != nil {
		t.Fatal(err)
	}
}

// assertLotLedger checks the invariant every primitive keeps: each lot's
// kalan equals the sum of its dilim_hareket deltas.
func assertLotLedger(t *testing.T, s *PostgresStore) {
	t.Helper()
	if n := count(t, s, `
		SELECT count(*) FROM kredi.dilim d
		WHERE d.kalan <> COALESCE((SELECT SUM(delta) FROM kredi.dilim_hareket WHERE dilim_id = d.id), 0)`); n != 0 {
		t.Fatalf("%d lots disagree with their movements", n)
	}
}

func TestStore_DebitSpendsSoonestExpiryFirst(t *testing.T) {
	s, u := newStore(t)
	grant(t, s, u, "plan_dolum", "p", LotPlan, 60, at(10*day))
	grant(t, s, u, "kampanya", "k", LotCampaign, 24, at(3*day))
	grant(t, s, u, "paket", "b", LotPack, 100, nil)
	bal, err := s.Debit(context.Background(), u, "render", "r1", 30, Meta{}, lotNow)
	if err != nil || bal != 154 {
		t.Fatalf("Debit = %d, %v; want 154", bal, err)
	}
	if k, p, b := remain(t, s, LotCampaign, "k"), remain(t, s, LotPlan, "p"), remain(t, s, LotPack, "b"); k != 0 || p != 54 || b != 100 {
		t.Fatalf("lots kampanya %d plan %d paket %d, want 0 54 100", k, p, b)
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'render' AND ref = 'r1'`); got != -30 {
		t.Fatalf("tutar = %d, want -30", got)
	}
	if got := count(t, s, `SELECT count(*) FROM kredi.dilim_hareket dh JOIN kredi.hareket h ON h.id = dh.hareket_id WHERE h.ref = 'r1'`); got != 2 {
		t.Fatalf("debit touched %d lots, want 2", got)
	}
	assertLotLedger(t, s)
}

func TestStore_DebitTiesUsePlanCampaignAdminPack(t *testing.T) {
	s, u := newStore(t)
	end := at(5 * day)
	grant(t, s, u, "paket", "b", LotPack, 10, nil)
	grant(t, s, u, "yonetici", "a", LotAdmin, 10, end)
	grant(t, s, u, "kampanya", "k", LotCampaign, 10, end)
	grant(t, s, u, "plan_dolum", "p", LotPlan, 10, end)
	lots, err := s.Lots(context.Background(), u, lotNow)
	if err != nil {
		t.Fatal(err)
	}
	want := []LotSource{LotPlan, LotCampaign, LotAdmin, LotPack}
	if len(lots) != 4 {
		t.Fatalf("Lots = %+v", lots)
	}
	for i, l := range lots {
		if l.Source != want[i] || l.Amount != 10 {
			t.Fatalf("lot %d = %+v, want %s 10", i, l, want[i])
		}
	}
	ctx := context.Background()
	steps := []struct {
		ref  string
		left map[LotSource]int
	}{
		{"t1", map[LotSource]int{LotPlan: 0, LotCampaign: 10, LotAdmin: 10, LotPack: 10}},
		{"t2", map[LotSource]int{LotPlan: 0, LotCampaign: 0, LotAdmin: 10, LotPack: 10}},
		{"t3", map[LotSource]int{LotPlan: 0, LotCampaign: 0, LotAdmin: 0, LotPack: 10}},
	}
	refs := map[LotSource]string{LotPlan: "p", LotCampaign: "k", LotAdmin: "a", LotPack: "b"}
	for _, st := range steps {
		if _, err := s.Debit(ctx, u, "render", st.ref, 10, Meta{}, lotNow); err != nil {
			t.Fatal(err)
		}
		for src, w := range st.left {
			if got := remain(t, s, src, refs[src]); got != w {
				t.Fatalf("after %s: %s = %d, want %d", st.ref, src, got, w)
			}
		}
	}
}

func TestStore_DebitInsufficientCountsDebt(t *testing.T) {
	s, u := newStore(t)
	grant(t, s, u, "paket", "b", LotPack, 20, nil)
	setDebt(t, s, u, 15)
	_, err := s.Debit(context.Background(), u, "render", "r1", 12, Meta{}, lotNow)
	var ie *InsufficientError
	if !errors.As(err, &ie) || ie.Required != 12 || ie.Balance != 5 {
		t.Fatalf("err = %v, want InsufficientError{12, 5}", err)
	}
	if remain(t, s, LotPack, "b") != 20 || count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'render'`) != 0 {
		t.Fatal("a refused debit must change nothing")
	}
}

func TestStore_ConcurrentDebitsNeverOverdraw(t *testing.T) {
	s, u := newStore(t)
	grant(t, s, u, "paket", "b", LotPack, 12, nil)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Debit(context.Background(), u, "render", []string{"ra", "rb"}[i], 12, Meta{}, lotNow)
		}(i)
	}
	wg.Wait()
	failed := 0
	for _, err := range errs {
		var ie *InsufficientError
		if errors.As(err, &ie) {
			failed++
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if failed != 1 {
		t.Fatalf("%d debits failed, want exactly 1", failed)
	}
	if b := balance(t, s, u, lotNow); b != 0 {
		t.Fatalf("balance = %d, want 0", b)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.dilim WHERE kalan < 0`); n != 0 {
		t.Fatal("a lot went negative")
	}
	assertLotLedger(t, s)
}

func TestStore_DebitIsIdempotentPerRef(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "paket", "b", LotPack, 20, nil)
	if b, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil || b != 8 {
		t.Fatalf("first = %d, %v", b, err)
	}
	// The repeat would be insufficient, but it is the same debit: no error.
	if b, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil || b != 8 {
		t.Fatalf("repeat = %d, %v; want 8, nil", b, err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'render'`); n != 1 {
		t.Fatalf("%d debit rows, want 1", n)
	}
}

func TestStore_RefundReturnsToSameLots(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "kampanya", "k", LotCampaign, 10, at(2*day))
	grant(t, s, u, "paket", "b", LotPack, 50, nil)
	if _, err := s.Debit(ctx, u, "render", "r1", 32, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if remain(t, s, LotCampaign, "k") != 0 || remain(t, s, LotPack, "b") != 28 {
		t.Fatal("debit did not span both lots")
	}
	bal, ok, err := s.Refund(ctx, u, "render", "render_iade", "r1", lotNow.Add(time.Hour), 168*time.Hour)
	if err != nil || !ok || bal != 60 {
		t.Fatalf("Refund = %d, %v, %v; want 60, true", bal, ok, err)
	}
	if remain(t, s, LotCampaign, "k") != 10 || remain(t, s, LotPack, "b") != 50 {
		t.Fatal("refund did not restore both lots")
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.dilim`); n != 2 {
		t.Fatalf("%d lots, want 2 (no grace lot)", n)
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'render_iade' AND ref = 'r1'`); got != 32 {
		t.Fatalf("refund tutar = %d, want 32", got)
	}
	assertLotLedger(t, s)
}

func TestStore_RefundIntoExpiredLotCreatesGraceLot(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "kampanya", "k", LotCampaign, 20, at(day))
	grant(t, s, u, "paket", "b", LotPack, 30, nil)
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	later := lotNow.Add(2 * day) // campaign lot ended in between, sweep not run
	before := balance(t, s, u, later)
	if before != 30 {
		t.Fatalf("balance before refund = %d, want 30", before)
	}
	bal, ok, err := s.Refund(ctx, u, "render", "render_iade", "r1", later, 168*time.Hour)
	if err != nil || !ok || bal != before+12 {
		t.Fatalf("Refund = %d, %v, %v; want %d", bal, ok, err, before+12)
	}
	if remain(t, s, LotCampaign, "k") != 8 {
		t.Fatal("the expired lot must not be touched")
	}
	var src string
	var kalan, ilk int
	var end time.Time
	if err := s.pool.QueryRow(ctx, `
		SELECT kaynak, kalan, ilk_miktar, bitis FROM kredi.dilim WHERE ref LIKE 'r1:%'`).Scan(&src, &kalan, &ilk, &end); err != nil {
		t.Fatalf("grace lot: %v", err)
	}
	if src != "kampanya" || kalan != 12 || ilk != 12 || !end.Equal(later.Add(168*time.Hour)) {
		t.Fatalf("grace lot = %s %d/%d %v", src, kalan, ilk, end)
	}
	if b := balance(t, s, u, later); b != before+12 {
		t.Fatalf("balance = %d, want %d", b, before+12)
	}
	assertLotLedger(t, s)
}

func TestStore_RefundOnceOnly(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "paket", "b", LotPack, 20, nil)
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Refund(ctx, u, "render", "render_iade", "r1", lotNow, 168*time.Hour); err != nil || !ok {
		t.Fatalf("first refund: %v %v", ok, err)
	}
	bal, ok, err := s.Refund(ctx, u, "render", "render_iade", "r1", lotNow, 168*time.Hour)
	if err != nil || ok || bal != 20 {
		t.Fatalf("second refund = %d, %v, %v; want 20, false", bal, ok, err)
	}
	if _, ok, err := s.Refund(ctx, u, "render", "render_iade", "never", lotNow, 168*time.Hour); err != nil || ok {
		t.Fatalf("refund without debit = %v, %v", ok, err)
	}
	other := "00000000-0000-0000-0000-0000000000aa"
	if _, ok, err := s.Refund(ctx, other, "render", "render_iade", "r1", lotNow, 168*time.Hour); err != nil || ok {
		t.Fatalf("refund by another user = %v, %v", ok, err)
	}
	if remain(t, s, LotPack, "b") != 20 {
		t.Fatal("lot changed by a repeated refund")
	}
}

func TestStore_GrantPaysDebtFirst(t *testing.T) {
	s, u := newStore(t)
	setDebt(t, s, u, 15)
	bal, err := s.Grant(context.Background(), u, "paket", "b", Grant{Source: LotPack, Amount: 100}, lotNow)
	if err != nil || bal != 85 {
		t.Fatalf("Grant = %d, %v; want 85", bal, err)
	}
	if d := count(t, s, `SELECT borc FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); d != 0 {
		t.Fatalf("borc = %d, want 0", d)
	}
	if k, i := remain(t, s, LotPack, "b"), count(t, s, `SELECT ilk_miktar FROM kredi.dilim WHERE ref = 'b'`); k != 85 || i != 100 {
		t.Fatalf("lot %d/%d, want 85/100", k, i)
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'paket' AND ref = 'b'`); got != 100 {
		t.Fatalf("tutar = %d, want 100", got)
	}
	// Repeat changes nothing.
	if bal, err := s.Grant(context.Background(), u, "paket", "b", Grant{Source: LotPack, Amount: 100}, lotNow); err != nil || bal != 85 {
		t.Fatalf("repeat = %d, %v", bal, err)
	}
	assertLotLedger(t, s)
}

func TestStore_GrantRecordsAdminAndCampaign(t *testing.T) {
	s, u := newStore(t)
	cid := int64(7)
	if _, err := s.Grant(context.Background(), u, "yonetici", "a1",
		Grant{Source: LotAdmin, Amount: 5, By: "admin@x.y", Note: "sorry", CampaignID: &cid}, lotNow); err != nil {
		t.Fatal(err)
	}
	var by, note string
	var got int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT yapan, notu, kampanya_id FROM kredi.hareket WHERE ref = 'a1'`).Scan(&by, &note, &got); err != nil {
		t.Fatal(err)
	}
	if by != "admin@x.y" || note != "sorry" || got != 7 {
		t.Fatalf("movement = %q %q %d", by, note, got)
	}
	if _, err := s.Grant(context.Background(), u, "paket", "x", Grant{Source: LotPack, Amount: 5, ExpiresAt: at(day)}, lotNow); err == nil {
		t.Fatal("a pack lot with an end must be rejected")
	}
}

func TestStore_ExpiredLotsIgnoredBeforeSweep(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "plan_dolum", "p", LotPlan, 50, at(-time.Hour))
	grant(t, s, u, "paket", "b", LotPack, 20, nil)
	if b := balance(t, s, u, lotNow); b != 20 {
		t.Fatalf("balance = %d, want 20", b)
	}
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if remain(t, s, LotPlan, "p") != 50 || remain(t, s, LotPack, "b") != 8 {
		t.Fatal("debit took from the expired lot")
	}
	var ie *InsufficientError
	if _, err := s.Debit(ctx, u, "render", "r2", 12, Meta{}, lotNow); !errors.As(err, &ie) || ie.Balance != 8 {
		t.Fatalf("err = %v, want insufficient with balance 8", err)
	}
	lots, err := s.Lots(ctx, u, lotNow)
	if err != nil || len(lots) != 1 || lots[0].Source != LotPack {
		t.Fatalf("Lots = %+v, %v", lots, err)
	}
}

func TestStore_ExpireSweepIsIdempotent(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "plan_dolum", "p", LotPlan, 50, at(-time.Hour))
	grant(t, s, u, "kampanya", "k", LotCampaign, 24, at(-time.Minute))
	grant(t, s, u, "yonetici", "a", LotAdmin, 5, at(day))
	grant(t, s, u, "paket", "b", LotPack, 20, nil)
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	before := balance(t, s, u, lotNow)
	n, err := s.ExpireLots(ctx, lotNow, 500)
	if err != nil || n != 2 {
		t.Fatalf("ExpireLots = %d, %v; want 2", n, err)
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'plan_sure'`); got != -38 {
		t.Fatalf("plan_sure tutar = %d, want -38", got)
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'kampanya_sure'`); got != -24 {
		t.Fatalf("kampanya_sure tutar = %d, want -24", got)
	}
	if remain(t, s, LotPlan, "p") != 0 || remain(t, s, LotCampaign, "k") != 0 || remain(t, s, LotAdmin, "a") != 5 {
		t.Fatal("sweep left wrong remainders")
	}
	if after := balance(t, s, u, lotNow); after != before {
		t.Fatalf("sweep changed the balance: %d -> %d", before, after)
	}
	movements := count(t, s, `SELECT count(*) FROM kredi.hareket`)
	if n, err := s.ExpireLots(ctx, lotNow, 500); err != nil || n != 0 {
		t.Fatalf("second sweep = %d, %v; want 0", n, err)
	}
	if count(t, s, `SELECT count(*) FROM kredi.hareket`) != movements {
		t.Fatal("second sweep wrote movements")
	}
	// The admin lot expires later with its own kind.
	if n, err := s.ExpireLots(ctx, lotNow.Add(2*day), 500); err != nil || n != 1 {
		t.Fatalf("later sweep = %d, %v", n, err)
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'yonetici_sure'`); got != -5 {
		t.Fatalf("yonetici_sure tutar = %d, want -5", got)
	}
	assertLotLedger(t, s)
}

func TestStore_ExpireSweepMarksEmptyLotsWithoutMovement(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "kampanya", "k", LotCampaign, 12, at(-time.Minute))
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ExpireLots(ctx, lotNow, 500); err != nil || n != 1 {
		t.Fatalf("ExpireLots = %d, %v", n, err)
	}
	if count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'kampanya_sure'`) != 0 {
		t.Fatal("empty lot must expire without a movement")
	}
	if count(t, s, `SELECT count(*) FROM kredi.dilim WHERE NOT sure_islendi`) != 0 {
		t.Fatal("empty lot not marked")
	}
}

func TestStore_EndPlanLotsZeroesLivePlan(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "plan_dolum", "p", LotPlan, 300, at(20*day))
	grant(t, s, u, "paket", "b", LotPack, 100, nil)
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	bal, err := s.EndPlanLots(ctx, u, "plan_bitis", "e1", lotNow)
	if err != nil || bal != 100 {
		t.Fatalf("EndPlanLots = %d, %v; want 100", bal, err)
	}
	if remain(t, s, LotPlan, "p") != 0 || remain(t, s, LotPack, "b") != 100 {
		t.Fatal("wrong lots after plan end")
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'plan_bitis' AND ref = 'e1'`); got != -288 {
		t.Fatalf("plan_bitis tutar = %d, want -288", got)
	}
	if bal, err := s.EndPlanLots(ctx, u, "plan_bitis", "e1", lotNow); err != nil || bal != 100 {
		t.Fatalf("repeat = %d, %v", bal, err)
	}
	// The ended lot is done: the sweep writes nothing, a refund of a plan
	// spend lands in a grace lot.
	if n, err := s.ExpireLots(ctx, lotNow.Add(30*day), 500); err != nil || n != 0 {
		t.Fatalf("sweep after end = %d, %v", n, err)
	}
	if _, ok, err := s.Refund(ctx, u, "render", "render_iade", "r1", lotNow, 168*time.Hour); err != nil || !ok {
		t.Fatalf("refund: %v %v", ok, err)
	}
	if remain(t, s, LotPlan, "p") != 0 || count(t, s, `SELECT kalan FROM kredi.dilim WHERE ref LIKE 'r1:%'`) != 12 {
		t.Fatal("refund of an ended plan lot must create a grace lot")
	}
	assertLotLedger(t, s)
}

func TestStore_ExtendPlanLotMovesEnd(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "plan_dolum", "p", LotPlan, 300, at(time.Hour))
	until := lotNow.Add(16 * day)
	if err := s.ExtendPlanLot(ctx, u, until); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, s, u, lotNow.Add(10*day)); b != 300 {
		t.Fatalf("balance during grace = %d, want 300", b)
	}
	if n, err := s.ExpireLots(ctx, lotNow.Add(2*time.Hour), 500); err != nil || n != 0 {
		t.Fatalf("sweep before new end = %d, %v", n, err)
	}
	if b := balance(t, s, u, until.Add(time.Second)); b != 0 {
		t.Fatalf("balance after grace = %d, want 0", b)
	}
	if err := s.ExtendPlanLot(ctx, "00000000-0000-0000-0000-0000000000bb", until); err != nil {
		t.Fatalf("no account must not error: %v", err)
	}
}

func TestStore_ClawBackPackShortfallBecomesDebt(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "paket", "b", LotPack, 100, nil)
	grant(t, s, u, "plan_dolum", "p", LotPlan, 10, nil)
	if _, err := s.EndPlanLots(ctx, u, "plan_bitis", "x", lotNow); err != nil { // plan out of the way
		t.Fatal(err)
	}
	grant(t, s, u, "kampanya", "k", LotCampaign, 24, at(30*day))
	if _, err := s.DeductAdmin(ctx, u, "d1", 24, "a@b.c", "", lotNow); err != nil { // uses the campaign lot
		t.Fatal(err)
	}
	grant(t, s, u, "kampanya", "k2", LotCampaign, 5, nil)
	if _, err := s.Debit(ctx, u, "render", "r1", 75, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if remain(t, s, LotPack, "b") != 30 || remain(t, s, LotCampaign, "k2") != 0 {
		t.Fatalf("setup: pack %d", remain(t, s, LotPack, "b"))
	}
	grant(t, s, u, "yonetici", "a1", LotAdmin, 40, nil)
	bal, err := s.ClawBackPack(ctx, u, "b", "iade1", 100, lotNow)
	if err != nil || bal != -30 {
		t.Fatalf("ClawBackPack = %d, %v; want -30", bal, err)
	}
	if remain(t, s, LotPack, "b") != 0 || remain(t, s, LotAdmin, "a1") != 40 {
		t.Fatal("clawback must only touch the pack lot")
	}
	if d := count(t, s, `SELECT borc FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); d != 70 {
		t.Fatalf("borc = %d, want 70", d)
	}
	if got := count(t, s, `SELECT tutar FROM kredi.hareket WHERE tur = 'magaza_iade' AND ref = 'iade1'`); got != -100 {
		t.Fatalf("tutar = %d, want -100", got)
	}
	if bal, err := s.ClawBackPack(ctx, u, "b", "iade1", 100, lotNow); err != nil || bal != -30 {
		t.Fatalf("repeat = %d, %v", bal, err)
	}
	if _, err := s.ClawBackPack(ctx, u, "nope", "iade2", 10, lotNow); !errors.Is(err, ErrLotNotFound) {
		t.Fatalf("unknown pack err = %v", err)
	}
	var ie *InsufficientError
	if _, err := s.Debit(ctx, u, "render", "r2", 1, Meta{}, lotNow); !errors.As(err, &ie) || ie.Balance != -30 {
		t.Fatalf("debt must block spending: %v", err)
	}
	assertLotLedger(t, s)
}

func TestStore_DeductAdminStopsAtZero(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "paket", "b", LotPack, 10, nil)
	if bal, err := s.DeductAdmin(ctx, u, "d1", 11, "a@b.c", "too much", lotNow); !errors.Is(err, ErrBalanceTooLow) || bal != 10 {
		t.Fatalf("deduct 11 = %d, %v; want 10, ErrBalanceTooLow", bal, err)
	}
	bal, err := s.DeductAdmin(ctx, u, "d2", 10, "a@b.c", "fix", lotNow)
	if err != nil || bal != 0 {
		t.Fatalf("deduct 10 = %d, %v", bal, err)
	}
	var by, note string
	var tutar int
	if err := s.pool.QueryRow(ctx,
		`SELECT yapan, notu, tutar FROM kredi.hareket WHERE tur = 'yonetici' AND ref = 'd2'`).Scan(&by, &note, &tutar); err != nil {
		t.Fatal(err)
	}
	if by != "a@b.c" || note != "fix" || tutar != -10 {
		t.Fatalf("movement = %q %q %d", by, note, tutar)
	}
	if count(t, s, `SELECT count(*) FROM kredi.hareket WHERE ref = 'd1'`) != 0 {
		t.Fatal("refused deduct wrote a movement")
	}
}

func tierOfMovement(t *testing.T, s *PostgresStore, tur, ref string) string {
	t.Helper()
	var tier *string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT kademe FROM kredi.hareket WHERE tur = $1 AND ref = $2`, tur, ref).Scan(&tier); err != nil {
		t.Fatalf("movement %s/%s: %v", tur, ref, err)
	}
	if tier == nil {
		return ""
	}
	return *tier
}

func TestStore_PlanMovementsCarryTier(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	if _, err := s.Grant(ctx, u, "plan_dolum", "p1", Grant{Source: LotPlan, Amount: 300, ExpiresAt: at(10 * day), Tier: TierPlus}, lotNow); err != nil {
		t.Fatal(err)
	}
	if got := tierOfMovement(t, s, "plan_dolum", "p1"); got != "plus" {
		t.Fatalf("plan_dolum tier = %q, want plus", got)
	}
	if _, err := s.EndPlanLots(ctx, u, "plan_bitis", "e1", lotNow); err != nil {
		t.Fatal(err)
	}
	if got := tierOfMovement(t, s, "plan_bitis", "e1"); got != "plus" {
		t.Fatalf("plan_bitis tier = %q, want plus", got)
	}
	if _, err := s.Grant(ctx, u, "plan_dolum", "p2", Grant{Source: LotPlan, Amount: 1000, ExpiresAt: at(20 * day), Tier: TierPro}, lotNow); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ExpireLots(ctx, lotNow.Add(21*day), 500); err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	var lotID string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM kredi.dilim WHERE ref = 'p2'`).Scan(&lotID); err != nil {
		t.Fatal(err)
	}
	if got := tierOfMovement(t, s, "plan_sure", lotID); got != "pro" {
		t.Fatalf("plan_sure tier = %q, want pro", got)
	}
	// Other kinds carry no tier.
	grant(t, s, u, "kampanya", "k", LotCampaign, 5, at(day))
	if n, err := s.ExpireLots(ctx, lotNow.Add(2*day), 500); err != nil || n != 1 {
		t.Fatalf("sweep 2: n=%d err=%v", n, err)
	}
	var campaignLot string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM kredi.dilim WHERE ref = 'k'`).Scan(&campaignLot); err != nil {
		t.Fatal(err)
	}
	if got := tierOfMovement(t, s, "kampanya_sure", campaignLot); got != "" {
		t.Fatalf("kampanya_sure tier = %q, want none", got)
	}
}

func TestStore_DebitRecordsProjectAndReason(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "paket", "b", LotPack, 50, nil)
	const project = "3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71"
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{Reason: "render_mix", ProjectID: project}, lotNow); err != nil {
		t.Fatal(err)
	}
	var reason, got string
	if err := s.pool.QueryRow(ctx, `SELECT sebep, proje_id::text FROM kredi.hareket WHERE tur = 'render'`).Scan(&reason, &got); err != nil ||
		reason != "render_mix" || got != project {
		t.Fatalf("debit row: %q %q %v", reason, got, err)
	}
	// No project: the column stays NULL.
	if _, err := s.Debit(ctx, u, "soz", "s1", 2, Meta{Reason: "lyrics_generate"}, lotNow); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'soz' AND proje_id IS NULL`); n != 1 {
		t.Fatalf("soz rows without project = %d, want 1", n)
	}
}

func TestStore_DebitBlockedWhileInDebt(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "plan_dolum", "p", LotPlan, 300, at(10*day))
	setDebt(t, s, u, 70)
	var ie *InsufficientError
	_, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow)
	if !errors.As(err, &ie) || ie.Required != 12 || ie.Balance != 230 {
		t.Fatalf("debit in debt: %v, want insufficient 12/230", err)
	}
	if remain(t, s, LotPlan, "p") != 300 || count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'render'`) != 0 {
		t.Fatal("a blocked debit wrote something")
	}
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "a", 2, time.Hour, 12, lotNow); !errors.As(err, &ie) {
		t.Fatalf("lyrics action in debt: %v", err)
	}
	// An admin deduction is not a spend and still works.
	if _, err := s.DeductAdmin(ctx, u, "d1", 10, "a@b.c", "n", lotNow); err != nil {
		t.Fatalf("admin deduct in debt: %v", err)
	}
	// A grant pays the debt, then spending works again.
	grant(t, s, u, "paket", "b", LotPack, 100, nil)
	if _, err := s.Debit(ctx, u, "render", "r2", 12, Meta{}, lotNow); err != nil {
		t.Fatalf("debit after the debt was paid: %v", err)
	}
}

func TestStore_RefundGraceLotIsNotAPlanLot(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "plan_dolum", "p1", LotPlan, 50, at(day))
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	later := lotNow.Add(2 * day)
	if _, ok, err := s.Refund(ctx, u, "render", "render_iade", "r1", later, 168*time.Hour); err != nil || !ok {
		t.Fatalf("refund: ok=%v err=%v", ok, err)
	}
	graceEnd := later.Add(168 * time.Hour)
	if n := count(t, s, `SELECT count(*) FROM kredi.dilim WHERE iade AND kaynak = 'plan'`); n != 1 {
		t.Fatalf("refund plan lots = %d, want 1", n)
	}
	// The new period's plan lot is the only plan lot P3 sees.
	if err := s.GrantPlan(ctx, u, "p2", 300, TierPlus, later.Add(30*day), later); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_bitis'`); n != 0 {
		t.Fatalf("GrantPlan ended the refund lot (plan_bitis rows = %d)", n)
	}
	if b := balance(t, s, u, later); b != 312 {
		t.Fatalf("balance = %d, want 312", b)
	}
	if err := s.ExtendPlanLot(ctx, u, later.Add(40*day)); err != nil {
		t.Fatal(err)
	}
	var end time.Time
	if err := s.pool.QueryRow(ctx, `SELECT bitis FROM kredi.dilim WHERE iade`).Scan(&end); err != nil || !end.Equal(graceEnd) {
		t.Fatalf("refund lot end = %v (%v), want %v", end, err, graceEnd)
	}
	if err := s.pool.QueryRow(ctx, `SELECT bitis FROM kredi.dilim WHERE ref = 'p2'`).Scan(&end); err != nil || !end.Equal(later.Add(40*day)) {
		t.Fatalf("plan lot end = %v (%v), want extended", end, err)
	}
	// Ending the plan leaves the refund lot alone.
	if _, err := s.EndPlanLots(ctx, u, "plan_bitis", "e1", later); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, s, u, later); b != 12 {
		t.Fatalf("balance after plan end = %d, want 12 (the refund lot)", b)
	}
}

func TestStore_ExtendPlanLotOnlyMovesLater(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "plan_dolum", "p", LotPlan, 300, at(10*day))
	if err := s.ExtendPlanLot(ctx, u, lotNow.Add(5*day)); err != nil {
		t.Fatal(err)
	}
	var end time.Time
	if err := s.pool.QueryRow(ctx, `SELECT bitis FROM kredi.dilim WHERE ref = 'p'`).Scan(&end); err != nil || !end.Equal(lotNow.Add(10*day)) {
		t.Fatalf("end = %v (%v), want unchanged", end, err)
	}
	if err := s.ExtendPlanLot(ctx, u, lotNow.Add(10*day)); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT bitis FROM kredi.dilim WHERE ref = 'p'`).Scan(&end); err != nil || !end.Equal(lotNow.Add(10*day)) {
		t.Fatalf("equal end moved: %v (%v)", end, err)
	}
}

func TestStore_ExpireLotsDoesNotRecreateDeletedAccount(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "kampanya", "k", LotCampaign, 5, at(-time.Hour))
	if _, err := s.pool.Exec(ctx, `DELETE FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExpireLots(ctx, lotNow, 500); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Fatalf("the sweep recreated the account (%d rows)", n)
	}
}

func TestStore_ExpireLotsSkipsBadLotAndContinues(t *testing.T) {
	s, u := newStore(t)
	other, good := uuid.NewString(), uuid.NewString()
	ctx := context.Background()
	grant(t, s, u, "kampanya", "bad", LotCampaign, 5, at(-3*time.Hour))
	grant(t, s, good, "kampanya", "ok", LotCampaign, 7, at(-time.Hour))
	var badID string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM kredi.dilim WHERE ref = 'bad'`).Scan(&badID); err != nil {
		t.Fatal(err)
	}
	// Poison: the expiry movement of the bad lot already belongs to someone else.
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO kredi.hareket (kullanici_id, tur, ref, tutar) VALUES ($1::uuid, 'kampanya_sure', $2, -1)`, other, badID); err != nil {
		t.Fatal(err)
	}
	n, err := s.ExpireLots(ctx, lotNow, 500)
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want 1 lot swept and no error", n, err)
	}
	if count(t, s, `SELECT count(*) FROM kredi.dilim WHERE ref = 'ok' AND sure_islendi`) != 1 {
		t.Fatal("the good lot behind the bad one was not swept")
	}
}

func TestStore_DebitTiesNoExpiryAdminBeforePack(t *testing.T) {
	s, u := newStore(t)
	grant(t, s, u, "paket", "b", LotPack, 10, nil)
	grant(t, s, u, "yonetici", "a", LotAdmin, 10, nil)
	lots, err := s.Lots(context.Background(), u, lotNow)
	if err != nil || len(lots) != 2 || lots[0].Source != LotAdmin || lots[1].Source != LotPack {
		t.Fatalf("Lots = %+v, %v; want admin then pack", lots, err)
	}
	if _, err := s.Debit(context.Background(), u, "render", "r1", 10, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if remain(t, s, LotAdmin, "a") != 0 || remain(t, s, LotPack, "b") != 10 {
		t.Fatal("pack was spent before the admin lot")
	}
}

func TestStore_DebitFinalTieBreakIsLotID(t *testing.T) {
	s, u := newStore(t)
	end := at(5 * day)
	grant(t, s, u, "plan_dolum", "p-first-id", LotPlan, 10, end)
	grant(t, s, u, "plan_dolum", "a-later-id", LotPlan, 10, end)
	if _, err := s.Debit(context.Background(), u, "render", "r1", 10, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	// Same source and end: the lot written first (lower id) is spent first.
	if remain(t, s, LotPlan, "p-first-id") != 0 || remain(t, s, LotPlan, "a-later-id") != 10 {
		t.Fatal("the lower lot id was not spent first")
	}
}
