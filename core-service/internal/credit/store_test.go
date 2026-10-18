package credit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newStore(t *testing.T) (*PostgresStore, string) {
	t.Helper()
	pool := testPool(t)
	return NewPostgresStore(pool), uuid.NewString()
}

// seed sets the buckets directly through the ledger.
func seed(t *testing.T, s *PostgresStore, user string, plan, perm int) {
	t.Helper()
	ctx := context.Background()
	if plan > 0 {
		if _, _, err := s.SetPlan(ctx, user, "plan_dolum", "seed-plan-"+user, plan, nil); err != nil {
			t.Fatalf("seed plan: %v", err)
		}
	}
	if perm > 0 {
		if _, _, err := s.Credit(ctx, user, "paket", "seed-perm-"+user, "", perm); err != nil {
			t.Fatalf("seed permanent: %v", err)
		}
	}
	if plan == 0 && perm == 0 {
		if _, err := s.EnsureAccount(ctx, user); err != nil {
			t.Fatalf("seed account: %v", err)
		}
	}
}

func count(t *testing.T, s *PostgresStore, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestStore_DebitSameRefOnce(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 20)
	ctx := context.Background()
	a, done, err := s.DebitBucket(ctx, u, "render", "r1", "render_mix", 5, false)
	if err != nil || !done || a.Balance() != 15 {
		t.Fatalf("first debit: %+v %v %v", a, done, err)
	}
	a, done, err = s.DebitBucket(ctx, u, "render", "r1", "render_mix", 5, false)
	if err != nil || done || a.Balance() != 15 {
		t.Fatalf("second debit: %+v %v %v", a, done, err)
	}
}

func TestStore_CreditSameRefOnce(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	ctx := context.Background()
	if _, done, err := s.Credit(ctx, u, "paket", "p1", "", 100); err != nil || !done {
		t.Fatalf("first credit: %v %v", done, err)
	}
	a, done, err := s.Credit(ctx, u, "paket", "p1", "", 100)
	if err != nil || done || a.Permanent != 100 {
		t.Fatalf("second credit: %+v %v %v", a, done, err)
	}
}

func TestStore_DebitTakesPlanFirst(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 5, 20)
	a, _, err := s.DebitBucket(context.Background(), u, "render", "r1", "render_mix", 12, false)
	if err != nil || a.Plan != 0 || a.Permanent != 13 {
		t.Fatalf("got %+v %v", a, err)
	}
}

func TestStore_DebitInsufficient(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 2, 5)
	_, _, err := s.DebitBucket(context.Background(), u, "render", "r1", "render_mix", 12, false)
	var ie *InsufficientError
	if !errors.As(err, &ie) || ie.Required != 12 || ie.Balance != 7 {
		t.Fatalf("got %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'render'`); n != 0 {
		t.Errorf("render rows = %d, want 0", n)
	}
}

func TestStore_ConcurrentDebitsOneSucceeds(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 12)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = s.DebitBucket(context.Background(), u, "render", "r"+string(rune('a'+i)), "render_mix", 12, false)
		}()
	}
	wg.Wait()
	insufficient := 0
	for _, err := range errs {
		var ie *InsufficientError
		if errors.As(err, &ie) {
			insufficient++
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if insufficient != 1 {
		t.Fatalf("insufficient errors = %d, want 1", insufficient)
	}
}

func TestStore_RefundRestoresBuckets(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 5, 20)
	ctx := context.Background()
	if _, _, err := s.DebitBucket(ctx, u, "render", "r1", "render_mix", 12, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundBucket(ctx, "render", "render_iade", "r1"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.EnsureAccount(ctx, u)
	if a.Plan != 5 || a.Permanent != 20 {
		t.Fatalf("got %+v", a)
	}
}

func TestStore_RefundTwiceOnce(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 20)
	ctx := context.Background()
	if _, _, err := s.DebitBucket(ctx, u, "render", "r1", "render_mix", 12, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.RefundBucket(ctx, "render", "render_iade", "r1"); err != nil {
			t.Fatal(err)
		}
	}
	if a, _ := s.EnsureAccount(ctx, u); a.Balance() != 20 {
		t.Fatalf("balance = %d, want 20", a.Balance())
	}
}

func TestStore_RefundAfterDeleteDoesNotRecreateAccount(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 20)
	ctx := context.Background()
	if _, _, err := s.DebitBucket(ctx, u, "render", "r1", "render_mix", 12, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUserData(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundBucket(ctx, "render", "render_iade", "r1"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Errorf("hesap rows = %d, want 0", n)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Errorf("hareket rows = %d, want 0", n)
	}
}

func TestStore_SetPlanIsIdempotentSet(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	ctx := context.Background()
	end := time.Now().Add(30 * 24 * time.Hour)
	if _, _, err := s.SetPlan(ctx, u, "plan_dolum", "a", 300, &end); err != nil {
		t.Fatal(err)
	}
	a, done, err := s.SetPlan(ctx, u, "plan_dolum", "b", 300, &end)
	if err != nil || !done || a.Plan != 300 || a.PlanPeriodEnd == nil {
		t.Fatalf("got %+v %v %v", a, done, err)
	}
	a, _, err = s.SetPlan(ctx, u, "plan_bitis", "c", 0, nil)
	if err != nil || a.Plan != 0 {
		t.Fatalf("expiry: %+v %v", a, err)
	}
}

func TestStore_ClawBackGoesNegative(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 30)
	a, done, err := s.ClawBack(context.Background(), u, "iade1", 100)
	if err != nil || !done || a.Permanent != -70 {
		t.Fatalf("got %+v %v %v", a, done, err)
	}
}

func TestStore_NegativeBalanceBlocksDebit(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 30)
	ctx := context.Background()
	if _, _, err := s.ClawBack(ctx, u, "iade1", 100); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.DebitBucket(ctx, u, "render", "r1", "render_mix", 1, false)
	var ie *InsufficientError
	if !errors.As(err, &ie) || ie.Balance != -70 {
		t.Fatalf("got %v", err)
	}
}

func TestStore_SpendActionFollowUpsFree(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	c, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, false)
	if err != nil || !c.Charged || c.Account.Balance() != 8 {
		t.Fatalf("first: %+v %v", c, err)
	}
	for i := 2; i <= 12; i++ {
		c, err = s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, false)
		if err != nil || c.Charged || c.Account.Balance() != 8 {
			t.Fatalf("call %d: %+v %v", i, c, err)
		}
	}
	if _, err = s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, false); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("call 13: %v", err)
	}
}

func TestStore_SpendActionExpiresAfterWindow(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsRewrite, "act", 1, 10*time.Minute, 2, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE kredi.eylem SET olusturuldu = now() - interval '11 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SpendAction(ctx, u, OpLyricsRewrite, "act", 1, 10*time.Minute, 2, false); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestStore_SpendActionOtherUserCannotReuse(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	other := uuid.NewString()
	seed(t, s, u, 0, 10)
	seed(t, s, other, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, false); err != nil {
		t.Fatal(err)
	}
	c, err := s.SpendAction(ctx, other, OpLyricsGenerate, "act", 2, time.Hour, 12, false)
	if err != nil || !c.Charged || c.Account.Balance() != 8 {
		t.Fatalf("other user: %+v %v", c, err)
	}
}

func TestStore_DropActionThenReuseExpired(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DropAction(ctx, u+":act"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, false); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("got %v", err)
	}
	if a, _ := s.EnsureAccount(ctx, u); a.Balance() != 8 {
		t.Fatalf("balance = %d, want 8 (no second charge)", a.Balance())
	}
}

func TestStore_RefundSozClosesAction(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundBucket(ctx, "soz", "soz_iade", u+":act"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.EnsureAccount(ctx, u); a.Balance() != 10 {
		t.Fatalf("balance = %d, want 10", a.Balance())
	}
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, false); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestStore_HistoryPagesByID(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	ctx := context.Background()
	for i := range 25 {
		if _, _, err := s.Credit(ctx, u, "paket", "h"+string(rune('a'+i)), "", 1); err != nil {
			t.Fatal(err)
		}
	}
	p1, err := s.History(ctx, u, 0, 20)
	if err != nil || len(p1) != 20 {
		t.Fatalf("page 1: %d %v", len(p1), err)
	}
	p2, err := s.History(ctx, u, p1[19].ID, 20)
	if err != nil || len(p2) != 5 || p2[0].ID >= p1[19].ID {
		t.Fatalf("page 2: %d %v", len(p2), err)
	}
}

func TestStore_DeleteUserDataAnonymises(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO kredi.satin_alma (magaza, islem_id, kullanici_id, urun, kredi, durum, ortam)
		VALUES ('apple', 't1-`+u+`', $1::uuid, 'credits.100', 100, 'ok', 'sandbox')`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO kredi.abonelik (kullanici_id, magaza, orijinal_islem_id, urun, durum, donem_sonu, ortam)
		VALUES ($1::uuid, 'apple', 'o1-`+u+`', 'pro.monthly', 'aktif', now(), 'sandbox')`, u); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // the second run must be a no-op, not an error
		if err := s.DeleteUserData(ctx, u); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	for _, table := range []string{"hesap", "hareket", "eylem"} {
		if n := count(t, s, `SELECT count(*) FROM kredi.`+table+` WHERE kullanici_id = $1::uuid`, u); n != 0 {
			t.Errorf("%s rows = %d, want 0", table, n)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 't1-`+u+`' AND kullanici_id IS NULL`); n != 1 {
		t.Errorf("anonymised purchases = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.abonelik WHERE orijinal_islem_id = 'o1-`+u+`' AND kullanici_id IS NULL`); n != 1 {
		t.Errorf("anonymised subscriptions = %d, want 1", n)
	}
}

func TestStore_ReadAccountMissingCreatesNothing(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	ctx := context.Background()
	if _, ok, err := s.ReadAccount(ctx, u); err != nil || ok {
		t.Fatalf("missing account: ok=%v err=%v", ok, err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Fatalf("ReadAccount created %d rows", n)
	}
	seed(t, s, u, 0, 7)
	a, ok, err := s.ReadAccount(ctx, u)
	if err != nil || !ok || a.Balance() != 7 {
		t.Fatalf("existing account: %+v ok=%v err=%v", a, ok, err)
	}
}

func TestStore_LatestSubscription(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	if sub, err := s.LatestSubscription(ctx, u); err != nil || sub != nil {
		t.Fatalf("no subscription: %+v %v", sub, err)
	}
	old, latest := "old-"+u, "new-"+u
	for id, end := range map[string]string{old: "2026-01-01T00:00:00Z", latest: "2026-09-01T00:00:00Z"} {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO kredi.abonelik (kullanici_id, magaza, orijinal_islem_id, urun, durum, donem_sonu, ortam)
			VALUES ($1::uuid, 'apple', $2, 'pro.monthly', 'aktif', $3::timestamptz, 'production')`, u, id, end); err != nil {
			t.Fatal(err)
		}
	}
	sub, err := s.LatestSubscription(ctx, u)
	if err != nil || sub == nil || sub.OriginalID != latest || sub.Product != "pro.monthly" ||
		sub.Status != "aktif" || sub.UserID == nil || *sub.UserID != u || !sub.AutoRenew {
		t.Fatalf("latest subscription: %+v %v", sub, err)
	}
}

func TestStore_RefundWithoutAccountRowWritesNothing(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	seed(t, s, u, 0, 20)
	ctx := context.Background()
	if _, _, err := s.DebitBucket(ctx, u, "render", "orphan-"+u, "render_mix", 12, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); err != nil {
		t.Fatal(err)
	}
	if err := s.RefundBucket(ctx, "render", "render_iade", "orphan-"+u); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Errorf("account row recreated: %d", n)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'render_iade' AND ref = $1`, "orphan-"+u); n != 0 {
		t.Errorf("refund rows = %d, want 0", n)
	}
}
