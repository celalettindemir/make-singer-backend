package credit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// refillBase is far from the clock the other tests use, so RunRefill's
// pruning and resync never touch their rows.
var refillBase = time.Date(2020, 1, 15, 12, 0, 0, 0, time.UTC)

func (e *purchaseEnv) movements(t *testing.T, user string) int {
	t.Helper()
	var n int
	err := e.s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'plan_dolum'`, user).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// yearlySub stores a yearly subscription directly with the given schedule.
func (e *purchaseEnv) yearlySub(t *testing.T, user *string, periodEnd time.Time, count int) string {
	t.Helper()
	orig := "orig-" + uuid.NewString()
	anchor := refillBase
	next := refillAt(anchor, count+1)
	err := e.s.UpsertSubscription(context.Background(), Subscription{
		Store: storeApple, OriginalID: orig, UserID: user, Product: productYearly, Status: subActive,
		PeriodEnd: periodEnd, AutoRenew: true, NextRefill: &next, RefillAnchor: &anchor, RefillCount: count, Env: envProduction,
	})
	if err != nil {
		t.Fatal(err)
	}
	return orig
}

func TestRunRefill_YearlyRefillsOncePerMonth(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, _ := e.user(t)
	orig := e.yearlySub(t, &u, refillBase.AddDate(1, 0, 0), 0)
	e.p.now = func() time.Time { return refillBase.AddDate(0, 1, 1) }
	for range 2 {
		if _, _, err := e.p.RunRefill(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.movements(t, u); n != 1 {
		t.Fatalf("movements = %d, want 1", n)
	}
	sub := e.sub(t, storeApple, orig)
	if sub.RefillCount != 1 || !sub.NextRefill.Equal(refillAt(refillBase, 2)) {
		t.Fatalf("schedule = %d %v", sub.RefillCount, sub.NextRefill)
	}
	if a := e.view(t, u); a.Plan != 300 {
		t.Fatalf("plan = %d", a.Plan)
	}
}

func TestRunRefill_CatchesUpMissedMonths(t *testing.T) {
	e := newPurchaseEnv(t)
	u, _ := e.user(t)
	orig := e.yearlySub(t, &u, refillBase.AddDate(1, 0, 0), 0)
	e.p.now = func() time.Time { return refillBase.AddDate(0, 3, 1) }
	refilled, _, err := e.p.RunRefill(context.Background())
	if err != nil || refilled < 3 {
		t.Fatalf("refilled = %d, %v", refilled, err)
	}
	if n := e.movements(t, u); n != 3 {
		t.Fatalf("movements = %d, want 3", n)
	}
	if sub := e.sub(t, storeApple, orig); sub.RefillCount != 3 {
		t.Fatalf("count = %d", sub.RefillCount)
	}
}

func TestRunRefill_SkipsOwnerless(t *testing.T) {
	e := newPurchaseEnv(t)
	orig := e.yearlySub(t, nil, refillBase.AddDate(1, 0, 0), 0)
	e.p.now = func() time.Time { return refillBase.AddDate(0, 1, 1) }
	if _, _, err := e.p.RunRefill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeApple, orig); sub.RefillCount != 0 || !sub.NextRefill.Equal(refillAt(refillBase, 1)) {
		t.Fatalf("ownerless schedule moved: %d %v", sub.RefillCount, sub.NextRefill)
	}
}

func TestRunRefill_StopsAtPeriodEnd(t *testing.T) {
	e := newPurchaseEnv(t)
	u, _ := e.user(t)
	// The second refill is due exactly at the period end: it is not
	// inside the paid period, so only the first one is given.
	end := refillAt(refillBase, 2)
	orig := e.yearlySub(t, &u, end, 0)
	e.p.now = func() time.Time { return end.Add(-30 * time.Minute) }
	for range 2 {
		if _, _, err := e.p.RunRefill(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.movements(t, u); n != 1 {
		t.Fatalf("movements = %d, want 1", n)
	}
	if sub := e.sub(t, storeApple, orig); sub.RefillCount != 1 {
		t.Fatalf("count = %d", sub.RefillCount)
	}
}

func TestRunRefill_PastPeriodEndGivesNothing(t *testing.T) {
	e := newPurchaseEnv(t)
	u, _ := e.user(t)
	end := refillAt(refillBase, 1).Add(30 * time.Minute)
	orig := e.yearlySub(t, &u, end, 0)
	// The refill is due but the period has ended (the worker was down);
	// within the resync grace the row is still aktif.
	e.p.now = func() time.Time { return end.Add(10 * time.Minute) }
	if _, _, err := e.p.RunRefill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := e.movements(t, u); n != 0 {
		t.Fatalf("movements = %d, want 0", n)
	}
	if sub := e.sub(t, storeApple, orig); sub.RefillCount != 0 {
		t.Fatalf("count = %d", sub.RefillCount)
	}
}

func TestRefillStep_StaleReadChangesNothing(t *testing.T) {
	e := newPurchaseEnv(t)
	u, _ := e.user(t)
	end := refillBase.AddDate(1, 0, 0)
	orig := e.yearlySub(t, &u, end, 0)
	stale := *e.sub(t, storeApple, orig)
	ctx := context.Background()
	now := refillAt(refillBase, 1).Add(time.Hour)
	if ok, err := e.s.RefillStep(ctx, stale, now, "r1", 300, TierPro, refillAt(refillBase, 2)); err != nil || !ok {
		t.Fatalf("first step = %v %v", ok, err)
	}
	// A renewal moves the period end after the snapshot was read.
	newEnd := end.AddDate(0, 1, 0)
	cur := *e.sub(t, storeApple, orig)
	cur.PeriodEnd = newEnd
	if err := e.s.UpsertSubscription(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.s.RefillStep(ctx, stale, now, "r1b", 300, TierPro, refillAt(refillBase, 2)); err != nil || ok {
		t.Fatalf("stale step = %v %v, want false", ok, err)
	}
	if sub := e.sub(t, storeApple, orig); sub.RefillCount != 1 {
		t.Fatalf("count = %d", sub.RefillCount)
	}
	// A fresh step applies the CURRENT period end to the plan.
	fresh := *e.sub(t, storeApple, orig)
	later := refillAt(refillBase, 2).Add(time.Hour)
	if ok, err := e.s.RefillStep(ctx, fresh, later, "r2", 300, TierPro, refillAt(refillBase, 3)); err != nil || !ok {
		t.Fatalf("fresh step = %v %v", ok, err)
	}
	e.p.now = func() time.Time { return later }
	if a := e.view(t, u); a.PlanPeriodEnd == nil || !a.PlanPeriodEnd.Equal(newEnd) {
		t.Fatalf("plan period end = %v, want %v", a.PlanPeriodEnd, newEnd)
	}
}

func TestRunRefill_ResyncsOverdueApple(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	start := refillBase
	e.p.now = func() time.Time { return start.Add(24 * time.Hour) }
	jws := e.apple.sign(subTx("tx-r", "orig-r-"+u, productMonthly, tok, start, 1))
	orig := "orig-r-" + u
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	if a := e.view(t, u); a.Plan != 300 {
		t.Fatalf("plan = %d", a.Plan)
	}
	e.api.status, e.api.signedTx = 2, jws
	e.p.now = func() time.Time { return start.AddDate(0, 1, 0).Add(2 * time.Hour) }
	_, resynced, err := e.p.RunRefill(ctx)
	if err != nil || resynced < 1 {
		t.Fatalf("resynced = %d, %v", resynced, err)
	}
	if sub := e.sub(t, storeApple, orig); sub.Status != subExpired {
		t.Fatalf("status = %q", sub.Status)
	}
	if a := e.view(t, u); a.Plan != 0 {
		t.Fatalf("plan = %d, want 0", a.Plan)
	}
}

func TestRunRefill_PrunesOldActions(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, _ := e.user(t)
	old, fresh := "act-old-"+u, "act-new-"+u
	for ref, at := range map[string]time.Time{old: refillBase.Add(-25 * time.Hour), fresh: refillBase.Add(-time.Hour)} {
		if _, err := e.s.pool.Exec(ctx,
			`INSERT INTO kredi.eylem (ref, kullanici_id, tur, kullanim, olusturuldu) VALUES ($1, $2::uuid, 'render', 12, $3)`,
			ref, u, at); err != nil {
			t.Fatal(err)
		}
	}
	e.p.now = func() time.Time { return refillBase }
	if _, _, err := e.p.RunRefill(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.s.pool.QueryRow(ctx, `SELECT count(*) FROM kredi.eylem WHERE ref = $1`, old).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old rows = %d, %v", n, err)
	}
	if err := e.s.pool.QueryRow(ctx, `SELECT count(*) FROM kredi.eylem WHERE ref = $1`, fresh).Scan(&n); err != nil || n != 1 {
		t.Fatalf("fresh rows = %d, %v", n, err)
	}
}
