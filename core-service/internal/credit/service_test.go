package credit

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeLedger is an in-memory Ledger that records the calls it receives. It
// embeds the interface, so a method a test does not set up panics.
type fakeLedger struct {
	Ledger
	acct   *Account
	sub    *Subscription
	lots   []Lot
	gift   *Lot
	bal    int
	owners map[string]string
	pages  Page

	calls  []string
	filter HistoryFilter
	cursor string
	debit  struct {
		tur, ref string
		amount   int
		meta     Meta
	}
	action struct {
		user, id        string
		amount, maxUses int
		window          time.Duration
	}
	refund struct{ user, debitTur, refundTur, ref string }
}

func (f *fakeLedger) EnsureAccount(_ context.Context, userID string) (Account, error) {
	f.calls = append(f.calls, "EnsureAccount")
	if f.acct == nil {
		f.acct = &Account{UserID: userID, StoreToken: uuid.New()}
	}
	return *f.acct, nil
}

func (f *fakeLedger) Balance(context.Context, string, time.Time) (int, error) {
	f.calls = append(f.calls, "Balance")
	return f.bal, nil
}

func (f *fakeLedger) Lots(context.Context, string, time.Time) ([]Lot, error) { return f.lots, nil }

func (f *fakeLedger) Gift(context.Context, string, time.Time) (*Lot, error) { return f.gift, nil }

func (f *fakeLedger) Debit(_ context.Context, _, tur, ref string, amount int, meta Meta, _ time.Time) (int, error) {
	f.calls = append(f.calls, "Debit")
	f.debit.tur, f.debit.ref, f.debit.amount, f.debit.meta = tur, ref, amount, meta
	if f.bal < amount {
		return 0, &InsufficientError{Required: amount, Balance: f.bal}
	}
	return f.bal - amount, nil
}

func (f *fakeLedger) MovementOwner(_ context.Context, tur, ref string) (string, bool, error) {
	owner, ok := f.owners[tur+"/"+ref]
	return owner, ok, nil
}

func (f *fakeLedger) Refund(_ context.Context, userID, debitTur, refundTur, ref string, _ time.Time, _ time.Duration) (int, bool, error) {
	f.calls = append(f.calls, "Refund")
	f.refund.user, f.refund.debitTur, f.refund.refundTur, f.refund.ref = userID, debitTur, refundTur, ref
	return f.bal, true, nil
}

func (f *fakeLedger) SpendAction(_ context.Context, userID string, _ Operation, actionID string, amount int, window time.Duration, maxUses int, _ time.Time) (ActionCharge, error) {
	f.calls = append(f.calls, "SpendAction")
	f.action.user, f.action.id, f.action.amount, f.action.window, f.action.maxUses = userID, actionID, amount, window, maxUses
	return ActionCharge{Balance: 40, Charged: true}, nil
}

func (f *fakeLedger) History(_ context.Context, _, cursor string, filter HistoryFilter) (Page, error) {
	f.cursor, f.filter = cursor, filter
	if cursor == "bad" {
		return Page{}, ErrBadCursor
	}
	return f.pages, nil
}

func (f *fakeLedger) LatestSubscription(context.Context, string) (*Subscription, error) {
	return f.sub, nil
}

func testSettings(enforce bool) Settings {
	return Settings{
		Enforce:     enforce,
		PlanCredits: map[Tier]int{TierPlus: 300, TierPro: 1000},
		Costs:       Costs{RenderMix: 23, RenderFocus: 43, RenderStems: 63, LyricsGenerate: 2, LyricsRewrite: 1, Master: 3},
		RefundGrace: 168 * time.Hour,
	}
}

// newTestService builds a service whose clock is lotNow.
func newTestService(l Ledger, enforce bool) *Service {
	s := NewService(l, testSettings(enforce))
	s.now = func() time.Time { return lotNow }
	return s
}

func TestService_ShadowSpendWritesNothing(t *testing.T) {
	l := &fakeLedger{}
	s := newTestService(l, false)
	bal, err := s.Spend(context.Background(), "u1", OpRenderStems, "job1", "")
	if err != nil || bal != 0 {
		t.Fatalf("Spend = %d, %v; want 0, nil", bal, err)
	}
	if _, charged, err := s.SpendAction(context.Background(), "u1", OpLyricsGenerate, "a1"); err != nil || charged {
		t.Fatalf("SpendAction charged=%v err=%v", charged, err)
	}
	for _, c := range l.calls {
		if c != "Balance" {
			t.Fatalf("shadow mode made a write call: %v", l.calls)
		}
	}
}

func TestService_ShadowRefundStillReachesLedger(t *testing.T) {
	l := &fakeLedger{owners: map[string]string{"render/job1": "u1", "master/job2": "u1"}}
	s := newTestService(l, false)
	ctx := context.Background()
	if err := s.Refund(ctx, OpRenderStems, "job1"); err != nil {
		t.Fatal(err)
	}
	if l.refund.user != "u1" || l.refund.debitTur != "render" || l.refund.refundTur != "render_iade" || l.refund.ref != "job1" {
		t.Fatalf("render refund = %+v", l.refund)
	}
	if err := s.Refund(ctx, OpMaster, "job2"); err != nil {
		t.Fatal(err)
	}
	if l.refund.debitTur != "master" || l.refund.refundTur != "master_iade" || l.refund.ref != "job2" {
		t.Fatalf("master refund = %+v", l.refund)
	}
	if err := s.RefundAction(ctx, "u1", OpLyricsGenerate, "a1"); err != nil {
		t.Fatal(err)
	}
	if l.refund.user != "u1" || l.refund.debitTur != "soz" || l.refund.refundTur != "soz_iade" || l.refund.ref != "u1:a1" {
		t.Fatalf("lyrics refund = %+v", l.refund)
	}
}

func TestService_RefundWithoutDebitTouchesNothing(t *testing.T) {
	l := &fakeLedger{}
	if err := newTestService(l, true).Refund(context.Background(), OpRenderMix, "never"); err != nil {
		t.Fatal(err)
	}
	if len(l.calls) != 0 {
		t.Fatalf("ledger touched: %v", l.calls)
	}
}

// A debit made on an enforcing pod is refunded by a shadow pod (or after a
// CREDITS_ENFORCE rollback): the refund row is written.
func TestService_DebitEnforcedRefundThroughShadowWritesRefund(t *testing.T) {
	store, u := newStore(t)
	seed(t, store, u, 0, 30)
	ctx := context.Background()
	enforcing := newTestService(store, true)
	if _, err := enforcing.Spend(ctx, u, OpRenderMix, "job9", "3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71"); err != nil {
		t.Fatal(err)
	}
	shadow := newTestService(store, false)
	if err := shadow.Refund(ctx, OpRenderMix, "job9"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, store, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'render_iade'`, u); n != 1 {
		t.Fatalf("refund rows = %d, want 1", n)
	}
	if b := balance(t, store, u, lotNow); b != 30 {
		t.Fatalf("balance = %d, want 30", b)
	}
	// No debit, no refund row.
	if err := shadow.Refund(ctx, OpRenderMix, "never-debited"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, store, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid AND tur = 'render_iade'`, u); n != 1 {
		t.Fatalf("refund rows = %d after a refund without debit, want 1", n)
	}
}

func TestService_SpendMasterDebitsMasterKind(t *testing.T) {
	store, u := newStore(t)
	seed(t, store, u, 0, 20)
	ctx := context.Background()
	s := newTestService(store, true)
	bal, err := s.Spend(ctx, u, OpMaster, "m1", "3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71")
	if err != nil || bal != 17 {
		t.Fatalf("Spend master = %d, %v; want 17", bal, err)
	}
	if err := s.Refund(ctx, OpMaster, "m1"); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, store, u, lotNow); b != 20 {
		t.Fatalf("balance after refund = %d, want 20", b)
	}
	if n := count(t, store, `SELECT count(*) FROM kredi.hareket WHERE tur = 'master_iade' AND ref = 'm1'`); n != 1 {
		t.Fatalf("master_iade rows = %d, want 1", n)
	}
}

func TestService_EnforcedSpendInsufficient(t *testing.T) {
	l := &fakeLedger{bal: 10}
	s := newTestService(l, true)
	_, err := s.Spend(context.Background(), "u1", OpRenderMix, "job1", "")
	var ins *InsufficientError
	if !errors.As(err, &ins) || ins.Required != 23 || ins.Balance != 10 {
		t.Fatalf("err = %v; want InsufficientError 23/10", err)
	}
}

func TestService_SpendUsesConfiguredCost(t *testing.T) {
	l := &fakeLedger{bal: 100}
	s := newTestService(l, true)
	bal, err := s.Spend(context.Background(), "u1", OpRenderFocus, "job1", "p1")
	if err != nil || bal != 57 {
		t.Fatalf("Spend = %d, %v; want 57", bal, err)
	}
	if l.debit.amount != 43 || l.debit.tur != "render" || l.debit.ref != "job1" ||
		l.debit.meta.Reason != "render_focus" || l.debit.meta.ProjectID != "p1" {
		t.Fatalf("debit = %+v", l.debit)
	}
	if _, err := s.Spend(context.Background(), "u1", OpMaster, "m1", "p1"); err != nil || l.debit.amount != 3 || l.debit.tur != "master" {
		t.Fatalf("master debit = %+v, %v", l.debit, err)
	}
}

func TestService_LyricsRefScopedByUser(t *testing.T) {
	l := &fakeLedger{}
	s := newTestService(l, true)
	ctx := context.Background()
	for op, max := range map[Operation]int{OpLyricsGenerate: 12, OpLyricsRewrite: 2} {
		bal, charged, err := s.SpendAction(ctx, "u1", op, "act1")
		if err != nil || !charged || bal != 40 {
			t.Fatalf("SpendAction = %d %v %v", bal, charged, err)
		}
		if l.action.user != "u1" || l.action.id != "act1" || l.action.window != 10*time.Minute ||
			l.action.maxUses != max || l.action.amount != s.set.Costs.Of(op) {
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

func TestLyrics_ActionChargesLots(t *testing.T) {
	store, u := newStore(t)
	seed(t, store, u, 5, 10)
	ctx := context.Background()
	s := newTestService(store, true)

	bal, charged, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act")
	if err != nil || !charged || bal != 13 {
		t.Fatalf("first request: bal=%d charged=%v err=%v", bal, charged, err)
	}
	// The cost came from the plan lot (soonest end), not the pack.
	if r := remain(t, store, LotPlan, "seed-plan-"+u); r != 3 {
		t.Fatalf("plan lot = %d, want 3", r)
	}
	for i := 0; i < 3; i++ {
		bal, charged, err = s.SpendAction(ctx, u, OpLyricsGenerate, "act")
		if err != nil || charged || bal != 13 {
			t.Fatalf("follow-up %d: bal=%d charged=%v err=%v", i, bal, charged, err)
		}
	}
	// AI_ERROR on the first request: the charge is refunded and the action closed.
	if err := s.RefundAction(ctx, u, OpLyricsGenerate, "act"); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, store, u, lotNow); b != 15 {
		t.Fatalf("balance after refund = %d, want 15", b)
	}
	if r := remain(t, store, LotPlan, "seed-plan-"+u); r != 5 {
		t.Fatalf("plan lot after refund = %d, want 5", r)
	}
	if _, _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act"); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("reuse after refund: %v", err)
	}
	// A new action id is a new charge.
	if bal, charged, err = s.SpendAction(ctx, u, OpLyricsRewrite, "act2"); err != nil || !charged || bal != 14 {
		t.Fatalf("new action: bal=%d charged=%v err=%v", bal, charged, err)
	}
	assertLotLedger(t, store)
}

const getCreditsFixture = `{"balance": 84, "debt": 0, "lots": [{"source": "plan", "amount": 60, "expiresAt": "2026-11-01T00:00:00Z"}, {"source": "campaign", "amount": 24, "expiresAt": "2026-12-31T23:59:59Z"}], "plan": {"tier": "plus", "period": "monthly", "status": "active", "periodEnd": "2026-11-01T00:00:00Z", "autoRenew": true, "pendingTier": null}, "planCredits": {"plus": 300, "pro": 1000}, "costs": {"render": {"mix": 23, "focus": 43, "stems": 63}, "lyrics": {"generate": 2, "rewrite": 1}, "master": 3}, "gift": {"amount": 24, "expiresAt": "2026-12-31T23:59:59Z"}, "storeAccountToken": "3f6c1a52-8d47-4b1e-9a60-2c5d7e91b0a4", "enforced": true}`

func decodeJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The summary equals getCredits.200.json of OpenAPI 1.9.0 key for key.
func TestService_SummaryShowsLotsDebtPlanAndCosts(t *testing.T) {
	planEnd := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	giftEnd := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	token := uuid.MustParse("3f6c1a52-8d47-4b1e-9a60-2c5d7e91b0a4")
	l := &fakeLedger{
		acct: &Account{UserID: "u1", StoreToken: token},
		lots: []Lot{
			{Source: LotPlan, Amount: 60, ExpiresAt: &planEnd},
			{Source: LotCampaign, Amount: 24, ExpiresAt: &giftEnd},
		},
		gift: &Lot{Source: LotCampaign, Amount: 24, ExpiresAt: &giftEnd},
		sub:  &Subscription{Product: "plus.monthly", Status: "aktif", PeriodEnd: planEnd, AutoRenew: true},
	}
	got, err := newTestService(l, true).Summary(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal([]byte(getCreditsFixture), &want); err != nil {
		t.Fatal(err)
	}
	if have := decodeJSON(t, got); !reflect.DeepEqual(have, want) {
		t.Fatalf("summary JSON\n got %v\nwant %v", have, want)
	}
}

func TestService_SummaryDebtLowersBalanceAndEmptyLotsListIsArray(t *testing.T) {
	l := &fakeLedger{acct: &Account{UserID: "u1", Debt: 28, StoreToken: uuid.New()}, lots: []Lot{{Source: LotPack, Amount: 10}}}
	got, err := newTestService(l, true).Summary(context.Background(), "u1")
	if err != nil || got.Balance != -18 || got.Debt != 28 || got.Gift != nil || got.Plan != nil {
		t.Fatalf("summary = %+v, %v", got, err)
	}
	if len(got.Lots) != 1 || got.Lots[0].Source != "pack" || got.Lots[0].ExpiresAt != nil {
		t.Fatalf("lots = %+v", got.Lots)
	}
	empty, err := newTestService(&fakeLedger{}, true).Summary(context.Background(), "u1")
	if err != nil || empty.Lots == nil || len(empty.Lots) != 0 {
		t.Fatalf("empty lots = %#v, %v", empty.Lots, err)
	}
}

func TestService_SummaryGiftIsLatestUnexpiredCampaignLot(t *testing.T) {
	store, u := newStore(t)
	grant(t, store, u, "kampanya", "old", LotCampaign, 10, at(-time.Hour))
	grant(t, store, u, "kampanya", "new", LotCampaign, 50, at(5*day))
	s := newTestService(store, true)
	got, err := s.Summary(context.Background(), u)
	if err != nil || got.Gift == nil || got.Gift.Amount != 50 || !got.Gift.ExpiresAt.Equal(*at(5 * day)) {
		t.Fatalf("gift = %+v, %v", got.Gift, err)
	}
	if got.Balance != 50 || len(got.Lots) != 1 || got.Lots[0].Source != "campaign" {
		t.Fatalf("summary = %+v", got)
	}
	// The gift amount is what the campaign granted: it stays after spending,
	// even when the lot is empty, until the lot expires.
	if _, err := s.Spend(context.Background(), u, OpRenderMix, "r1", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Summary(context.Background(), u); got.Gift == nil || got.Gift.Amount != 50 || got.Balance != 27 {
		t.Fatalf("gift after spend = %+v balance %d", got.Gift, got.Balance)
	}
	if _, err := store.Debit(context.Background(), u, "soz", "s-all", 27, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Summary(context.Background(), u); got.Gift == nil || got.Gift.Amount != 50 || got.Balance != 0 || len(got.Lots) != 0 {
		t.Fatalf("spent gift = %+v summary %+v", got.Gift, got)
	}
	s.now = func() time.Time { return lotNow.Add(6 * day) }
	if got, _ = s.Summary(context.Background(), u); got.Gift != nil || got.Balance != 0 {
		t.Fatalf("expired summary = %+v", got)
	}
}

func TestService_SummaryRefundGraceLotIsNotAGift(t *testing.T) {
	store, u := newStore(t)
	ctx := context.Background()
	grant(t, store, u, "kampanya", "g", LotCampaign, 50, at(day))
	if _, err := store.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	later := lotNow.Add(2 * day) // the gift lot expired
	if _, ok, err := store.Refund(ctx, u, "render", "render_iade", "r1", later, 168*time.Hour); err != nil || !ok {
		t.Fatalf("refund: ok=%v err=%v", ok, err)
	}
	s := newTestService(store, true)
	s.now = func() time.Time { return later }
	got, err := s.Summary(ctx, u)
	if err != nil || got.Balance != 12 || len(got.Lots) != 1 || got.Lots[0].Source != "campaign" {
		t.Fatalf("summary = %+v, %v", got, err)
	}
	if got.Gift != nil {
		t.Fatalf("a refund grace lot was reported as a gift: %+v", got.Gift)
	}
}

func TestService_SummaryMapsPlanStatus(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for tur, want := range map[string]string{"aktif": "active", "odeme_sorunu": "billing_issue", "ek_sure": "grace", "bitti": "expired", "iade": "refunded"} {
		l := &fakeLedger{sub: &Subscription{Product: "pro.yearly", Status: tur, PeriodEnd: end, AutoRenew: true}}
		got, err := newTestService(l, true).Summary(context.Background(), "u1")
		if err != nil || got.Plan == nil || got.Plan.Status != want || got.Plan.Tier != TierPro ||
			got.Plan.Period != "yearly" || !got.Plan.PeriodEnd.Equal(end) {
			t.Fatalf("%s: plan = %+v, %v", tur, got.Plan, err)
		}
	}
}

func TestService_SummaryPendingTier(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	next := "plus.monthly"
	l := &fakeLedger{sub: &Subscription{Product: "pro.monthly", Status: "aktif", PeriodEnd: end, NextProduct: &next}}
	got, err := newTestService(l, true).Summary(context.Background(), "u1")
	if err != nil || got.Plan == nil || got.Plan.PendingTier == nil || *got.Plan.PendingTier != TierPlus {
		t.Fatalf("plan = %+v, %v", got.Plan, err)
	}
}

func TestService_SummaryPlanNilWithoutPeriodEnd(t *testing.T) {
	l := &fakeLedger{sub: &Subscription{Product: "pro.monthly", Status: "aktif"}}
	got, err := newTestService(l, true).Summary(context.Background(), "u1")
	if err != nil || got.Plan != nil {
		t.Fatalf("plan = %+v, %v", got.Plan, err)
	}
}

func TestService_HistoryMapsKindsProjectAndTier(t *testing.T) {
	proj := "3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71"
	tier := "plus"
	l := &fakeLedger{pages: Page{Next: "next", Items: []Movement{
		{ID: 9, Tur: "master", Reason: "master", Amount: -3, ProjectID: &proj},
		{ID: 8, Tur: "render_iade", Reason: "render_focus", Amount: 32, ProjectID: &proj},
		{ID: 7, Tur: "soz", Reason: "lyrics_rewrite", Amount: -1},
		{ID: 6, Tur: "plan_dolum", Amount: 300, Tier: &tier},
		{ID: 5, Tur: "plan_sure", Amount: -18, Tier: &tier},
		{ID: 4, Tur: "kampanya", Amount: 50},
		{ID: 3, Tur: "yonetici", Amount: 5, Reason: "ignored", ProjectID: &proj, Tier: &tier},
	}}}
	p, err := newTestService(l, true).History(context.Background(), "u1", "cur", "")
	if err != nil || p.NextCursor == nil || *p.NextCursor != "next" || l.cursor != "cur" || len(p.Items) != 7 {
		t.Fatalf("page = %+v cursor=%v %v", p, p.NextCursor, err)
	}
	type row struct {
		id, kind, op, project, tier string
	}
	str := func(s *string) string {
		if s == nil {
			return "-"
		}
		return *s
	}
	var got []row
	for _, it := range p.Items {
		got = append(got, row{it.ID, it.Kind, str(it.Operation), str(it.ProjectID), str(it.Tier)})
	}
	want := []row{
		{"mv_9", "master", "master", proj, "-"},
		{"mv_8", "render_refund", "render_focus", proj, "-"},
		{"mv_7", "lyrics", "lyrics_rewrite", "-", "-"},
		{"mv_6", "plan_refill", "-", "-", "plus"},
		{"mv_5", "plan_expire", "-", "-", "plus"},
		{"mv_4", "campaign_gift", "-", "-", "-"},
		{"mv_3", "admin_adjust", "-", "-", "-"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items\n got %v\nwant %v", got, want)
	}
}

func TestService_HistoryFilterSpendEarn(t *testing.T) {
	l := &fakeLedger{}
	s := newTestService(l, true)
	for kind, want := range map[string]HistoryFilter{"": FilterAll, "spend": FilterSpend, "earn": FilterEarn} {
		if _, err := s.History(context.Background(), "u1", "", kind); err != nil || l.filter != want {
			t.Fatalf("kind %q: filter=%q err=%v", kind, l.filter, err)
		}
	}
	l.filter = "untouched"
	for _, kind := range []string{"all", "Spend", "x"} {
		if _, err := s.History(context.Background(), "u1", "", kind); !errors.Is(err, ErrBadFilter) {
			t.Errorf("kind %q: err = %v", kind, err)
		}
	}
	if l.filter != "untouched" {
		t.Fatal("a bad filter reached the ledger")
	}
}

func TestService_HistoryBadCursor(t *testing.T) {
	s := newTestService(&fakeLedger{}, true)
	if _, err := s.History(context.Background(), "u1", "bad", ""); !errors.Is(err, ErrBadCursor) {
		t.Errorf("err = %v", err)
	}
}

func TestService_HasPro(t *testing.T) {
	future := lotNow.Add(10 * day)
	past := lotNow.Add(-day)
	cases := []struct {
		name string
		sub  *Subscription
		lots []Lot
		want bool
	}{
		{"none", nil, nil, false},
		{"plus active", &Subscription{Product: "plus.monthly", Status: "aktif", PeriodEnd: future}, nil, false},
		{"pro active", &Subscription{Product: "pro.monthly", Status: "aktif", PeriodEnd: future}, nil, true},
		{"pro yearly billing issue", &Subscription{Product: "pro.yearly", Status: "odeme_sorunu", PeriodEnd: future}, nil, true},
		{"pro grace with extended lot", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: past}, []Lot{{Source: LotPlan, Amount: 5}}, true},
		{"pro grace with a spent plan lot", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: past}, nil, true},
		{"pro grace with only a refund grace lot", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: past}, []Lot{{Source: LotPlan, Amount: 5, Refund: true}}, true},
		{"pro grace period still open", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: future}, nil, true},
		{"pro grace before its stored end", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: past, GraceEnd: &future}, nil, true},
		{"pro grace past its stored end", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: past.Add(-10 * day), GraceEnd: &past}, nil, false},
		{"pro grace unknown end capped at 16 days", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: lotNow.Add(-17 * day)}, nil, false},
		{"pro grace unknown end inside 16 days", &Subscription{Product: "pro.monthly", Status: "ek_sure", PeriodEnd: lotNow.Add(-15 * day)}, nil, true},
		{"pro grace ended by the store", &Subscription{Product: "pro.monthly", Status: "bitti", PeriodEnd: past}, nil, false},
		{"pro period ended", &Subscription{Product: "pro.monthly", Status: "aktif", PeriodEnd: past}, nil, false},
		{"pro expired", &Subscription{Product: "pro.monthly", Status: "bitti", PeriodEnd: future}, nil, false},
		{"pro refunded", &Subscription{Product: "pro.monthly", Status: "iade", PeriodEnd: future}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService(&fakeLedger{sub: tc.sub, lots: tc.lots}, true)
			got, err := s.HasPro(context.Background(), "u1")
			if err != nil || got != tc.want {
				t.Fatalf("HasPro = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestService_UnknownOperationErrors(t *testing.T) {
	l := &fakeLedger{bal: 100}
	s := newTestService(l, true)
	if _, err := s.Spend(context.Background(), "u1", Operation("nope"), "j", ""); !errors.Is(err, ErrUnknownOperation) {
		t.Fatalf("Spend err = %v", err)
	}
	if _, _, err := s.SpendAction(context.Background(), "u1", Operation("nope"), "a"); !errors.Is(err, ErrUnknownOperation) {
		t.Fatalf("SpendAction err = %v", err)
	}
	if len(l.calls) != 0 {
		t.Fatalf("ledger touched: %v", l.calls)
	}
}

func TestMovementKind_MapsEveryDBKind(t *testing.T) {
	want := map[string]string{
		"render": "render", "render_iade": "render_refund", "soz": "lyrics", "soz_iade": "lyrics_refund",
		"master": "master", "master_iade": "master_refund", "kampanya": "campaign_gift", "paket": "pack",
		"plan_dolum": "plan_refill", "plan_bitis": "plan_end", "plan_sure": "plan_expire",
		"kampanya_sure": "gift_expire", "yonetici": "admin_adjust", "yonetici_sure": "admin_expire",
		"magaza_iade": "store_refund",
	}
	for tur, kind := range want {
		if got := MovementKind(tur); got != kind {
			t.Errorf("MovementKind(%q) = %q, want %q", tur, got, kind)
		}
	}
	if len(movementKinds) != len(want) {
		t.Errorf("movementKinds has %d entries, want %d", len(movementKinds), len(want))
	}
	for src, want := range map[LotSource]string{LotPlan: "plan", LotCampaign: "campaign", LotPack: "pack", LotAdmin: "admin"} {
		if got := src.JSONSource(); got != want {
			t.Errorf("JSONSource(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestService_HasPlan(t *testing.T) {
	future := lotNow.Add(10 * day)
	past := lotNow.Add(-day)
	cases := []struct {
		name string
		sub  *Subscription
		min  Tier
		want bool
	}{
		{"none needs plus", nil, TierPlus, false},
		{"plus covers plus", &Subscription{Product: "plus.monthly", Status: "aktif", PeriodEnd: future}, TierPlus, true},
		{"plus does not cover pro", &Subscription{Product: "plus.monthly", Status: "aktif", PeriodEnd: future}, TierPro, false},
		{"pro covers plus", &Subscription{Product: "pro.yearly", Status: "aktif", PeriodEnd: future}, TierPlus, true},
		{"plus period ended", &Subscription{Product: "plus.monthly", Status: "aktif", PeriodEnd: past}, TierPlus, false},
		{"plus billing issue", &Subscription{Product: "plus.yearly", Status: "odeme_sorunu", PeriodEnd: future}, TierPlus, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService(&fakeLedger{sub: tc.sub}, true)
			got, err := s.HasPlan(context.Background(), "u1", tc.min)
			if err != nil || got != tc.want {
				t.Fatalf("HasPlan(%s) = %v, %v; want %v", tc.min, got, err, tc.want)
			}
		})
	}
}

func TestKnownOperation_RenderGuideUnknown(t *testing.T) {
	l := &fakeLedger{bal: 100}
	s := newTestService(l, true)
	if _, err := s.Spend(context.Background(), "u1", Operation("render_guide"), "job1:rehber", "p1"); !errors.Is(err, ErrUnknownOperation) {
		t.Fatalf("Spend(render_guide) = %v; want ErrUnknownOperation", err)
	}
}

func TestSummary_CostsHaveNoGuide(t *testing.T) {
	got, err := newTestService(&fakeLedger{acct: &Account{UserID: "u1"}}, true).Summary(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got.Costs.Render)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"mix":23,"focus":43,"stems":63}` {
		t.Fatalf("render costs = %s", b)
	}
}
