package credit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/googleplay"
)

const testPrefix = "com.test.app."

var testNow = time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)

// fakeApple verifies "signed" payloads by looking them up.
type fakeApple struct {
	mu       sync.Mutex
	txs      map[string]*appstore.Transaction
	renewals map[string]*appstore.RenewalInfo
}

func (f *fakeApple) Transaction(jws string) (*appstore.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx, ok := f.txs[jws]
	if !ok {
		return nil, fmt.Errorf("%w: unknown test payload", appstore.ErrInvalid)
	}
	c := *tx
	return &c, nil
}

func (f *fakeApple) Renewal(jws string) (*appstore.RenewalInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.renewals[jws]
	if !ok {
		return nil, fmt.Errorf("%w: unknown test payload", appstore.ErrInvalid)
	}
	c := *r
	return &c, nil
}

// sign registers tx and returns its "signed" form.
func (f *fakeApple) sign(tx *appstore.Transaction) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	jws := "jws-" + tx.TransactionID
	f.txs[jws] = tx
	return jws
}

type fakeAppleAPI struct {
	status            int
	signedTx, renewal string
}

func (f *fakeAppleAPI) SubscriptionStatus(context.Context, string) (int, string, string, error) {
	return f.status, f.signedTx, f.renewal, nil
}

// fakeGoogle is an in-memory Android Publisher.
type fakeGoogle struct {
	mu         sync.Mutex
	subs       map[string]*googleplay.SubscriptionPurchase
	products   map[string]*googleplay.ProductPurchase
	consumeErr error
	apiErr     error // returned by Subscription and Product when set
	consumed   int
	acked      []string
}

func (f *fakeGoogle) Subscription(_ context.Context, token string) (*googleplay.SubscriptionPurchase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.apiErr != nil {
		return nil, f.apiErr
	}
	sp, ok := f.subs[token]
	if !ok {
		return nil, googleplay.ErrNotFound
	}
	c := *sp
	return &c, nil
}

func (f *fakeGoogle) Product(_ context.Context, _, token string) (*googleplay.ProductPurchase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.apiErr != nil {
		return nil, f.apiErr
	}
	pp, ok := f.products[token]
	if !ok {
		return nil, googleplay.ErrNotFound
	}
	c := *pp
	return &c, nil
}

func (f *fakeGoogle) AcknowledgeSubscription(_ context.Context, subscriptionID, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, subscriptionID+"/"+token)
	if sp, ok := f.subs[token]; ok {
		sp.Acknowledged = true
	}
	return nil
}

func (f *fakeGoogle) ConsumeProduct(_ context.Context, _, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consumed++
	if f.consumeErr != nil {
		return f.consumeErr
	}
	if pp, ok := f.products[token]; ok {
		pp.ConsumptionState = 1
	}
	return nil
}

type purchaseEnv struct {
	p      *Purchases
	s      *PostgresStore
	apple  *fakeApple
	api    *fakeAppleAPI
	google *fakeGoogle
}

func newPurchaseEnv(t *testing.T) *purchaseEnv {
	t.Helper()
	s := NewPostgresStore(testPool(t))
	e := &purchaseEnv{
		s:      s,
		apple:  &fakeApple{txs: map[string]*appstore.Transaction{}, renewals: map[string]*appstore.RenewalInfo{}},
		api:    &fakeAppleAPI{},
		google: &fakeGoogle{subs: map[string]*googleplay.SubscriptionPurchase{}, products: map[string]*googleplay.ProductPurchase{}},
	}
	svc := NewService(s, Settings{Enforce: true, Welcome: 24, PlanMonthly: 300, Costs: Costs{RenderMix: 12}})
	e.p = NewPurchases(s, svc, e.apple, e.api, e.google,
		PurchasesConfig{PlanMonthly: 300, AppleProductPrefix: testPrefix, PackageName: "com.test.app"},
		func() time.Time { return testNow })
	return e
}

// user creates a living account and returns its id and store token.
func (e *purchaseEnv) user(t *testing.T) (string, string) {
	t.Helper()
	id := uuid.NewString()
	a, err := e.s.EnsureAccount(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return id, a.StoreToken.String()
}

func (e *purchaseEnv) account(t *testing.T, id string) Account {
	t.Helper()
	a, _, err := e.s.ReadAccount(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (e *purchaseEnv) sub(t *testing.T, store, orig string) *Subscription {
	t.Helper()
	sub, err := e.s.SubscriptionByOriginal(context.Background(), store, orig)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func tp(t time.Time) *time.Time { return &t }

func packTx(id, product, token string) *appstore.Transaction {
	return &appstore.Transaction{
		TransactionID: id, OriginalTransactionID: id, ProductID: testPrefix + product,
		BundleID: "com.test.app", Environment: "Production", AppAccountToken: token,
		PurchaseDate: tp(testNow.Add(-time.Minute)),
	}
}

func subTx(id, orig, product, token string, start time.Time, months int) *appstore.Transaction {
	return &appstore.Transaction{
		TransactionID: id, OriginalTransactionID: orig, ProductID: testPrefix + product,
		BundleID: "com.test.app", Environment: "Production", AppAccountToken: token,
		PurchaseDate: tp(start), ExpiresDate: tp(start.AddDate(0, months, 0)),
	}
}

func notify(typ, subtype, signedTx string) *appstore.Notification {
	return &appstore.Notification{Type: typ, Subtype: subtype, SignedTransactionInfo: signedTx}
}

func TestRefillAt_ClampsMonthEnd(t *testing.T) {
	anchor := time.Date(2027, 1, 31, 9, 30, 0, 0, time.UTC)
	cases := map[int]time.Time{
		1:  time.Date(2027, 2, 28, 9, 30, 0, 0, time.UTC),
		2:  time.Date(2027, 3, 31, 9, 30, 0, 0, time.UTC),
		3:  time.Date(2027, 4, 30, 9, 30, 0, 0, time.UTC),
		13: time.Date(2028, 2, 29, 9, 30, 0, 0, time.UTC),
		14: time.Date(2028, 3, 31, 9, 30, 0, 0, time.UTC),
	}
	for n, want := range cases {
		if got := refillAt(anchor, n); !got.Equal(want) {
			t.Errorf("refillAt(+%d) = %v, want %v", n, got, want)
		}
	}
}

func TestVerifyApple_PackCreditsOnceAcrossReposts(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	jws := e.apple.sign(packTx("tx-pack", "credits.100", tok))
	for i := range 3 {
		res, err := e.p.VerifyApple(ctx, u, jws)
		if err != nil || res.Status != PurchaseCredited || res.Summary.PermanentCredits != 100 || res.Summary.Balance != 100 {
			t.Fatalf("post %d: %+v %v", i, res, err)
		}
	}
	if err := e.p.ApplyAppleNotification(ctx, notify("ONE_TIME_CHARGE", "", jws)); err != nil {
		t.Fatal(err)
	}
	if a := e.account(t, u); a.Permanent != 100 {
		t.Fatalf("permanent = %d, want 100", a.Permanent)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'paket' AND ref = 'apple:tx-pack'`); n != 1 {
		t.Fatalf("pack movements = %d, want 1", n)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 'tx-pack' AND durum = 'kredilendi' AND kullanici_id = $1::uuid`, u); n != 1 {
		t.Fatalf("purchase record = %d, want 1 credited row", n)
	}
}

func TestVerifyApple_TokenOfOtherLivingUser409(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	_, tokA := e.user(t)
	b, _ := e.user(t)
	jws := e.apple.sign(packTx("tx-a", "credits.300", tokA))
	if _, err := e.p.VerifyApple(ctx, b, jws); !errors.Is(err, ErrOwnedByOther) {
		t.Fatalf("err = %v, want ErrOwnedByOther", err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket`); n != 0 {
		t.Fatalf("movements = %d, want 0", n)
	}
	sub := subTx("tx-s", "orig-s", productMonthly, tokA, testNow, 1)
	if _, err := e.p.VerifyApple(ctx, b, e.apple.sign(sub)); !errors.Is(err, ErrOwnedByOther) {
		t.Fatalf("subscription err = %v, want ErrOwnedByOther", err)
	}
	if e.sub(t, storeApple, "orig-s") != nil {
		t.Fatal("subscription row written for a 409")
	}
}

func TestVerifyApple_TokenOfDeletedUserSubmitterClaims(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	a, tokA := e.user(t)
	jws := e.apple.sign(subTx("tx-1", "orig-1", productMonthly, tokA, testNow.AddDate(0, 0, -3), 1))
	if res, err := e.p.VerifyApple(ctx, a, jws); err != nil || res.Status != PurchaseCredited {
		t.Fatalf("owner subscribe: %+v %v", res, err)
	}
	if err := e.s.DeleteUserData(ctx, a); err != nil {
		t.Fatal(err)
	}
	b, _ := e.user(t)
	res, err := e.p.VerifyApple(ctx, b, jws)
	if err != nil || res.Status != PurchaseCredited || res.Summary.PlanCredits != 300 {
		t.Fatalf("claim: %+v %v", res, err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.UserID == nil || *sub.UserID != b {
		t.Fatalf("owner = %v, want %s", sub.UserID, b)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid`, a); n != 0 {
		t.Fatalf("deleted account recreated: %d", n)
	}
}

func TestVerifyApple_NoTokenSubscriptionByOriginal(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	a, tokA := e.user(t)
	b, _ := e.user(t)
	start := testNow.AddDate(0, -1, 0)
	if _, err := e.p.VerifyApple(ctx, a, e.apple.sign(subTx("tx-1", "orig-1", productMonthly, tokA, start, 1))); err != nil {
		t.Fatal(err)
	}
	// A resubscription from the store settings carries no token.
	renewal := e.apple.sign(subTx("tx-2", "orig-1", productMonthly, "", testNow.Add(-time.Hour), 1))
	if _, err := e.p.VerifyApple(ctx, b, renewal); !errors.Is(err, ErrOwnedByOther) {
		t.Fatalf("other user: %v, want ErrOwnedByOther", err)
	}
	res, err := e.p.VerifyApple(ctx, a, renewal)
	if err != nil || res.Status != PurchaseCredited {
		t.Fatalf("owner: %+v %v", res, err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_dolum' AND ref = 'apple:tx-2' AND kullanici_id = $1::uuid`, a); n != 1 {
		t.Fatalf("refill for tx-2 = %d, want 1", n)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid AND plan_bakiye <> 0`, b); n != 0 {
		t.Fatal("other user got plan credits")
	}
}

func TestVerifyApple_RevokedIsInvalid(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	tx := packTx("tx-r", "credits.100", tok)
	tx.RevocationDate = tp(testNow.Add(-time.Minute))
	if _, err := e.p.VerifyApple(context.Background(), u, e.apple.sign(tx)); !errors.Is(err, ErrPurchaseInvalid) {
		t.Fatalf("revoked: %v, want ErrPurchaseInvalid", err)
	}
	if _, err := e.p.VerifyApple(context.Background(), u, "garbage"); !errors.Is(err, ErrPurchaseInvalid) {
		t.Fatalf("bad signature: %v, want ErrPurchaseInvalid", err)
	}
	if _, err := e.p.VerifyApple(context.Background(), u, e.apple.sign(packTx("tx-x", "credits.5", tok))); !errors.Is(err, ErrPurchaseInvalid) {
		t.Fatalf("unknown product: %v, want ErrPurchaseInvalid", err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket`); n != 0 {
		t.Fatalf("movements = %d", n)
	}
}

func TestVerifyApple_SandboxRecordedAsSandbox(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	pk := packTx("tx-sb", "credits.100", tok)
	pk.Environment = "Sandbox"
	if res, err := e.p.VerifyApple(ctx, u, e.apple.sign(pk)); err != nil || res.Status != PurchaseCredited {
		t.Fatalf("sandbox pack: %+v %v", res, err)
	}
	sb := subTx("tx-x", "orig-x", productMonthly, tok, testNow, 1)
	sb.Environment = "Xcode"
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(sb)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 'tx-sb' AND ortam = 'sandbox'`); n != 1 {
		t.Fatalf("sandbox pack rows = %d", n)
	}
	if sub := e.sub(t, storeApple, "orig-x"); sub.Env != envSandbox {
		t.Fatalf("subscription env = %q", sub.Env)
	}
}

func TestVerifyApple_MonthlySubscribeSetsPlan300(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	res, err := e.p.VerifyApple(context.Background(), u, e.apple.sign(subTx("tx-m", "orig-m", productMonthly, tok, testNow, 1)))
	if err != nil || res.Status != PurchaseCredited || res.Summary.PlanCredits != 300 {
		t.Fatalf("got %+v %v", res, err)
	}
	pl := res.Summary.Plan
	if pl == nil || pl.Product != productMonthly || pl.Status != "active" || !pl.PeriodEnd.Equal(testNow.AddDate(0, 1, 0)) {
		t.Fatalf("plan = %+v", pl)
	}
	if sub := e.sub(t, storeApple, "orig-m"); sub.RefillAnchor != nil || sub.NextRefill != nil {
		t.Fatalf("monthly has a refill schedule: %+v", sub)
	}
}

func TestVerifyApple_YearlySetsAnchorAndNextRefill(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	start := time.Date(2027, 1, 31, 10, 0, 0, 0, time.UTC)
	res, err := e.p.VerifyApple(context.Background(), u, e.apple.sign(subTx("tx-y", "orig-y", productYearly, tok, start, 12)))
	if err != nil || res.Summary.PlanCredits != 300 {
		t.Fatalf("got %+v %v", res, err)
	}
	sub := e.sub(t, storeApple, "orig-y")
	if sub.Product != productYearly || sub.RefillAnchor == nil || !sub.RefillAnchor.Equal(start) ||
		sub.NextRefill == nil || !sub.NextRefill.Equal(time.Date(2027, 2, 28, 10, 0, 0, 0, time.UTC)) || sub.RefillCount != 0 {
		t.Fatalf("schedule = %+v", sub)
	}
}

func TestVerifyApple_ExpiredRestoreRecordedNoCredit(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	old := subTx("tx-old", "orig-old", productMonthly, tok, testNow.AddDate(0, -3, 0), 1)
	res, err := e.p.VerifyApple(context.Background(), u, e.apple.sign(old))
	if err != nil || res.Status != PurchaseRecorded || res.Summary.Balance != 0 {
		t.Fatalf("got %+v %v", res, err)
	}
	if sub := e.sub(t, storeApple, "orig-old"); sub == nil || sub.Status != subExpired {
		t.Fatalf("subscription = %+v", sub)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket`); n != 0 {
		t.Fatalf("movements = %d, want 0", n)
	}
}

func googlePack(order string, state int, accountID string) *googleplay.ProductPurchase {
	return &googleplay.ProductPurchase{OrderID: order, PurchaseState: state, AccountID: accountID}
}

func googleSub(state, order, basePlan, accountID string, expiry time.Time) *googleplay.SubscriptionPurchase {
	return &googleplay.SubscriptionPurchase{
		State: state, LatestOrderID: order, AccountID: accountID,
		LineItems: []googleplay.LineItem{{ProductID: "pro", BasePlanID: basePlan, Expiry: expiry, AutoRenew: true}},
	}
}

func TestVerifyGoogle_PackCreditsThenConsumes(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	e.google.products["gtok"] = googlePack("GPA.1", 0, tok)
	res, err := e.p.VerifyGoogle(context.Background(), u, "credits.1000", "gtok")
	if err != nil || res.Status != PurchaseCredited || res.Summary.PermanentCredits != 1000 {
		t.Fatalf("got %+v %v", res, err)
	}
	if e.google.consumed != 1 || e.google.products["gtok"].ConsumptionState != 1 {
		t.Fatalf("consumed = %d", e.google.consumed)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'paket' AND ref = 'google:GPA.1'`); n != 1 {
		t.Fatalf("pack movements = %d", n)
	}
}

func TestVerifyGoogle_PendingNoCredit(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	e.google.products["gtok"] = googlePack("GPA.2", 2, tok)
	res, err := e.p.VerifyGoogle(context.Background(), u, "credits.100", "gtok")
	if err != nil || res.Status != PurchasePending || res.Summary.Balance != 0 {
		t.Fatalf("got %+v %v", res, err)
	}
	if e.google.consumed != 0 {
		t.Fatal("pending purchase consumed")
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket`); n != 0 {
		t.Fatalf("movements = %d", n)
	}
}

func TestVerifyGoogle_ConsumeFailureStillCreditedOnce(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	e.google.products["gtok"] = googlePack("GPA.3", 0, tok)
	e.google.consumeErr = errors.New("google down")
	for i := range 2 {
		res, err := e.p.VerifyGoogle(context.Background(), u, "credits.300", "gtok")
		if err != nil || res.Status != PurchaseCredited || res.Summary.PermanentCredits != 300 {
			t.Fatalf("post %d: %+v %v", i, res, err)
		}
	}
	if e.google.consumed != 2 {
		t.Fatalf("consume attempts = %d, want 2 (retried on repost)", e.google.consumed)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'paket'`); n != 1 {
		t.Fatalf("pack movements = %d, want 1", n)
	}
}

func TestVerifyGoogle_SubscriptionAcknowledges(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.10", "yearly", tok, testNow.AddDate(1, 0, 0))
	res, err := e.p.VerifyGoogle(context.Background(), u, "pro", "stok")
	if err != nil || res.Status != PurchaseCredited || res.Summary.PlanCredits != 300 {
		t.Fatalf("got %+v %v", res, err)
	}
	if len(e.google.acked) != 1 || e.google.acked[0] != "pro/stok" {
		t.Fatalf("acked = %v", e.google.acked)
	}
	sub := e.sub(t, storeGoogle, "stok")
	if sub.Product != productYearly || sub.Status != subActive || sub.NextRefill == nil {
		t.Fatalf("subscription = %+v", sub)
	}
	if _, err := e.p.VerifyGoogle(context.Background(), u, "pro", "stok"); err != nil || len(e.google.acked) != 1 {
		t.Fatalf("repost acked again: %v %v", e.google.acked, err)
	}
}

func TestVerifyGoogle_LinkedTokenKeepsOwner(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	a, tokA := e.user(t)
	b, _ := e.user(t)
	e.google.subs["t1"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.20", "monthly", tokA, testNow.AddDate(0, 0, 10))
	if _, err := e.p.VerifyGoogle(ctx, a, "pro", "t1"); err != nil {
		t.Fatal(err)
	}
	t2 := googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.21", "yearly", "", testNow.AddDate(1, 0, 0))
	t2.LinkedPurchaseToken = "t1"
	e.google.subs["t2"] = t2
	if _, err := e.p.VerifyGoogle(ctx, b, "pro", "t2"); !errors.Is(err, ErrOwnedByOther) {
		t.Fatalf("other user: %v, want ErrOwnedByOther", err)
	}
	res, err := e.p.VerifyGoogle(ctx, a, "pro", "t2")
	if err != nil || res.Status != PurchaseCredited {
		t.Fatalf("owner: %+v %v", res, err)
	}
	if sub := e.sub(t, storeGoogle, "t2"); sub.UserID == nil || *sub.UserID != a || sub.Product != productYearly {
		t.Fatalf("t2 = %+v", sub)
	}
	if old := e.sub(t, storeGoogle, "t1"); old.Status != subExpired {
		t.Fatalf("replaced t1 status = %q, want bitti", old.Status)
	}
}

func TestApplyApple_DidRenewRefillsByTransaction(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	start := testNow.AddDate(0, -1, 0).Add(time.Hour)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-1", "orig-1", productMonthly, tok, start, 1))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.Debit(ctx, u, "render", "job-1", "render_stems", 250, false); err != nil {
		t.Fatal(err)
	}
	renew := e.apple.sign(subTx("tx-2", "orig-1", productMonthly, tok, start.AddDate(0, 1, 0), 1))
	for range 2 {
		if err := e.p.ApplyAppleNotification(ctx, notify("DID_RENEW", "", renew)); err != nil {
			t.Fatal(err)
		}
	}
	if a := e.account(t, u); a.Plan != 300 {
		t.Fatalf("plan = %d, want 300", a.Plan)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_dolum'`); n != 2 {
		t.Fatalf("refills = %d, want 2", n)
	}
	if sub := e.sub(t, storeApple, "orig-1"); !sub.PeriodEnd.Equal(start.AddDate(0, 2, 0)) || sub.Status != subActive {
		t.Fatalf("subscription = %+v", sub)
	}
}

func TestApplyApple_FailToRenewGraceThenExpiredZeroesPlan(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	tx := subTx("tx-1", "orig-1", productMonthly, tok, testNow.AddDate(0, 0, -20), 1)
	jws := e.apple.sign(tx)
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_FAIL_TO_RENEW", "GRACE_PERIOD", jws)); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subGrace {
		t.Fatalf("status = %q, want ek_sure", sub.Status)
	}
	if a := e.account(t, u); a.Plan != 300 {
		t.Fatalf("plan during grace = %d, want 300", a.Plan)
	}
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_FAIL_TO_RENEW", "", jws)); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subBillingIssue {
		t.Fatalf("status = %q, want odeme_sorunu", sub.Status)
	}
	for _, typ := range []string{"GRACE_PERIOD_EXPIRED", "EXPIRED"} {
		if err := e.p.ApplyAppleNotification(ctx, notify(typ, "", jws)); err != nil {
			t.Fatal(err)
		}
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subExpired {
		t.Fatalf("status = %q, want bitti", sub.Status)
	}
	if a := e.account(t, u); a.Plan != 0 {
		t.Fatalf("plan after expiry = %d, want 0", a.Plan)
	}
	ref := "apple:orig-1:" + strconv.FormatInt(tx.ExpiresDate.Unix(), 10)
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_bitis' AND ref = $1`, ref); n != 1 {
		t.Fatalf("plan_bitis %s = %d, want 1", ref, n)
	}
}

func TestApplyApple_RefundPackClawsBackNegative(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	jws := e.apple.sign(packTx("tx-p", "credits.100", tok))
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.Debit(ctx, u, "render", "job-1", "render_stems", 80, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.p.ApplyAppleNotification(ctx, notify("REFUND", "", jws)); err != nil {
			t.Fatal(err)
		}
	}
	if a := e.account(t, u); a.Balance() != -80 {
		t.Fatalf("balance = %d, want -80", a.Balance())
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'magaza_iade' AND ref = 'apple:tx-p'`); n != 1 {
		t.Fatalf("claw-backs = %d, want 1", n)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 'tx-p' AND durum = 'iade'`); n != 1 {
		t.Fatal("purchase not marked refunded")
	}
	// A refunded pack is never credited again.
	if res, err := e.p.VerifyApple(ctx, u, e.apple.sign(packTx("tx-p", "credits.100", tok))); err != nil || res.Summary.Balance != -80 {
		t.Fatalf("repost after refund: %+v %v", res, err)
	}
}

func TestApplyApple_OwnerlessRenewalRecordedNoCredit(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	jws := e.apple.sign(subTx("tx-o", "orig-o", productMonthly, "", testNow.Add(-time.Hour), 1))
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_RENEW", "", jws)); err != nil {
		t.Fatal(err)
	}
	sub := e.sub(t, storeApple, "orig-o")
	if sub == nil || sub.UserID != nil || sub.Status != subActive {
		t.Fatalf("subscription = %+v", sub)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket`) + count(t, e.s, `SELECT count(*) FROM kredi.hesap`); n != 0 {
		t.Fatalf("rows written = %d, want 0", n)
	}
	// The first account to send it claims it (6-G15).
	u, _ := e.user(t)
	res, err := e.p.VerifyApple(ctx, u, jws)
	if err != nil || res.Status != PurchaseCredited || res.Summary.PlanCredits != 300 {
		t.Fatalf("claim: %+v %v", res, err)
	}
}

func TestApplyApple_UnknownTypeIgnored(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	for _, typ := range []string{"CONSUMPTION_REQUEST", "PRICE_INCREASE", "SOMETHING_NEW"} {
		if err := e.p.ApplyAppleNotification(ctx, notify(typ, "", "not-verifiable")); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.abonelik`) + count(t, e.s, `SELECT count(*) FROM kredi.satin_alma`); n != 0 {
		t.Fatalf("rows written = %d", n)
	}
}

func TestApplyApple_ProductSwitchResetsPlanAndAnchor(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-1", "orig-1", productMonthly, tok, testNow.AddDate(0, 0, -10), 1))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.Debit(ctx, u, "render", "job-1", "render_stems", 62, false); err != nil {
		t.Fatal(err)
	}
	switchAt := testNow.Add(-time.Hour)
	up := e.apple.sign(subTx("tx-2", "orig-1", productYearly, tok, switchAt, 12))
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_CHANGE_RENEWAL_PREF", "UPGRADE", up)); err != nil {
		t.Fatal(err)
	}
	if a := e.account(t, u); a.Plan != 300 {
		t.Fatalf("plan = %d, want 300", a.Plan)
	}
	sub := e.sub(t, storeApple, "orig-1")
	if sub.Product != productYearly || sub.RefillAnchor == nil || !sub.RefillAnchor.Equal(switchAt) ||
		sub.RefillCount != 0 || sub.NextRefill == nil || !sub.NextRefill.Equal(refillAt(switchAt, 1)) {
		t.Fatalf("subscription = %+v", sub)
	}
}

func TestApplyGoogle_RenewedNewOrderRefills(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.30", "monthly", tok, testNow.AddDate(0, 0, 1))
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.Debit(ctx, u, "render", "job-1", "render_stems", 200, false); err != nil {
		t.Fatal(err)
	}
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.30..0", "monthly", tok, testNow.AddDate(0, 1, 1))
	n := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Subscription: &googleplay.SubscriptionNotification{NotificationType: 2, PurchaseToken: "stok", SubscriptionID: "pro"}}
	for range 2 {
		if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if a := e.account(t, u); a.Plan != 300 {
		t.Fatalf("plan = %d, want 300", a.Plan)
	}
	if c := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_dolum' AND ref = 'google:GPA.30..0'`); c != 1 {
		t.Fatalf("renewal refills = %d, want 1", c)
	}
	// A notification for another package is ignored.
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_EXPIRED", "GPA.30..0", "monthly", tok, testNow.AddDate(0, 1, 1))
	n.PackageName = "com.other"
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeGoogle, "stok"); sub.Status != subActive {
		t.Fatalf("foreign package changed status to %q", sub.Status)
	}
}

func TestApplyGoogle_VoidedSubscriptionRefunded(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.40", "monthly", tok, testNow.AddDate(0, 1, 0))
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	n := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Voided: &googleplay.VoidedNotification{PurchaseToken: "stok", OrderID: "GPA.40", ProductType: 1}}
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeGoogle, "stok"); sub.Status != subRefunded {
		t.Fatalf("status = %q, want iade", sub.Status)
	}
	if a := e.account(t, u); a.Plan != 0 {
		t.Fatalf("plan = %d, want 0", a.Plan)
	}
	// A voided pack is clawed back from its owner.
	e.google.products["ptok"] = googlePack("GPA.41", 0, tok)
	if _, err := e.p.VerifyGoogle(ctx, u, "credits.100", "ptok"); err != nil {
		t.Fatal(err)
	}
	n.Voided = &googleplay.VoidedNotification{PurchaseToken: "ptok", OrderID: "GPA.41", ProductType: 2}
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if a := e.account(t, u); a.Balance() != 0 {
		t.Fatalf("balance after voided pack = %d, want 0", a.Balance())
	}
}

func TestApplyGoogle_OnHoldBillingIssue(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	expiry := testNow.AddDate(0, 0, 3)
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.50", "monthly", tok, expiry)
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ON_HOLD", "GPA.50", "monthly", tok, expiry)
	n := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Subscription: &googleplay.SubscriptionNotification{NotificationType: 5, PurchaseToken: "stok", SubscriptionID: "pro"}}
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeGoogle, "stok"); sub.Status != subBillingIssue {
		t.Fatalf("status = %q, want odeme_sorunu", sub.Status)
	}
	if c := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_bitis'`); c != 0 {
		t.Fatalf("plan_bitis on hold = %d", c)
	}
	sum, err := e.p.svc.Summary(ctx, u)
	if err != nil || sum.Plan == nil || sum.Plan.Status != "billing_issue" {
		t.Fatalf("summary plan = %+v %v", sum.Plan, err)
	}
}

func TestPurchases_NotConfigured(t *testing.T) {
	p := NewPurchases(nil, nil, nil, nil, nil, PurchasesConfig{}, nil)
	ctx := context.Background()
	if _, err := p.VerifyApple(ctx, "u", "x"); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("apple: %v", err)
	}
	if _, err := p.VerifyGoogle(ctx, "u", "credits.100", "x"); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("google: %v", err)
	}
	if err := p.ApplyAppleNotification(ctx, notify("DID_RENEW", "", "x")); !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("apple notification: %v", err)
	}
	if err := p.Resync(ctx, Subscription{Store: storeApple, OriginalID: "o"}); err != nil {
		t.Errorf("apple resync without API: %v", err)
	}
}

func TestResync_AppleStatusExpiredZeroesPlan(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	tx := subTx("tx-1", "orig-1", productMonthly, tok, testNow.AddDate(0, 0, -20), 1)
	jws := e.apple.sign(tx)
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	e.api.status, e.api.signedTx = 2, jws
	if err := e.p.Resync(ctx, *e.sub(t, storeApple, "orig-1")); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subExpired {
		t.Fatalf("status = %q", sub.Status)
	}
	if a := e.account(t, u); a.Plan != 0 {
		t.Fatalf("plan = %d, want 0", a.Plan)
	}
}

func TestVerifyApple_RestoreDuringGraceKeepsPlan(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	// Active when first sent; its period has ended by the restore.
	jws := e.apple.sign(subTx("tx-1", "orig-1", productMonthly, tok, testNow.AddDate(0, -1, 0).Add(time.Hour), 1))
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_FAIL_TO_RENEW", "GRACE_PERIOD", jws)); err != nil {
		t.Fatal(err)
	}
	e.p.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	res, err := e.p.VerifyApple(ctx, u, jws)
	if err != nil || res.Status != PurchaseRecorded || res.Summary.PlanCredits != 300 {
		t.Fatalf("restore: %+v %v", res, err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subGrace {
		t.Fatalf("status = %q, want ek_sure", sub.Status)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_bitis'`); n != 0 {
		t.Fatalf("plan_bitis = %d, want 0", n)
	}
}

func TestVerifyApple_StaleActiveRecordedNotCredited(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-new", "orig-1", productMonthly, tok, testNow.Add(-time.Hour), 2))); err != nil {
		t.Fatal(err)
	}
	res, err := e.p.VerifyApple(ctx, u, e.apple.sign(subTx("tx-old", "orig-1", productMonthly, tok, testNow.Add(-2*time.Hour), 1)))
	if err != nil || res.Status != PurchaseRecorded {
		t.Fatalf("stale: %+v %v", res, err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 'tx-old' AND durum = 'kaydedildi'`); n != 1 {
		t.Fatalf("stale record not kaydedildi")
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE ref = 'apple:tx-old'`); n != 0 {
		t.Fatalf("stale tx refilled")
	}
	if sub := e.sub(t, storeApple, "orig-1"); !sub.PeriodEnd.Equal(testNow.Add(-time.Hour).AddDate(0, 2, 0)) {
		t.Fatalf("period end regressed: %v", sub.PeriodEnd)
	}
}

func TestApplyApple_ClientThenWebhookSameTxOneRefill(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	jws := e.apple.sign(subTx("tx-1", "orig-1", productMonthly, tok, testNow.Add(-time.Hour), 1))
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.Debit(ctx, u, "render", "job-1", "render_mix", 12, false); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"SUBSCRIBED", "DID_RENEW"} {
		if err := e.p.ApplyAppleNotification(ctx, notify(typ, "", jws)); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_dolum'`); n != 1 {
		t.Fatalf("refills = %d, want 1", n)
	}
	if a := e.account(t, u); a.Plan != 288 {
		t.Fatalf("plan = %d, want 288 (no second refill)", a.Plan)
	}
}

func TestApplyApple_RefundOfOldPeriodKeepsCurrentPlan(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	start := testNow.AddDate(0, -1, 0).Add(time.Hour)
	old := subTx("tx-1", "orig-1", productMonthly, tok, start, 1)
	if _, err := e.p.VerifyApple(ctx, u, e.apple.sign(old)); err != nil {
		t.Fatal(err)
	}
	current := subTx("tx-2", "orig-1", productMonthly, tok, start.AddDate(0, 1, 0), 1)
	if err := e.p.ApplyAppleNotification(ctx, notify("DID_RENEW", "", e.apple.sign(current))); err != nil {
		t.Fatal(err)
	}
	old.RevocationDate = tp(testNow)
	if err := e.p.ApplyAppleNotification(ctx, notify("REFUND", "", e.apple.sign(old))); err != nil {
		t.Fatal(err)
	}
	sub := e.sub(t, storeApple, "orig-1")
	if sub.Status != subActive || !sub.PeriodEnd.Equal(*current.ExpiresDate) {
		t.Fatalf("subscription = %+v", sub)
	}
	if a := e.account(t, u); a.Plan != 300 {
		t.Fatalf("plan = %d, want 300", a.Plan)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 'tx-1' AND durum = 'iade'`); n != 1 {
		t.Fatal("old order not marked iade")
	}
}

func TestApplyGoogle_VoidedOldOrderKeepsCurrentPlan(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.60", "monthly", tok, testNow.AddDate(0, 0, 1))
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.60..0", "monthly", tok, testNow.AddDate(0, 1, 1))
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	n := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Voided: &googleplay.VoidedNotification{PurchaseToken: "stok", OrderID: "GPA.60", ProductType: 1}}
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeGoogle, "stok"); sub.Status != subActive {
		t.Fatalf("status = %q, want aktif", sub.Status)
	}
	if a := e.account(t, u); a.Plan != 300 {
		t.Fatalf("plan = %d, want 300", a.Plan)
	}
	if c := count(t, e.s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 'GPA.60' AND durum = 'iade'`); c != 1 {
		t.Fatal("old order not marked iade")
	}
}

func TestApplyGoogle_VoidedUnknownPackTombstone(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	n := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Voided: &googleplay.VoidedNotification{PurchaseToken: "ptok", OrderID: "GPA.70", ProductType: 2}}
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	e.google.products["ptok"] = googlePack("GPA.70", 0, tok)
	res, err := e.p.VerifyGoogle(ctx, u, "credits.100", "ptok")
	if err != nil || res.Status != PurchaseRecorded || res.Summary.Balance != 0 {
		t.Fatalf("post after void: %+v %v", res, err)
	}
	if e.google.consumed != 0 {
		t.Fatal("refunded pack consumed")
	}
}

// refundingStore marks the purchase refunded just before the credit, as a
// REFUND webhook racing the client POST would.
type refundingStore struct {
	*PostgresStore
}

func (r refundingStore) Credit(ctx context.Context, userID, tur, ref, reason string, amount int) (Account, bool, error) {
	if tur == turPaket {
		store, order, _ := strings.Cut(ref, ":")
		if _, err := r.RecordPurchase(ctx, PurchaseRecord{Store: store, OrderID: order, Product: reason, Status: purchaseRefunded, Env: envProduction}); err != nil {
			return Account{}, false, err
		}
	}
	return r.PostgresStore.Credit(ctx, userID, tur, ref, reason, amount)
}

func TestApplyPack_RefundDuringCreditClawsBack(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.p.store = refundingStore{e.s}
	res, err := e.p.VerifyApple(ctx, u, e.apple.sign(packTx("tx-race", "credits.100", tok)))
	if err != nil || res.Status != PurchaseRecorded || res.Summary.Balance != 0 {
		t.Fatalf("got %+v %v", res, err)
	}
	if n := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'magaza_iade' AND ref = 'apple:tx-race'`); n != 1 {
		t.Fatalf("claw-backs = %d, want 1", n)
	}
}

// refundThenReplay subscribes with tx, has Apple refund it, then replays
// the JWS signed before the refund (no RevocationDate).
func refundThenReplay(t *testing.T, e *purchaseEnv, u string, tx *appstore.Transaction) PurchaseResult {
	t.Helper()
	ctx := context.Background()
	jws := e.apple.sign(tx)
	if _, err := e.p.VerifyApple(ctx, u, jws); err != nil {
		t.Fatal(err)
	}
	// The notification carries the same transaction id as the purchase.
	e.apple.txs["jws-refund-"+tx.TransactionID] = &appstore.Transaction{
		TransactionID: tx.TransactionID, OriginalTransactionID: tx.OriginalTransactionID, ProductID: tx.ProductID,
		BundleID: tx.BundleID, Environment: tx.Environment, AppAccountToken: tx.AppAccountToken,
		PurchaseDate: tx.PurchaseDate, ExpiresDate: tx.ExpiresDate, RevocationDate: tp(testNow),
	}
	if err := e.p.ApplyAppleNotification(ctx, notify("REFUND", "", "jws-refund-"+tx.TransactionID)); err != nil {
		t.Fatal(err)
	}
	res, err := e.p.VerifyApple(ctx, u, jws)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return res
}

func TestVerifyApple_ReplayAfterRefundStaysRefunded(t *testing.T) {
	e := newPurchaseEnv(t)
	u, tok := e.user(t)
	res := refundThenReplay(t, e, u, subTx("tx-1", "orig-1", productMonthly, tok, testNow.Add(-time.Hour), 1))
	if res.Status != PurchaseRecorded || res.Summary.PlanCredits != 0 {
		t.Fatalf("replay result = %+v", res)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subRefunded || sub.NextRefill != nil {
		t.Fatalf("subscription = %+v", sub)
	}
}

func TestVerifyApple_YearlyReplayAfterRefundNoRefills(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	res := refundThenReplay(t, e, u, subTx("tx-y", "orig-y", productYearly, tok, testNow.Add(-time.Hour), 12))
	if res.Summary.PlanCredits != 0 {
		t.Fatalf("plan after replay = %d", res.Summary.PlanCredits)
	}
	if sub := e.sub(t, storeApple, "orig-y"); sub.Status != subRefunded {
		t.Fatalf("status = %q, want iade", sub.Status)
	}
	e.p.now = func() time.Time { return testNow.AddDate(0, 3, 0) }
	refilled, _, err := e.p.RunRefill(ctx)
	if err != nil || refilled != 0 {
		t.Fatalf("RunRefill = %d %v, want 0", refilled, err)
	}
	if a := e.account(t, u); a.Plan != 0 {
		t.Fatalf("plan = %d, want 0", a.Plan)
	}
}

func TestVerifyGoogle_ReplayAfterVoidStaysRefunded(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.80", "yearly", tok, testNow.AddDate(1, 0, 0))
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	n := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Voided: &googleplay.VoidedNotification{PurchaseToken: "stok", OrderID: "GPA.80", ProductType: 1}}
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	// Google still reports the subscription active; the client posts it.
	res, err := e.p.VerifyGoogle(ctx, u, "pro", "stok")
	if err != nil || res.Status != PurchaseRecorded || res.Summary.PlanCredits != 0 {
		t.Fatalf("replay: %+v %v", res, err)
	}
	if sub := e.sub(t, storeGoogle, "stok"); sub.Status != subRefunded {
		t.Fatalf("status = %q, want iade", sub.Status)
	}
	e.p.now = func() time.Time { return testNow.AddDate(0, 3, 0) }
	if refilled, _, err := e.p.RunRefill(ctx); err != nil || refilled != 0 {
		t.Fatalf("RunRefill = %d %v, want 0", refilled, err)
	}
}

func TestVerifyApple_ResubscribeAfterRefundReactivates(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	first := subTx("tx-1", "orig-1", productMonthly, tok, testNow.AddDate(0, 0, -5), 1)
	refundThenReplay(t, e, u, first)
	later := subTx("tx-2", "orig-1", productMonthly, tok, testNow.Add(-time.Minute), 1)
	res, err := e.p.VerifyApple(ctx, u, e.apple.sign(later))
	if err != nil || res.Status != PurchaseCredited || res.Summary.PlanCredits != 300 {
		t.Fatalf("resubscribe: %+v %v", res, err)
	}
	if sub := e.sub(t, storeApple, "orig-1"); sub.Status != subActive || !sub.PeriodEnd.Equal(*later.ExpiresDate) {
		t.Fatalf("subscription = %+v", sub)
	}
}

// withLineOrder gives the sub a line-item order and a different deprecated
// top-level one, so the test shows which one is used.
func withLineOrder(sp *googleplay.SubscriptionPurchase, lineOrder string) *googleplay.SubscriptionPurchase {
	sp.LineItems[0].LatestSuccessfulOrderID = lineOrder
	return sp
}

func TestApplyGoogle_RefillRefUsesLineItemLatestSuccessfulOrder(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["stok"] = withLineOrder(googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.DEPRECATED", "monthly", tok, testNow.AddDate(0, 0, 10)), "GPA.LINE")
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	if c := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_dolum' AND ref = 'google:GPA.LINE'`); c != 1 {
		t.Fatalf("line-item refill = %d, want 1", c)
	}
	if c := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE ref = 'google:GPA.DEPRECATED'`); c != 0 {
		t.Fatalf("deprecated-order movements = %d, want 0", c)
	}
}

func TestApplyGoogle_RefillRefFallsBackToTopLevelLatestOrderID(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["stok"] = googleSub("SUBSCRIPTION_STATE_ACTIVE", "GPA.TOP", "monthly", tok, testNow.AddDate(0, 0, 10))
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	if c := count(t, e.s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'plan_dolum' AND ref = 'google:GPA.TOP'`); c != 1 {
		t.Fatalf("fallback refill = %d, want 1", c)
	}
}

func TestApplyGoogle_VoidedOldOrderDetectedFromLineItem(t *testing.T) {
	e := newPurchaseEnv(t)
	ctx := context.Background()
	u, tok := e.user(t)
	e.google.subs["stok"] = withLineOrder(googleSub("SUBSCRIPTION_STATE_ACTIVE", "", "monthly", tok, testNow.AddDate(0, 0, 1)), "GPA.61")
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	e.google.subs["stok"] = withLineOrder(googleSub("SUBSCRIPTION_STATE_ACTIVE", "", "monthly", tok, testNow.AddDate(0, 1, 1)), "GPA.61..0")
	if _, err := e.p.VerifyGoogle(ctx, u, "pro", "stok"); err != nil {
		t.Fatal(err)
	}
	n := &googleplay.DeveloperNotification{PackageName: "com.test.app",
		Voided: &googleplay.VoidedNotification{PurchaseToken: "stok", OrderID: "GPA.61", ProductType: 1}}
	if err := e.p.ApplyGoogleNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t, storeGoogle, "stok"); sub.Status != subActive {
		t.Fatalf("status = %q, want aktif (old order voided only)", sub.Status)
	}
}

func TestVerifyGoogle_APIFailureIsTransient(t *testing.T) {
	e := newPurchaseEnv(t)
	u, _ := e.user(t)
	e.google.apiErr = errors.New("googleplay: unexpected status 503")
	for _, product := range []string{"pro", "credits.1000"} {
		_, err := e.p.VerifyGoogle(context.Background(), u, product, "gtok")
		if !errors.Is(err, ErrStoreTransient) || errors.Is(err, ErrPurchaseInvalid) {
			t.Fatalf("%s: err = %v, want ErrStoreTransient", product, err)
		}
	}
}
