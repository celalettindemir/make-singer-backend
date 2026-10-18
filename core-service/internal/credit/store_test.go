package credit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newStore(t *testing.T) (*PostgresStore, string) {
	t.Helper()
	pool := testPool(t)
	return NewPostgresStore(pool), uuid.NewString()
}

// seed gives the user a plan lot (plan > 0, ends in 30 days) and a pack lot
// (perm > 0, never ends); with neither it only creates the account.
func seed(t *testing.T, s *PostgresStore, user string, plan, perm int) {
	t.Helper()
	ctx := context.Background()
	if plan > 0 {
		grant(t, s, user, "plan_dolum", "seed-plan-"+user, LotPlan, plan, at(30*day))
	}
	if perm > 0 {
		grant(t, s, user, "paket", "seed-perm-"+user, LotPack, perm, nil)
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

func TestStore_RefundAfterDeleteDoesNotRecreateAccount(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 0, 20)
	ctx := context.Background()
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUserData(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Refund(ctx, u, "render", "render_iade", "r1", lotNow, 168*time.Hour); err != nil || ok {
		t.Fatalf("refund after delete: ok=%v err=%v", ok, err)
	}
	for _, table := range []string{"hesap", "hareket", "dilim"} {
		if n := count(t, s, `SELECT count(*) FROM kredi.`+table+` WHERE kullanici_id = $1::uuid`, u); n != 0 {
			t.Errorf("%s rows = %d, want 0", table, n)
		}
	}
}

func TestStore_SpendActionFollowUpsFree(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	c, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, lotNow)
	if err != nil || !c.Charged || c.Balance != 8 {
		t.Fatalf("first: %+v %v", c, err)
	}
	for i := 2; i <= 12; i++ {
		c, err = s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, lotNow)
		if err != nil || c.Charged || c.Balance != 8 {
			t.Fatalf("call %d: %+v %v", i, c, err)
		}
	}
	if _, err = s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, lotNow); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("call 13: %v", err)
	}
	var reason string
	if err := s.pool.QueryRow(ctx, `SELECT sebep FROM kredi.hareket WHERE tur = 'soz' AND ref = $1`, u+":act").Scan(&reason); err != nil || reason != "lyrics_generate" {
		t.Fatalf("movement reason = %q, %v", reason, err)
	}
}

func TestStore_SpendActionInsufficientWritesNothing(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 0, 1)
	_, err := s.SpendAction(context.Background(), u, OpLyricsGenerate, "act", 2, time.Hour, 12, lotNow)
	var ie *InsufficientError
	if !errors.As(err, &ie) || ie.Required != 2 || ie.Balance != 1 {
		t.Fatalf("got %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.eylem WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Errorf("action rows = %d, want 0", n)
	}
}

func TestStore_SpendActionExpiresAfterWindow(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsRewrite, "act", 1, 10*time.Minute, 2, lotNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE kredi.eylem SET olusturuldu = now() - interval '11 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SpendAction(ctx, u, OpLyricsRewrite, "act", 1, 10*time.Minute, 2, lotNow); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestStore_SpendActionOtherUserCannotReuse(t *testing.T) {
	s, u := newStore(t)
	other := uuid.NewString()
	seed(t, s, u, 0, 10)
	seed(t, s, other, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, lotNow); err != nil {
		t.Fatal(err)
	}
	c, err := s.SpendAction(ctx, other, OpLyricsGenerate, "act", 2, time.Hour, 12, lotNow)
	if err != nil || !c.Charged || c.Balance != 8 {
		t.Fatalf("other user: %+v %v", c, err)
	}
}

func TestStore_DropActionThenReuseExpired(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, lotNow); err != nil {
		t.Fatal(err)
	}
	if err := s.DropAction(ctx, u+":act"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, lotNow); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("got %v", err)
	}
	if b := balance(t, s, u, lotNow); b != 8 {
		t.Fatalf("balance = %d, want 8 (no second charge)", b)
	}
}

func TestStore_RefundSozClosesAction(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 0, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, lotNow); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Refund(ctx, u, "soz", "soz_iade", u+":act", lotNow, 168*time.Hour); err != nil || !ok {
		t.Fatalf("refund: ok=%v err=%v", ok, err)
	}
	if b := balance(t, s, u, lotNow); b != 10 {
		t.Fatalf("balance = %d, want 10", b)
	}
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, time.Hour, 12, lotNow); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestStore_HistoryPagesByCursor(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	for i := range 25 {
		grant(t, s, u, "paket", "h"+string(rune('a'+i)), LotPack, 1, nil)
	}
	p1, err := s.History(ctx, u, "", FilterAll)
	if err != nil || len(p1.Items) != 20 || p1.Next == "" {
		t.Fatalf("page 1: %d %q %v", len(p1.Items), p1.Next, err)
	}
	p2, err := s.History(ctx, u, p1.Next, FilterAll)
	if err != nil || len(p2.Items) != 5 || p2.Next != "" || p2.Items[0].ID >= p1.Items[19].ID {
		t.Fatalf("page 2: %d %q %v", len(p2.Items), p2.Next, err)
	}
	if _, err := s.History(ctx, u, "zzz", FilterAll); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err := s.History(ctx, u, "", HistoryFilter("all")); !errors.Is(err, ErrBadFilter) {
		t.Fatalf("bad filter: %v", err)
	}
}

func TestStore_HistoryFilterSplitsSpendAndEarn(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "paket", "b", LotPack, 100, nil)
	if _, err := s.Debit(ctx, u, "render", "r1", 12, Meta{Reason: "render_mix", ProjectID: "3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71"}, lotNow); err != nil {
		t.Fatal(err)
	}
	spend, err := s.History(ctx, u, "", FilterSpend)
	if err != nil || len(spend.Items) != 1 || spend.Items[0].Tur != "render" || spend.Items[0].Amount != -12 ||
		spend.Items[0].Reason != "render_mix" || spend.Items[0].ProjectID == nil {
		t.Fatalf("spend: %+v %v", spend, err)
	}
	earn, err := s.History(ctx, u, "", FilterEarn)
	if err != nil || len(earn.Items) != 1 || earn.Items[0].Tur != "paket" || earn.Items[0].Amount != 100 {
		t.Fatalf("earn: %+v %v", earn, err)
	}
}

func TestStore_RefundCopiesProjectAndReason(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	grant(t, s, u, "paket", "b", LotPack, 100, nil)
	const project = "3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71"
	if _, err := s.Debit(ctx, u, "master", "m1", 3, Meta{Reason: "master", ProjectID: project}, lotNow); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Refund(ctx, u, "master", "master_iade", "m1", lotNow, 168*time.Hour); err != nil || !ok {
		t.Fatalf("refund: ok=%v err=%v", ok, err)
	}
	var reason, proj string
	if err := s.pool.QueryRow(ctx, `SELECT sebep, proje_id::text FROM kredi.hareket WHERE tur = 'master_iade'`).Scan(&reason, &proj); err != nil ||
		reason != "master" || proj != project {
		t.Fatalf("refund row: %q %q %v", reason, proj, err)
	}
}

func TestStore_DeleteUserDataAnonymises(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 20, 10)
	ctx := context.Background()
	if _, err := s.SpendAction(ctx, u, OpLyricsGenerate, "act", 2, 10*time.Minute, 12, lotNow); err != nil {
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
	for _, table := range []string{"hesap", "hareket", "eylem", "dilim"} {
		if n := count(t, s, `SELECT count(*) FROM kredi.`+table+` WHERE kullanici_id = $1::uuid`, u); n != 0 {
			t.Errorf("%s rows = %d, want 0", table, n)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.dilim_hareket dh WHERE NOT EXISTS (SELECT 1 FROM kredi.dilim d WHERE d.id = dh.dilim_id)`); n != 0 {
		t.Errorf("orphan lot movements = %d, want 0", n)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.satin_alma WHERE islem_id = 't1-`+u+`' AND kullanici_id IS NULL`); n != 1 {
		t.Errorf("anonymised purchases = %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.abonelik WHERE orijinal_islem_id = 'o1-`+u+`' AND kullanici_id IS NULL`); n != 1 {
		t.Errorf("anonymised subscriptions = %d, want 1", n)
	}
}

func TestStore_ReadAccountMissingCreatesNothing(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	if _, ok, err := s.ReadAccount(ctx, u); err != nil || ok {
		t.Fatalf("missing account: ok=%v err=%v", ok, err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Fatalf("ReadAccount created %d rows", n)
	}
	setDebt(t, s, u, 7)
	a, ok, err := s.ReadAccount(ctx, u)
	if err != nil || !ok || a.Debt != 7 {
		t.Fatalf("existing account: %+v ok=%v err=%v", a, ok, err)
	}
}

func TestStore_GiftIsLatestLiveCampaignLot(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	if g, err := s.Gift(ctx, u, lotNow); err != nil || g != nil {
		t.Fatalf("no gift: %+v %v", g, err)
	}
	grant(t, s, u, "kampanya", "old", LotCampaign, 10, at(-time.Hour))
	grant(t, s, u, "kampanya", "new", LotCampaign, 50, at(5*day))
	g, err := s.Gift(ctx, u, lotNow)
	if err != nil || g == nil || g.Amount != 50 || g.ExpiresAt == nil || !g.ExpiresAt.Equal(*at(5 * day)) {
		t.Fatalf("gift: %+v %v", g, err)
	}
	if g, err := s.Gift(ctx, u, lotNow.Add(6*day)); err != nil || g != nil {
		t.Fatalf("expired gift: %+v %v", g, err)
	}
}

func TestStore_MovementOwner(t *testing.T) {
	s, u := newStore(t)
	grant(t, s, u, "paket", "b", LotPack, 100, nil)
	if owner, ok, err := s.MovementOwner(context.Background(), "paket", "b"); err != nil || !ok || owner != u {
		t.Fatalf("owner = %q ok=%v err=%v", owner, ok, err)
	}
	if _, ok, err := s.MovementOwner(context.Background(), "paket", "nope"); err != nil || ok {
		t.Fatalf("missing: ok=%v err=%v", ok, err)
	}
}

func TestStore_RefundWithoutAccountRowWritesNothing(t *testing.T) {
	s, u := newStore(t)
	seed(t, s, u, 0, 20)
	ctx := context.Background()
	if _, err := s.Debit(ctx, u, "render", "orphan-"+u, 12, Meta{}, lotNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Refund(ctx, u, "render", "render_iade", "orphan-"+u, lotNow, 168*time.Hour); err != nil || ok {
		t.Fatalf("refund: ok=%v err=%v", ok, err)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hesap WHERE kullanici_id = $1::uuid`, u); n != 0 {
		t.Errorf("account row recreated: %d", n)
	}
	if n := count(t, s, `SELECT count(*) FROM kredi.hareket WHERE tur = 'render_iade' AND ref = $1`, "orphan-"+u); n != 0 {
		t.Errorf("refund rows = %d, want 0", n)
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
