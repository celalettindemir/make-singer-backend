package credit

import (
	"context"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/googleplay"
)

// realCredits switches the purchase config to the product values
// (Plus 300, Pro 1000).
func (e *purchaseEnv) realCredits() {
	e.p.cfg.PlanCredits = map[Tier]int{TierPlus: 300, TierPro: 1000}
}

func (e *purchaseEnv) at(t time.Time) { e.p.now = func() time.Time { return t } }

// liveLotOf returns the amount left and the end of the user's live plan lot.
func liveLotOf(t *testing.T, e *purchaseEnv, user string) (int, *time.Time) {
	t.Helper()
	var left int
	var end *time.Time
	err := e.s.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(kalan), 0), MAX(bitis) FROM kredi.dilim
		WHERE kaynak = 'plan' AND NOT iade AND `+liveLot, user, e.p.now()).Scan(&left, &end)
	if err != nil {
		t.Fatal(err)
	}
	return left, end
}

func movementTier(t *testing.T, e *purchaseEnv, user, tur string) string {
	t.Helper()
	var tier *string
	err := e.s.pool.QueryRow(context.Background(),
		`SELECT kademe FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = $2 ORDER BY id DESC LIMIT 1`,
		user, tur).Scan(&tier)
	if err != nil {
		t.Fatalf("movement %s: %v", tur, err)
	}
	if tier == nil {
		return ""
	}
	return *tier
}

func googleSubOf(product, state, order, basePlan, accountID string, expiry time.Time) *googleplay.SubscriptionPurchase {
	return &googleplay.SubscriptionPurchase{
		State: state, LatestOrderID: order, AccountID: accountID,
		LineItems: []googleplay.LineItem{{ProductID: product, BasePlanID: basePlan, Expiry: expiry, AutoRenew: true}},
	}
}

func TestPurchases_PlusMonthlyGrantsPlanLotUntilPeriodEnd(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	u, tok := e.user(t)
	res, err := e.p.VerifyApple(context.Background(), u, e.apple.sign(subTx("tx-1", "orig-1", productPlusMonthly, tok, testNow, 1)))
	if err != nil || res.Status != PurchaseCredited || res.Summary.Balance != 300 {
		t.Fatalf("got %+v %v", res, err)
	}
	left, end := liveLotOf(t, e, u)
	if left != 300 || end == nil || !end.Equal(testNow.AddDate(0, 1, 0)) {
		t.Fatalf("plan lot = %d until %v, want 300 until period end", left, end)
	}
	if got := movementTier(t, e, u, "plan_dolum"); got != "plus" {
		t.Fatalf("plan_dolum tier = %q, want plus", got)
	}
	if pl := res.Summary.Plan; pl == nil || pl.Tier != TierPlus || pl.Period != "monthly" {
		t.Fatalf("plan = %+v", pl)
	}
}

func TestPurchases_ProYearlyRefillsMonthlyWith1000(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	start := time.Date(2027, 1, 20, 10, 0, 0, 0, time.UTC)
	e.at(start)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-y", "orig-y", productProYearly, tok, start, 12))); err != nil {
		t.Fatal(err)
	}
	left, end := liveLotOf(t, e, u)
	if left != 1000 || end == nil || !end.Equal(refillAt(start, 1)) {
		t.Fatalf("first lot = %d until %v, want 1000 until the first refill %v", left, end, refillAt(start, 1))
	}
	e.at(refillAt(start, 1).Add(time.Hour))
	if refilled, _, err := e.p.RunRefill(ctx); err != nil || refilled != 1 {
		t.Fatalf("refilled = %d, %v", refilled, err)
	}
	left, end = liveLotOf(t, e, u)
	if left != 1000 || end == nil || !end.Equal(refillAt(start, 2)) {
		t.Fatalf("second lot = %d until %v, want 1000 until %v", left, end, refillAt(start, 2))
	}
	if got := movementTier(t, e, u, "plan_dolum"); got != "pro" {
		t.Fatalf("plan_dolum tier = %q, want pro", got)
	}
}

func TestPurchases_UpgradePlusToProEndsPlusLotAndGrantsPro(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-1", "orig-1", productPlusMonthly, tok, testNow.AddDate(0, 0, -10), 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Debit(ctx, u, "render", "job-1", 240, Meta{Reason: "render_stems"}, e.p.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Grant(ctx, u, "kampanya", "camp-1", Grant{Source: LotCampaign, Amount: 24}, e.p.now()); err != nil {
		t.Fatal(err)
	}
	if a := e.view(t, u); a.Plan != 60 || a.Balance() != 84 {
		t.Fatalf("before upgrade: plan %d balance %d, want 60 and 84", a.Plan, a.Balance())
	}
	up := e.apple.sign(subTx("tx-2", "orig-1", productProMonthly, tok, testNow.Add(-time.Hour), 1))
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_CHANGE_RENEWAL_PREF", "UPGRADE", up)); err != nil {
		t.Fatal(err)
	}
	if a := e.view(t, u); a.Plan != 1000 || a.Balance() != 1024 {
		t.Fatalf("after upgrade: plan %d balance %d, want 1000 and 1024", a.Plan, a.Balance())
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'plan_bitis' AND tutar = -60`, u); n != 1 {
		t.Fatalf("plan_bitis of the Plus remainder = %d, want 1", n)
	}
	if got := movementTier(t, e, u, "plan_bitis"); got != "plus" {
		t.Fatalf("plan_bitis tier = %q, want plus", got)
	}
	if got := movementTier(t, e, u, "plan_dolum"); got != "pro" {
		t.Fatalf("plan_dolum tier = %q, want pro", got)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Product != productProMonthly {
		t.Fatalf("product = %q", sub.Product)
	}
}

func TestPurchases_UpgradeFromYearlyPlusToMonthlyProIsNotStale(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-1", "orig-1", productPlusYearly, tok, testNow.AddDate(0, 0, -10), 12))); err != nil {
		t.Fatal(err)
	}
	// The new period ends long before the old yearly one.
	up := e.apple.sign(subTx("tx-2", "orig-1", productProMonthly, tok, testNow.Add(-time.Hour), 1))
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_CHANGE_RENEWAL_PREF", "UPGRADE", up)); err != nil {
		t.Fatal(err)
	}
	if a := e.view(t, u); a.Plan != 1000 {
		t.Fatalf("plan = %d, want 1000", a.Plan)
	}
	sub := e.sub(t, storeApple, "orig-1")
	if sub.Product != productProMonthly || !sub.PeriodEnd.Equal(testNow.Add(-time.Hour).AddDate(0, 1, 0)) {
		t.Fatalf("subscription = %+v", sub)
	}
	// A replay of the same order is stale and changes nothing.
	if _, err := e.p.VerifyApple(ctx, u, up); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'plan_dolum' AND ref = 'apple:tx-2'`, u); n != 1 {
		t.Fatalf("upgrade refills = %d, want 1", n)
	}
}

func TestPurchases_SamePeriodChangeReplacesLot(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-1", "orig-1", productPlusMonthly, tok, testNow.AddDate(0, 0, -10), 1))); err != nil {
		t.Fatal(err)
	}
	switchAt := testNow.Add(-time.Hour)
	yearly := e.apple.sign(subTx("tx-2", "orig-1", productPlusYearly, tok, switchAt, 12))
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_CHANGE_RENEWAL_PREF", "", yearly)); err != nil {
		t.Fatal(err)
	}
	left, end := liveLotOf(t, e, u)
	if left != 300 || end == nil || !end.Equal(refillAt(switchAt, 1)) {
		t.Fatalf("plan lot = %d until %v, want 300 (one lot, not two) until %v", left, end, refillAt(switchAt, 1))
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'plan_bitis'`, u); n != 1 {
		t.Fatalf("plan_bitis = %d, want 1 (old lot ended)", n)
	}
	sub := e.sub(t, storeApple, "orig-1")
	if sub.Product != productPlusYearly || sub.RefillAnchor == nil || !sub.RefillAnchor.Equal(switchAt) {
		t.Fatalf("subscription = %+v", sub)
	}
}

func TestPurchases_DowngradeSetsPendingTierAndAppliesOnRenewal(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	start := testNow.AddDate(0, 0, -10)
	pro := e.apple.sign(subTx("tx-1", "orig-1", productProMonthly, tok, start, 1))
	if _, err := e.p.VerifyApple(ctx, u, pro); err != nil {
		t.Fatal(err)
	}
	e.apple.renewals["rn-down"] = &appstore.RenewalInfo{AutoRenewStatus: 1, AutoRenewProductID: testPrefix + productPlusMonthly}
	n := notify("DID_CHANGE_RENEWAL_PREF", "DOWNGRADE", pro)
	n.SignedRenewalInfo = "rn-down"
	if err := e.p.ApplyAppleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	sum, err := e.p.svc.Summary(ctx, u)
	if err != nil || sum.Plan == nil || sum.Plan.Tier != TierPro || sum.Plan.PendingTier == nil || *sum.Plan.PendingTier != TierPlus {
		t.Fatalf("summary plan = %+v %v, want tier pro pending plus", sum.Plan, err)
	}
	if sum.Balance != 1000 {
		t.Fatalf("balance = %d, the downgrade must not touch the lot", sum.Balance)
	}
	// A webhook without renewal info keeps the pending change.
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_CHANGE_RENEWAL_STATUS", "AUTO_RENEW_ENABLED", pro)); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.NextProduct == nil || *sub.NextProduct != productPlusMonthly {
		t.Fatalf("next product = %v after a status webhook", sub.NextProduct)
	}
	// The renewal arrives with the new product.
	renewAt := start.AddDate(0, 1, 0).Add(time.Minute)
	e.at(renewAt)
	renew := e.apple.sign(subTx("tx-2", "orig-1", productPlusMonthly, tok, renewAt, 1))
	e.apple.renewals["rn-same"] = &appstore.RenewalInfo{AutoRenewStatus: 1, AutoRenewProductID: testPrefix + productPlusMonthly}
	rn := notify("DID_RENEW", "", renew)
	rn.SignedRenewalInfo = "rn-same"
	if err := e.p.ApplyAppleNotification(ctx, rn); err != nil {
		t.Fatal(err)
	}
	sum, err = e.p.svc.Summary(ctx, u)
	if err != nil || sum.Plan == nil || sum.Plan.Tier != TierPlus || sum.Plan.PendingTier != nil || sum.Balance != 300 {
		t.Fatalf("after renewal: plan %+v balance %d %v, want plus, no pending, 300", sum.Plan, sum.Balance, err)
	}
	if got := movementTier(t, e, u, "plan_dolum"); got != "plus" {
		t.Fatalf("plan_dolum tier = %q, want plus", got)
	}
}

func TestPurchases_DowngradeGoogleDeferredReplacement(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	expiry := testNow.AddDate(0, 0, 10)
	sp := googleSubOf("pro", "SUBSCRIPTION_STATE_ACTIVE", "GPA.1", "monthly", tok, expiry)
	e.google.subs["tok-pro"] = sp
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "tok-pro"); err != nil {
		t.Fatal(err)
	}
	deferred := googleSubOf("pro", "SUBSCRIPTION_STATE_ACTIVE", "GPA.1", "monthly", tok, expiry)
	deferred.LineItems[0].DeferredProductID, deferred.LineItems[0].DeferredBasePlanID = "plus", "monthly"
	e.google.subs["tok-pro"] = deferred
	notif := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Subscription: &googleplay.SubscriptionNotification{NotificationType: 8, PurchaseToken: "tok-pro", SubscriptionID: "pro"}}
	if err := e.p.ApplyGoogleNotification(ctx, notif); err != nil {
		t.Fatal(err)
	}
	sum, err := e.p.svc.Summary(ctx, u)
	if err != nil || sum.Plan == nil || sum.Plan.PendingTier == nil || *sum.Plan.PendingTier != TierPlus || sum.Balance != 1000 {
		t.Fatalf("summary = %+v balance %d %v, want pending plus and 1000", sum.Plan, sum.Balance, err)
	}
	// At the period end Google starts the Plus purchase, linked to the Pro token.
	e.at(expiry.Add(time.Minute))
	plus := googleSubOf("plus", "SUBSCRIPTION_STATE_ACTIVE", "GPA.2", "monthly", tok, expiry.AddDate(0, 1, 0))
	plus.LinkedPurchaseToken = "tok-pro"
	e.google.subs["tok-plus"] = plus
	if _, err := e.p.VerifyGoogle(ctx, u, "plus", "tok-plus"); err != nil {
		t.Fatal(err)
	}
	sum, err = e.p.svc.Summary(ctx, u)
	if err != nil || sum.Plan == nil || sum.Plan.Tier != TierPlus || sum.Balance != 300 {
		t.Fatalf("after switch: %+v balance %d %v, want plus and 300", sum.Plan, sum.Balance, err)
	}
	if old := e.sub(t, storeGoogle, "tok-pro"); old.Status != subExpired {
		t.Fatalf("old subscription status = %q, want bitti", old.Status)
	}
}

func TestPurchases_UpgradeGoogleLinkedTokenGrantsProAtFullPrice(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["tok-plus"] = googleSubOf("plus", "SUBSCRIPTION_STATE_ACTIVE", "GPA.1", "monthly", tok, testNow.AddDate(0, 0, 20))
	if _, err := e.p.VerifyGoogle(ctx, u, "plus", "tok-plus"); err != nil {
		t.Fatal(err)
	}
	pro := googleSubOf("pro", "SUBSCRIPTION_STATE_ACTIVE", "GPA.2", "monthly", tok, testNow.AddDate(0, 1, 0))
	pro.LinkedPurchaseToken = "tok-plus"
	e.google.subs["tok-pro"] = pro
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "tok-pro"); err != nil {
		t.Fatal(err)
	}
	if a := e.view(t, u); a.Plan != 1000 {
		t.Fatalf("plan = %d, want only the Pro lot of 1000", a.Plan)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'plan_bitis'`, u); n != 1 {
		t.Fatalf("plan_bitis = %d, want 1", n)
	}
	// The old token's late EXPIRED webhook must not end the new Pro lot.
	e.google.subs["tok-plus"] = googleSubOf("plus", "SUBSCRIPTION_STATE_EXPIRED", "GPA.1", "monthly", tok, testNow.AddDate(0, 0, 20))
	notif := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Subscription: &googleplay.SubscriptionNotification{NotificationType: 13, PurchaseToken: "tok-plus", SubscriptionID: "plus"}}
	if err := e.p.ApplyGoogleNotification(ctx, notif); err != nil {
		t.Fatal(err)
	}
	if a := e.view(t, u); a.Plan != 1000 {
		t.Fatalf("plan after the old token expired = %d, want 1000", a.Plan)
	}
}

func TestPurchases_GraceExtendsPlanLot(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	start := testNow.AddDate(0, -1, 0).Add(time.Hour) // the period ends at testNow+1h
	jws := e.apple.sign(subTx("tx-1", "orig-1", productProMonthly, tok, start, 1))
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	graceEnd := testNow.Add(72 * time.Hour)
	e.apple.renewals["rn-g"] = &appstore.RenewalInfo{AutoRenewStatus: 1, GracePeriodExpiresDate: tp(graceEnd)}
	n := notify("DID_FAIL_TO_RENEW", "GRACE_PERIOD", jws)
	n.SignedRenewalInfo = "rn-g"
	if err := e.p.ApplyAppleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if _, end := liveLotOf(t, e, u); end == nil || !end.Equal(graceEnd) {
		t.Fatalf("plan lot end = %v, want the grace end %v", end, graceEnd)
	}
	e.at(testNow.Add(48 * time.Hour)) // past the period end, inside grace
	if a := e.view(t, u); a.Plan != 1000 {
		t.Fatalf("plan in grace = %d, want 1000", a.Plan)
	}
	if pro, err := e.p.svc.HasPro(ctx, u); err != nil || !pro {
		t.Fatalf("HasPro in grace = %v %v, want true", pro, err)
	}
	e.at(graceEnd.Add(time.Hour))
	if a := e.view(t, u); a.Plan != 0 {
		t.Fatalf("plan after grace = %d, want 0", a.Plan)
	}
	if pro, err := e.p.svc.HasPro(ctx, u); err != nil || pro {
		t.Fatalf("HasPro after grace = %v %v, want false", pro, err)
	}
}

func TestPurchases_GraceGoogleExtendsToLineItemExpiry(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	expiry := testNow.Add(time.Hour)
	e.google.subs["tok"] = googleSubOf("plus", "SUBSCRIPTION_STATE_ACTIVE", "GPA.1", "monthly", tok, expiry)
	if _, err := e.p.VerifyGoogle(ctx, u, "plus", "tok"); err != nil {
		t.Fatal(err)
	}
	graceEnd := expiry.Add(48 * time.Hour)
	e.google.subs["tok"] = googleSubOf("plus", "SUBSCRIPTION_STATE_IN_GRACE_PERIOD", "GPA.1", "monthly", tok, graceEnd)
	notif := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Subscription: &googleplay.SubscriptionNotification{NotificationType: 6, PurchaseToken: "tok", SubscriptionID: "plus"}}
	if err := e.p.ApplyGoogleNotification(ctx, notif); err != nil {
		t.Fatal(err)
	}
	e.at(expiry.Add(24 * time.Hour))
	if a := e.view(t, u); a.Plan != 300 {
		t.Fatalf("plan in grace = %d, want 300", a.Plan)
	}
}

func TestPurchases_BillingIssueKeepsLotUntilPeriodEnd(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	u, tok := e.user(t)
	end := testNow.Add(5 * 24 * time.Hour)
	jws := e.apple.sign(subTx("tx-1", "orig-1", productProMonthly, tok, end.AddDate(0, -1, 0), 1))
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	e.apple.renewals["rn-b"] = &appstore.RenewalInfo{AutoRenewStatus: 1}
	n := notify("DID_FAIL_TO_RENEW", "", jws)
	n.SignedRenewalInfo = "rn-b"
	if err := e.p.ApplyAppleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subBillingIssue {
		t.Fatalf("status = %q", sub.Status)
	}
	left, lotEnd := liveLotOf(t, e, u)
	if left != 1000 || lotEnd == nil || !lotEnd.Equal(end) {
		t.Fatalf("plan lot = %d until %v, want 1000 until the period end %v", left, lotEnd, end)
	}
	if c := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'plan_bitis'`, u); c != 0 {
		t.Fatalf("plan_bitis = %d, want 0", c)
	}
	if pro, err := e.p.svc.HasPro(ctx, u); err != nil || !pro {
		t.Fatalf("HasPro before period end = %v %v, want true", pro, err)
	}
	e.at(end.Add(time.Minute))
	if a := e.view(t, u); a.Plan != 0 {
		t.Fatalf("plan after the period end = %d, want 0", a.Plan)
	}
	if pro, err := e.p.svc.HasPro(ctx, u); err != nil || pro {
		t.Fatalf("HasPro after period end = %v %v, want false", pro, err)
	}
}

func TestPurchases_ExpiredOrRefundedEndsPlanLot(t *testing.T) {
	for _, typ := range []string{"EXPIRED", "REFUND"} {
		t.Run(typ, func(t *testing.T) {
			e := newPurchaseEnv(t)
			e.realCredits()
			ctx := context.Background()
			u, tok := e.user(t)
			jws := e.apple.sign(subTx("tx-1", "orig-1", productPlusMonthly, tok, testNow.AddDate(0, 0, -5), 1))
			if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
				t.Fatal(err)
			}
			if a := e.view(t, u); a.Plan != 300 {
				t.Fatalf("plan = %d, want 300", a.Plan)
			}
			if err := e.p.ApplyAppleNotification(ctx, notify(typ, "", jws)); err != nil {
				t.Fatal(err)
			}
			if a := e.view(t, u); a.Plan != 0 {
				t.Fatalf("plan after %s = %d, want 0", typ, a.Plan)
			}
			if c := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'plan_bitis'`, u); c != 1 {
				t.Fatalf("plan_bitis = %d, want 1", c)
			}
			if got := movementTier(t, e, u, "plan_bitis"); got != "plus" {
				t.Fatalf("plan_bitis tier = %q, want plus", got)
			}
		})
	}
}

func TestPurchases_PackGrantsPermanentLot(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	res, err := e.p.VerifyApple(ctx, u, e.apple.sign(packTx("tx-p", "credits.300", tok)))
	if err != nil || res.Status != PurchaseCredited || res.Summary.Balance != 300 {
		t.Fatalf("got %+v %v", res, err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.dilim WHERE kullanici_id = $1::uuid AND kaynak = 'paket' AND bitis IS NULL AND ilk_miktar = 300`, u); n != 1 {
		t.Fatalf("permanent pack lots = %d, want 1", n)
	}
	e.at(testNow.AddDate(5, 0, 0))
	if a := e.view(t, u); a.Permanent != 300 {
		t.Fatalf("balance five years later = %d, want 300", a.Permanent)
	}
}

func TestPurchases_PackRefundClawsBackIntoDebt(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	jws := e.apple.sign(packTx("tx-p", "credits.100", tok))
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Debit(ctx, u, "render", "job-1", 70, Meta{Reason: "render_stems"}, e.p.now()); err != nil {
		t.Fatal(err)
	}
	if err := e.p.ApplyAppleNotification(ctx, notify("REFUND", "", jws)); err != nil {
		t.Fatal(err)
	}
	sum, err := e.p.svc.Summary(ctx, u)
	if err != nil || sum.Debt != 70 || sum.Balance != -70 {
		t.Fatalf("summary debt %d balance %d %v, want debt 70 and balance -70", sum.Debt, sum.Balance, err)
	}
	if err := e.p.ApplyAppleNotification(ctx, notify("REFUND", "", jws)); err != nil {
		t.Fatal(err)
	}
	if sum, _ = e.p.svc.Summary(ctx, u); sum.Debt != 70 {
		t.Fatalf("debt after a repeated refund = %d, want 70", sum.Debt)
	}
}

func TestPurchases_GoogleProductIdsPlusAndPro(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	for _, tc := range []struct {
		product string
		want    int
	}{{"plus", 300}, {"pro", 1000}} {
		u, tok := e.user(t)
		token := "tok-" + tc.product
		e.google.subs[token] = googleSubOf(tc.product, "SUBSCRIPTION_STATE_ACTIVE", "GPA."+tc.product, "yearly", tok, testNow.AddDate(1, 0, 0))
		res, err := e.p.VerifyGoogle(ctx, u, tc.product, token)
		if err != nil || res.Status != PurchaseCredited || res.Summary.Balance != tc.want {
			t.Fatalf("%s: %+v %v", tc.product, res, err)
		}
		if sub := e.sub(t, storeGoogle, token); sub.Product != tc.product+".yearly" {
			t.Fatalf("%s: stored product %q", tc.product, sub.Product)
		}
		if got := e.google.acked[len(e.google.acked)-1]; got != tc.product+"/"+token {
			t.Fatalf("acknowledged %q, want %q", got, tc.product+"/"+token)
		}
	}
	// A Google subscription that is neither plus nor pro is not ours.
	u, tok := e.user(t)
	e.google.subs["tok-x"] = googleSubOf("gold", "SUBSCRIPTION_STATE_ACTIVE", "GPA.x", "monthly", tok, testNow.AddDate(0, 1, 0))
	if _, err := e.p.VerifyGoogle(ctx, u, "gold", "tok-x"); err == nil {
		t.Fatal("unknown google subscription accepted")
	}
}

func TestPurchases_AppleProductPrefixForFourSubscriptions(t *testing.T) {
	e := newPurchaseEnv(t)
	e.realCredits()
	ctx := context.Background()
	for i, tc := range []struct {
		product string
		want    int
	}{{productPlusMonthly, 300}, {productPlusYearly, 300}, {productProMonthly, 1000}, {productProYearly, 1000}} {
		u, tok := e.user(t)
		months := 1
		if isYearly(tc.product) {
			months = 12
		}
		orig := "orig-" + tc.product
		res, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-"+tc.product, orig, tc.product, tok, testNow, months)))
		if err != nil || res.Status != PurchaseCredited || res.Summary.Balance != tc.want {
			t.Fatalf("%d %s: %+v %v", i, tc.product, res, err)
		}
		if sub := e.sub(t, storeApple, orig); sub.Product != tc.product {
			t.Fatalf("%s: stored %q", tc.product, sub.Product)
		}
	}
	// A product without the prefix, or an unknown one, is invalid.
	u, tok := e.user(t)
	tx := subTx("tx-bad", "orig-bad", productPlusMonthly, tok, testNow, 1)
	tx.ProductID = "other.plus.monthly"
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(tx)); err == nil {
		t.Fatal("foreign prefix accepted")
	}
	tx = subTx("tx-bad2", "orig-bad2", "gold.monthly", tok, testNow, 1)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(tx)); err == nil {
		t.Fatal("unknown product accepted")
	}
}
