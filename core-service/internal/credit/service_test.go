package credit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeLedger is an in-memory Ledger that records the calls it receives.
type fakeLedger struct {
	acct     *Account
	sub      *Subscription
	movement []Movement

	calls []string
	debit struct {
		tur, ref, reason string
		amount           int
		allowNegative    bool
	}
	action struct {
		user, id        string
		amount, maxUses int
		window          time.Duration
		allowNegative   bool
	}
	refund         struct{ debitTur, refundTur, ref string }
	creditedAmount int
}

func (f *fakeLedger) EnsureAccount(_ context.Context, userID string) (Account, error) {
	f.calls = append(f.calls, "EnsureAccount")
	if f.acct == nil {
		f.acct = &Account{UserID: userID, StoreToken: uuid.New()}
	}
	return *f.acct, nil
}

func (f *fakeLedger) ReadAccount(context.Context, string) (Account, bool, error) {
	f.calls = append(f.calls, "ReadAccount")
	if f.acct == nil {
		return Account{}, false, nil
	}
	return *f.acct, true, nil
}

func (f *fakeLedger) DebitBucket(_ context.Context, userID, tur, ref, reason string, amount int, allowNegative bool) (Account, bool, error) {
	f.calls = append(f.calls, "Debit")
	f.debit.tur, f.debit.ref, f.debit.reason, f.debit.amount, f.debit.allowNegative = tur, ref, reason, amount, allowNegative
	bal := 0
	if f.acct != nil {
		bal = f.acct.Balance()
	}
	if !allowNegative && bal < amount {
		return Account{}, false, &InsufficientError{Required: amount, Balance: bal}
	}
	f.acct = &Account{UserID: userID, Permanent: bal - amount}
	return *f.acct, true, nil
}

func (f *fakeLedger) RefundBucket(_ context.Context, debitTur, refundTur, ref string) error {
	f.calls = append(f.calls, "Refund")
	f.refund.debitTur, f.refund.refundTur, f.refund.ref = debitTur, refundTur, ref
	return nil
}

func (f *fakeLedger) Credit(_ context.Context, userID, tur, ref, _ string, amount int) (Account, bool, error) {
	f.calls = append(f.calls, "Credit:"+tur+":"+ref)
	if f.acct == nil {
		f.acct = &Account{UserID: userID, StoreToken: uuid.New()}
	}
	if tur == "hediye" {
		if f.acct.WelcomeClaimed {
			return *f.acct, false, nil
		}
		f.acct.WelcomeClaimed = true
	}
	f.acct.Permanent += amount
	f.creditedAmount += amount
	return *f.acct, true, nil
}

func (f *fakeLedger) SetPlan(context.Context, string, string, string, int, *time.Time) (Account, bool, error) {
	return Account{}, false, nil
}

func (f *fakeLedger) ClawBack(context.Context, string, string, int) (Account, bool, error) {
	return Account{}, false, nil
}

func (f *fakeLedger) SpendAction(_ context.Context, userID string, _ Operation, actionID string, amount int, window time.Duration, maxUses int, allowNegative bool) (ActionCharge, error) {
	f.calls = append(f.calls, "SpendAction")
	f.action.user, f.action.id, f.action.amount, f.action.window, f.action.maxUses, f.action.allowNegative = userID, actionID, amount, window, maxUses, allowNegative
	return ActionCharge{Account: Account{Permanent: 40}, Charged: true}, nil
}

func (f *fakeLedger) DropAction(context.Context, string) error { return nil }

func (f *fakeLedger) History(_ context.Context, _ string, beforeID int64, limit int) ([]Movement, error) {
	var out []Movement
	for _, m := range f.movement {
		if (beforeID <= 0 || m.ID < beforeID) && len(out) < limit {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeLedger) DeleteUserData(context.Context, string) error { return nil }

func (f *fakeLedger) LatestSubscription(context.Context, string) (*Subscription, error) {
	return f.sub, nil
}

func testSettings(enforce bool) Settings {
	return Settings{
		Enforce: enforce, Welcome: 30, PlanMonthly: 300,
		Costs: Costs{RenderMix: 12, RenderFocus: 32, RenderStems: 62, LyricsGenerate: 2, LyricsRewrite: 1},
	}
}

func TestService_ShadowSpendWritesNothing(t *testing.T) {
	l := &fakeLedger{}
	s := NewService(l, testSettings(false))
	bal, err := s.Spend(context.Background(), "u1", OpRenderStems, "job1")
	if err != nil || bal != 0 {
		t.Fatalf("Spend = %d, %v; want 0, nil", bal, err)
	}
	if _, charged, err := s.SpendAction(context.Background(), "u1", OpLyricsGenerate, "a1"); err != nil || charged {
		t.Fatalf("SpendAction charged=%v err=%v", charged, err)
	}
	for _, c := range l.calls {
		if c != "ReadAccount" {
			t.Fatalf("shadow mode made a write call: %v", l.calls)
		}
	}
}

func TestService_ShadowRefundStillReachesLedger(t *testing.T) {
	l := &fakeLedger{}
	s := NewService(l, testSettings(false))
	if err := s.Refund(context.Background(), OpRenderStems, "job1"); err != nil {
		t.Fatal(err)
	}
	if l.refund.debitTur != "render" || l.refund.refundTur != "render_iade" || l.refund.ref != "job1" {
		t.Fatalf("render refund = %+v", l.refund)
	}
	if err := s.RefundAction(context.Background(), "u1", OpLyricsGenerate, "a1"); err != nil {
		t.Fatal(err)
	}
	if l.refund.debitTur != "soz" || l.refund.refundTur != "soz_iade" || l.refund.ref != "u1:a1" {
		t.Fatalf("lyrics refund = %+v", l.refund)
	}
}

// A debit made on an enforcing pod is refunded by a shadow pod (or after a
// CREDITS_ENFORCE rollback): the refund row is written.
func TestService_DebitEnforcedRefundThroughShadowWritesRefund(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	store, u := newStore(t)
	seed(t, store, u, 0, 20)
	ctx := context.Background()
	enforcing := NewService(store, testSettings(true))
	if _, err := enforcing.Spend(ctx, u, OpRenderMix, "job9"); err != nil {
		t.Fatal(err)
	}
	shadow := NewService(store, testSettings(false))
	if err := shadow.Refund(ctx, OpRenderMix, "job9"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, store, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'render_iade'`, u); n != 1 {
		t.Fatalf("refund rows = %d, want 1", n)
	}
	if a, _ := store.EnsureAccount(ctx, u); a.Balance() != 20 {
		t.Fatalf("balance = %d, want 20", a.Balance())
	}
	// No debit, no refund row.
	if err := shadow.Refund(ctx, OpRenderMix, "never-debited"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, store, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'render_iade'`, u); n != 1 {
		t.Fatalf("refund rows = %d after a refund without debit, want 1", n)
	}
}

func TestService_EnforcedSpendInsufficient(t *testing.T) {
	l := &fakeLedger{acct: &Account{Permanent: 10}}
	s := NewService(l, testSettings(true))
	_, err := s.Spend(context.Background(), "u1", OpRenderMix, "job1")
	var ins *InsufficientError
	if !errors.As(err, &ins) || ins.Required != 12 || ins.Balance != 10 {
		t.Fatalf("err = %v; want InsufficientError 12/10", err)
	}
	if l.debit.allowNegative {
		t.Fatal("enforced debit must not allow a negative balance")
	}
}

func TestService_SpendUsesConfiguredCost(t *testing.T) {
	l := &fakeLedger{acct: &Account{Permanent: 100}}
	s := NewService(l, testSettings(true))
	bal, err := s.Spend(context.Background(), "u1", OpRenderFocus, "job1")
	if err != nil || bal != 68 {
		t.Fatalf("Spend = %d, %v; want 68", bal, err)
	}
	if l.debit.amount != 32 || l.debit.tur != "render" || l.debit.ref != "job1" || l.debit.reason != "render_focus" {
		t.Fatalf("debit = %+v", l.debit)
	}
}

func TestService_LyricsRefScopedByUser(t *testing.T) {
	l := &fakeLedger{}
	s := NewService(l, testSettings(true))
	ctx := context.Background()
	for op, max := range map[Operation]int{OpLyricsGenerate: 12, OpLyricsRewrite: 2} {
		bal, charged, err := s.SpendAction(ctx, "u1", op, "act1")
		if err != nil || !charged || bal != 40 {
			t.Fatalf("SpendAction = %d %v %v", bal, charged, err)
		}
		if l.action.user != "u1" || l.action.id != "act1" || l.action.window != 10*time.Minute ||
			l.action.maxUses != max || l.action.amount != s.set.Costs.Of(op) || l.action.allowNegative {
			t.Fatalf("%s: action = %+v", op, l.action)
		}
	}
	if err := s.RefundAction(ctx, "u1", OpLyricsGenerate, "act1"); err != nil {
		t.Fatal(err)
	}
	if l.refund.debitTur != "soz" || l.refund.refundTur != "soz_iade" || l.refund.ref != "u1:act1" {
		t.Fatalf("refund = %+v", l.refund)
	}
}

func TestService_ClaimWelcomeOnce(t *testing.T) {
	l := &fakeLedger{}
	s := NewService(l, testSettings(false))
	first, err := s.ClaimWelcome(context.Background(), "u1")
	if err != nil || first.Welcome.Available || first.Balance != 30 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := s.ClaimWelcome(context.Background(), "u1")
	if err != nil || second.Welcome.Available || second.Balance != 30 {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if l.creditedAmount != 30 {
		t.Fatalf("credited %d, want 30", l.creditedAmount)
	}
}

func TestService_SummaryShape(t *testing.T) {
	s := NewService(&fakeLedger{}, testSettings(false))
	got, err := s.Summary(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	want := CostTable{Render: RenderCosts{Mix: 12, Focus: 32, Stems: 62}, Lyrics: LyricsCosts{Generate: 2, Rewrite: 1}}
	if got.Costs != want || got.Enforced || got.Plan != nil || !got.Welcome.Available || got.Welcome.Amount != 30 || got.StoreAccountToken == "" {
		t.Fatalf("summary = %+v", got)
	}
}

func TestService_SummaryMapsPlanStatus(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for tur, want := range map[string]string{"aktif": "active", "odeme_sorunu": "billing_issue", "ek_sure": "grace", "bitti": "expired", "iade": "refunded"} {
		l := &fakeLedger{sub: &Subscription{Product: "pro.monthly", Status: tur, PeriodEnd: end, AutoRenew: true}}
		got, err := NewService(l, testSettings(true)).Summary(context.Background(), "u1")
		if err != nil || got.Plan == nil || got.Plan.Status != want || got.Plan.Product != "pro.monthly" || !got.Plan.PeriodEnd.Equal(end) {
			t.Fatalf("%s: plan = %+v, %v", tur, got.Plan, err)
		}
	}
}

func TestService_HistoryPagesAndMapsKinds(t *testing.T) {
	l := &fakeLedger{}
	for id := int64(25); id >= 1; id-- {
		l.movement = append(l.movement, Movement{ID: id, Tur: "render", Reason: "render_mix", Amount: -12})
	}
	l.movement[0] = Movement{ID: 25, Tur: "soz_iade", Reason: "lyrics_generate", Amount: 2}
	l.movement[1] = Movement{ID: 24, Tur: "hediye", Amount: 30}
	s := NewService(l, testSettings(true))
	p1, err := s.History(context.Background(), "u1", "")
	if err != nil || len(p1.Items) != 20 || p1.NextCursor == nil || *p1.NextCursor != "6" {
		t.Fatalf("page 1: %d items, cursor %v, %v", len(p1.Items), p1.NextCursor, err)
	}
	if p1.Items[0].Kind != "lyrics_refund" || p1.Items[0].Operation == nil || *p1.Items[0].Operation != "lyrics_generate" {
		t.Fatalf("item 0 = %+v", p1.Items[0])
	}
	if p1.Items[1].Kind != "welcome" || p1.Items[1].Operation != nil {
		t.Fatalf("item 1 = %+v", p1.Items[1])
	}
	p2, err := s.History(context.Background(), "u1", *p1.NextCursor)
	if err != nil || len(p2.Items) != 5 || p2.NextCursor != nil {
		t.Fatalf("page 2: %d items, cursor %v, %v", len(p2.Items), p2.NextCursor, err)
	}
}

func TestService_HistoryBadCursor(t *testing.T) {
	s := NewService(&fakeLedger{}, testSettings(true))
	for _, c := range []string{"abc", "-3", "0", "1.5"} {
		if _, err := s.History(context.Background(), "u1", c); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: err = %v", c, err)
		}
	}
}

func TestService_UnknownOperationErrors(t *testing.T) {
	l := &fakeLedger{acct: &Account{Permanent: 100}}
	s := NewService(l, testSettings(true))
	if _, err := s.Spend(context.Background(), "u1", Operation("nope"), "j"); !errors.Is(err, ErrUnknownOperation) {
		t.Fatalf("Spend err = %v", err)
	}
	if _, _, err := s.SpendAction(context.Background(), "u1", Operation("nope"), "a"); !errors.Is(err, ErrUnknownOperation) {
		t.Fatalf("SpendAction err = %v", err)
	}
	if len(l.calls) != 0 {
		t.Fatalf("ledger touched: %v", l.calls)
	}
}

func TestService_SummaryPlanNilWithoutPeriodEnd(t *testing.T) {
	l := &fakeLedger{sub: &Subscription{Product: "pro.monthly", Status: "aktif"}}
	got, err := NewService(l, testSettings(true)).Summary(context.Background(), "u1")
	if err != nil || got.Plan != nil {
		t.Fatalf("plan = %+v, %v", got.Plan, err)
	}
}
