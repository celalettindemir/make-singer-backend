package credit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/identity"
)

type adminEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store *PostgresStore
	camp  *Campaigns
	a     *Admin
	tag   string
	mu    sync.Mutex
	now   time.Time
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	pool := testPool(t)
	if err := identity.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	e := &adminEnv{t: t, pool: pool, tag: strings.ReplaceAll(uuid.NewString(), "-", "")[:10],
		now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	e.store = NewPostgresStore(pool)
	clock := func() time.Time {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.now
	}
	e.camp = NewCampaigns(e.store, clock)
	e.a = NewAdmin(e.store, e.store, identity.NewDirectory(pool), clock)
	return e
}

func (e *adminEnv) setNow(t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = t
}

func (e *adminEnv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// user inserts an identity row; the e-mail carries the test tag so searches
// are isolated from the other tests sharing the users table.
func (e *adminEnv) user(name string, createdAt time.Time) (id, email string) {
	e.t.Helper()
	id = uuid.NewString()
	email = fmt.Sprintf("%s-%s@ornek.com", name, e.tag)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at) VALUES ($1, $2, true, $3, 'x', $4)`,
		id, email, name, createdAt); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id, email
}

func (e *adminEnv) campaign(quota, perUser, days int, endsAt *time.Time) Campaign {
	e.t.Helper()
	c, _, err := e.camp.Create(context.Background(),
		NewCampaign{Name: "Kampanya", Quota: quota, PerUser: perUser, ValidDays: days, EndsAt: endsAt}, "admin@ornek.com")
	if err != nil {
		e.t.Fatalf("Create: %v", err)
	}
	return c
}

func (e *adminEnv) join(id, email string) {
	e.camp.GrantSignupGift(context.Background(), identity.NewUser{ID: id, Email: email, CreatedAt: e.clock()})
}

func adminDay(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestAdmin_CreateCampaignValidates(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	ok := NewCampaign{Name: strings.Repeat("a", 80), Quota: 1000000, PerUser: 10000, ValidDays: 3650}
	if _, _, err := e.camp.Create(ctx, ok, "a@b.c"); err != nil {
		t.Fatalf("the maximum values must pass: %v", err)
	}
	for field, in := range map[string]NewCampaign{
		"name":      {Name: strings.Repeat("a", 81), Quota: 1, PerUser: 1, ValidDays: 1},
		"quota":     {Name: "x", Quota: 1000001, PerUser: 1, ValidDays: 1},
		"perUser":   {Name: "x", Quota: 1, PerUser: 10001, ValidDays: 1},
		"validDays": {Name: "x", Quota: 1, PerUser: 1, ValidDays: 3651},
		"endsAt":    {Name: "x", Quota: 1, PerUser: 1, ValidDays: 1, EndsAt: adminDay(2026, 10, 17)},
	} {
		_, _, err := e.camp.Create(ctx, in, "a@b.c")
		var fe *FieldError
		if !errors.Is(err, ErrCampaignInvalid) || !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: err = %v, want ErrCampaignInvalid on that field", field, err)
		}
	}
	// "Today" is the Istanbul day: at 22:00 UTC it is already the 19th there.
	e.setNow(time.Date(2026, 10, 18, 22, 0, 0, 0, time.UTC))
	if _, _, err := e.camp.Create(ctx, NewCampaign{Name: "x", Quota: 1, PerUser: 1, ValidDays: 1, EndsAt: adminDay(2026, 10, 18)}, "a@b.c"); !errors.Is(err, ErrCampaignInvalid) {
		t.Errorf("18 Oct at 01:00 on 19 Oct (Istanbul): err = %v, want invalid", err)
	}
	if _, _, err := e.camp.Create(ctx, NewCampaign{Name: "x", Quota: 1, PerUser: 1, ValidDays: 1, EndsAt: adminDay(2026, 10, 19)}, "a@b.c"); err != nil {
		t.Errorf("19 Oct: %v", err)
	}
}

func TestAdmin_CampaignEndsAtIstanbulDay(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	e.setNow(time.Date(2031, 12, 30, 9, 0, 0, 0, time.UTC))
	c := e.campaign(10, 5, 30, adminDay(2031, 12, 31))

	// 31 Dec 23:59 in Istanbul is 20:59 UTC: still gives gifts, not ended.
	e.setNow(time.Date(2031, 12, 31, 20, 59, 0, 0, time.UTC))
	id, email := e.user("son", e.clock())
	e.join(id, email)
	if ended, _, err := e.camp.Sweep(ctx, e.clock()); err != nil || ended != 0 {
		t.Fatalf("Sweep at 23:59 Istanbul: ended = %d, %v; want 0", ended, err)
	}
	if lots, _ := e.store.Lots(ctx, id, e.clock()); len(lots) != 1 {
		t.Fatalf("a signup at 23:59 Istanbul must get the gift, lots = %+v", lots)
	}

	// 1 Jan 00:00 in Istanbul is 21:00 UTC (still 31 Dec in UTC): over.
	e.setNow(time.Date(2031, 12, 31, 21, 0, 0, 0, time.UTC))
	id2, email2 := e.user("gec", e.clock())
	e.join(id2, email2)
	if lots, _ := e.store.Lots(ctx, id2, e.clock()); len(lots) != 0 {
		t.Fatalf("a signup after midnight Istanbul must get nothing, lots = %+v", lots)
	}
	ended, _, err := e.camp.Sweep(ctx, e.clock())
	if err != nil || ended != 1 {
		t.Fatalf("Sweep at 00:00 Istanbul: ended = %d, %v; want 1", ended, err)
	}
	d, err := e.camp.Get(ctx, c.ID)
	if err != nil || d.Status != CampaignEnded || d.Joined != 1 {
		t.Fatalf("campaign = %+v, %v", d.Campaign, err)
	}
}

func TestAdmin_ListShowsSummaryAndActive(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	o, err := e.camp.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v := o.View()
	if v.Summary.Active != nil || v.Summary.RemainingQuota != nil || len(v.Items) != 0 {
		t.Fatalf("empty list view = %+v", v)
	}
	raw, _ := json.Marshal(v)
	if !strings.Contains(string(raw), `"active":null`) || !strings.Contains(string(raw), `"remainingQuota":null`) || !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("empty list JSON = %s", raw)
	}

	first := e.campaign(5, 10, 30, nil)
	e.setNow(e.clock().Add(time.Hour))
	second, replaced, err := e.camp.Create(ctx, NewCampaign{Name: "Kis", Quota: 4, PerUser: 50, ValidDays: 75, EndsAt: adminDay(2026, 12, 31)}, "admin@ornek.com")
	if err != nil || replaced == nil || replaced.ID != first.ID || replaced.Status != CampaignStopped {
		t.Fatalf("Create = %+v, %+v, %v", second, replaced, err)
	}
	for _, n := range []string{"a", "b"} {
		id, email := e.user(n, e.clock())
		e.join(id, email)
	}
	o, _ = e.camp.List(ctx)
	v = o.View()
	if v.Summary.Active == nil || v.Summary.Active.ID != second.ID || v.Summary.Active.Joined != 2 ||
		v.Summary.Active.Status != "active" || *v.Summary.Active.EndsAt != "2026-12-31" {
		t.Fatalf("active = %+v", v.Summary.Active)
	}
	if v.Summary.RemainingQuota == nil || v.Summary.RemainingQuota.People != 2 || v.Summary.RemainingQuota.Credits != 100 {
		t.Fatalf("remainingQuota = %+v", v.Summary.RemainingQuota)
	}
	if v.Summary.DistributedThisMonth.Users != 2 || v.Summary.DistributedThisMonth.Credits != 100 {
		t.Fatalf("distributedThisMonth = %+v", v.Summary.DistributedThisMonth)
	}
	if len(v.Items) != 2 || v.Items[0].ID != second.ID || v.Items[1].Status != "stopped" || v.Items[1].StoppedAt == nil {
		t.Fatalf("items = %+v", v.Items)
	}
	if v.Items[1].EndsAt != nil {
		t.Fatalf("a campaign without a date shows endsAt %v", *v.Items[1].EndsAt)
	}
}

func TestAdmin_CampaignDetailUsedPercent(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	c := e.campaign(30, 8, 30, nil)
	e.setNow(e.clock().Add(time.Minute))
	id, email := e.user("kul", e.clock())
	e.join(id, email)
	if _, err := e.store.Debit(ctx, id, "render", "job-pct", 1, Meta{}, e.clock()); err != nil {
		t.Fatal(err)
	}
	d, err := e.camp.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Distributed != 8 || d.Used != 1 || d.UsedPercent != 13 { // 12.5 rounds up
		t.Fatalf("detail = %+v, want 8 given, 1 used, 13%%", d)
	}
	if _, err := e.store.Debit(ctx, id, "render", "job-pct2", 7, Meta{}, e.clock()); err != nil {
		t.Fatal(err)
	}
	d, _ = e.camp.Get(ctx, c.ID)
	if d.UsedPercent != 100 {
		t.Fatalf("usedPercent = %d, want 100", d.UsedPercent)
	}
	// Only the last 20 people, newest first.
	for i := 0; i < 22; i++ {
		e.setNow(e.clock().Add(time.Second))
		uid, mail := e.user(fmt.Sprintf("m%02d", i), e.clock())
		e.join(uid, mail)
	}
	d, _ = e.camp.Get(ctx, c.ID)
	view := d.View()
	if len(view.RecentJoins) != 20 || view.RecentJoins[0].Amount != 8 || view.RecentJoins[0].ShortID == "" ||
		!view.RecentJoins[0].JoinedAt.After(view.RecentJoins[1].JoinedAt) {
		t.Fatalf("recentJoins = %+v", view.RecentJoins)
	}
}

func TestAdmin_StopTwiceIs409(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	c := e.campaign(5, 5, 5, nil)
	stopped, err := e.camp.Stop(ctx, c.ID)
	if err != nil || stopped.Status != CampaignStopped || stopped.StoppedAt == nil {
		t.Fatalf("Stop = %+v, %v", stopped, err)
	}
	if _, err := e.camp.Stop(ctx, c.ID); !errors.Is(err, ErrCampaignNotActive) {
		t.Fatalf("second Stop err = %v, want ErrCampaignNotActive", err)
	}
	if _, err := e.camp.Stop(ctx, c.ID+1000); !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("unknown Stop err = %v", err)
	}
}

func TestAdmin_UserSearchByEmailShortIdUUID(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	idA, emailA := e.user("Ayse", base)
	idB, _ := e.user("Mert", base.Add(time.Hour))
	if _, err := e.store.Grant(ctx, idA, turYonetici, uuid.NewString(), Grant{Source: LotAdmin, Amount: 40}, e.clock()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO kredi.abonelik (magaza, orijinal_islem_id, kullanici_id, urun, durum, donem_sonu, ortam)
		 VALUES ('apple', $1, $2, 'plus.monthly', 'aktif', $3, 'production')`,
		uuid.NewString(), idA, e.clock().AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}

	// Email substring, case-insensitive; newest account first.
	page, err := e.a.Users(ctx, strings.ToUpper(e.tag), "")
	if err != nil || len(page.Items) != 2 || page.Items[0].UserID != idB || page.Items[1].UserID != idA || page.NextCursor != nil {
		t.Fatalf("email search = %+v, %v", page, err)
	}
	a := page.Items[1]
	if a.Email != emailA || a.Balance != 40 || a.ShortID != ShortID(idA) || a.Plan == nil || a.Plan.Tier != TierPlus || a.Plan.Status != "active" {
		t.Fatalf("item A = %+v plan %+v", a, a.Plan)
	}
	if page.Items[0].Plan != nil || page.Items[0].Balance != 0 || page.Items[0].CampaignName != nil {
		t.Fatalf("item B = %+v", page.Items[0])
	}

	// Short id with and without '#', lower or upper case.
	for _, q := range []string{"#" + ShortID(idA), ShortID(idA), strings.ToLower(ShortID(idA)), "#" + ShortID(idA)[:4]} {
		page, err = e.a.Users(ctx, q, "")
		found := false
		for _, it := range page.Items {
			found = found || it.UserID == idA
		}
		if err != nil || !found {
			t.Errorf("short id search %q: found=%v err=%v", q, found, err)
		}
	}
	// Full UUID: exactly that user.
	page, err = e.a.Users(ctx, idA, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].UserID != idA {
		t.Fatalf("uuid search = %+v, %v", page, err)
	}
	// Too short, and a '#' search never falls back to e-mail text.
	for _, q := range []string{"ab", "#ab", " a "} {
		if _, err := e.a.Users(ctx, q, ""); !errors.Is(err, ErrQueryTooShort) {
			t.Errorf("Users(%q) err = %v, want ErrQueryTooShort", q, err)
		}
	}
	page, err = e.a.Users(ctx, "#"+e.tag, "") // not hex -> nothing
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("'#' with text = %+v, %v", page, err)
	}
	if _, err := e.a.Users(ctx, e.tag, "%%%"); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("bad cursor err = %v", err)
	}

	// Detail: lots, plan, movements; unknown and malformed ids are not found.
	d, err := e.a.User(ctx, idA)
	if err != nil || d.User.UserID != idA || d.Debt != 0 || len(d.Lots) != 1 || d.Lots[0].Source != "admin" ||
		d.Plan == nil || len(d.Movements) != 1 || d.Movements[0].Kind != "admin_adjust" {
		t.Fatalf("User = %+v, %v", d, err)
	}
	for _, id := range []string{uuid.NewString(), "nope"} {
		if _, err := e.a.User(ctx, id); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("User(%q) err = %v", id, err)
		}
	}
}

func TestAdmin_UsersPagesAndHidesDeleted(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < 23; i++ {
		id, _ := e.user(fmt.Sprintf("p%02d", i), base.Add(time.Duration(i)*time.Minute))
		ids = append(ids, id)
	}
	first, err := e.a.Users(ctx, e.tag, "")
	if err != nil || len(first.Items) != 20 || first.NextCursor == nil || first.Items[0].UserID != ids[22] {
		t.Fatalf("first page = %d items next=%v err=%v", len(first.Items), first.NextCursor, err)
	}
	second, err := e.a.Users(ctx, e.tag, *first.NextCursor)
	if err != nil || len(second.Items) != 3 || second.NextCursor != nil || second.Items[2].UserID != ids[0] {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	// A deleted user (row gone) is not listed and has no detail.
	if _, err := e.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ids[22]); err != nil {
		t.Fatal(err)
	}
	page, _ := e.a.Users(ctx, e.tag, "")
	for _, it := range page.Items {
		if it.UserID == ids[22] {
			t.Fatal("a deleted user was listed")
		}
	}
	if _, err := e.a.User(ctx, ids[22]); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("deleted user detail err = %v", err)
	}
	if _, err := e.a.Adjust(ctx, ids[22], Adjustment{Amount: 5, Note: "deneme", RequestID: uuid.NewString()}, "a@b.c"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("deleted user adjust err = %v", err)
	}
}

func TestAdmin_AdjustAddWithExpiryAndAudit(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	id, email := e.user("Ayse", e.clock())
	days := 30
	res, err := e.a.Adjust(ctx, id, Adjustment{Amount: 25, Note: "  Destek talebi 4521  ", ValidDays: &days, RequestID: uuid.NewString()}, "admin@ornek.com")
	if err != nil {
		t.Fatal(err)
	}
	m := res.Movement
	if res.Balance != 25 || m.Kind != "admin_adjust" || m.Amount != 25 || m.UserID != id || m.ShortID != ShortID(id) ||
		m.Email == nil || *m.Email != email || m.By == nil || *m.By != "admin@ornek.com" ||
		m.Note == nil || *m.Note != "Destek talebi 4521" || m.Operation != nil || m.ProjectID != nil || m.Tier != nil || m.CampaignID != nil {
		t.Fatalf("result = %+v", res)
	}
	lots, _ := e.store.Lots(ctx, id, e.clock())
	want := e.clock().AddDate(0, 0, 30)
	if len(lots) != 1 || lots[0].Source != LotAdmin || lots[0].ExpiresAt == nil || !lots[0].ExpiresAt.Equal(want) {
		t.Fatalf("lots = %+v, want one admin lot ending %v", lots, want)
	}
	// Without a period the lot never expires.
	res2, err := e.a.Adjust(ctx, id, Adjustment{Amount: 5, Note: "kalici", RequestID: uuid.NewString()}, "admin@ornek.com")
	if err != nil || res2.Balance != 30 {
		t.Fatalf("second adjust = %+v, %v", res2, err)
	}
	lots, _ = e.store.Lots(ctx, id, e.clock())
	if len(lots) != 2 || lots[1].ExpiresAt != nil {
		t.Fatalf("lots = %+v, want the permanent lot last", lots)
	}
	// The audit trail is in the ledger.
	var by, note string
	if err := e.pool.QueryRow(ctx, `SELECT yapan, notu FROM kredi.hareket WHERE kullanici_id = $1 AND tutar = 25`, id).Scan(&by, &note); err != nil ||
		by != "admin@ornek.com" || note != "Destek talebi 4521" {
		t.Fatalf("ledger row by=%q note=%q err=%v", by, note, err)
	}
	// The user's own history shows the movement as admin_adjust, without the audit fields.
	d, err := e.a.User(ctx, id)
	if err != nil || len(d.Movements) != 2 || d.Movements[0].Kind != "admin_adjust" || d.Balance() != 30 {
		t.Fatalf("detail = %+v, %v", d, err)
	}
}

// Balance of the detail is the sum of its lots minus the debt.
func (d AdminUserDetail) Balance() int {
	n := -d.Debt
	for _, l := range d.Lots {
		n += l.Amount
	}
	return n
}

func TestAdmin_AdjustIsIdempotentPerRequestId(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	id, _ := e.user("Ayse", e.clock())
	other, _ := e.user("Mert", e.clock())
	req := uuid.NewString()
	in := Adjustment{Amount: 25, Note: "bir kez", RequestID: req}
	first, err := e.a.Adjust(ctx, id, in, "admin@ornek.com")
	if err != nil {
		t.Fatal(err)
	}
	// An upper-case spelling of the same id is the same request.
	in.RequestID = strings.ToUpper(req)
	again, err := e.a.Adjust(ctx, id, in, "admin@ornek.com")
	if err != nil || again.Movement.ID != first.Movement.ID || again.Balance != 25 {
		t.Fatalf("replay = %+v, %v; first %+v", again, err, first)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM kredi.hareket WHERE kullanici_id = $1 AND tur = 'yonetici'`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("movements = %d, %v; want 1", n, err)
	}
	// A deduction replays as well, even when the balance no longer covers it.
	dedReq := uuid.NewString()
	ded, err := e.a.Adjust(ctx, id, Adjustment{Amount: -20, Note: "geri al", RequestID: dedReq}, "admin@ornek.com")
	if err != nil || ded.Balance != 5 {
		t.Fatalf("deduct = %+v, %v", ded, err)
	}
	ded2, err := e.a.Adjust(ctx, id, Adjustment{Amount: -20, Note: "geri al", RequestID: dedReq}, "admin@ornek.com")
	if err != nil || ded2.Movement.ID != ded.Movement.ID || ded2.Movement.Amount != -20 {
		t.Fatalf("deduct replay = %+v, %v", ded2, err)
	}
	// The same request id for another user is refused.
	_, err = e.a.Adjust(ctx, other, Adjustment{Amount: 25, Note: "baska", RequestID: req}, "admin@ornek.com")
	var fe *FieldError
	if !errors.Is(err, ErrAdjustInvalid) || !errors.As(err, &fe) || fe.Field != "requestId" {
		t.Fatalf("other user err = %v", err)
	}
}

func TestAdmin_AdjustBelowZeroIs409(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	id, _ := e.user("Ayse", e.clock())
	if _, err := e.a.Adjust(ctx, id, Adjustment{Amount: 6, Note: "baslangic", RequestID: uuid.NewString()}, "a@b.c"); err != nil {
		t.Fatal(err)
	}
	_, err := e.a.Adjust(ctx, id, Adjustment{Amount: -10, Note: "fazla", RequestID: uuid.NewString()}, "a@b.c")
	var low *BalanceTooLowError
	if !errors.Is(err, ErrBalanceTooLow) || !errors.As(err, &low) || low.Balance != 6 {
		t.Fatalf("err = %v, want BalanceTooLowError{6}", err)
	}
	if bal, _ := e.store.Balance(ctx, id, e.clock()); bal != 6 {
		t.Fatalf("balance = %d after the refused deduction", bal)
	}
	res, err := e.a.Adjust(ctx, id, Adjustment{Amount: -6, Note: "tam", RequestID: uuid.NewString()}, "a@b.c")
	if err != nil || res.Balance != 0 || res.Movement.Amount != -6 {
		t.Fatalf("exact deduction = %+v, %v", res, err)
	}
}

func TestAdmin_AdjustValidDaysOnlyForAdd(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	id, _ := e.user("Ayse", e.clock())
	days, zero, big := 10, 0, 3651
	for name, tc := range map[string]struct {
		field string
		in    Adjustment
	}{
		"days with deduct": {"validDays", Adjustment{Amount: -5, Note: "not", ValidDays: &days, RequestID: uuid.NewString()}},
		"days zero":        {"validDays", Adjustment{Amount: 5, Note: "not", ValidDays: &zero, RequestID: uuid.NewString()}},
		"days too big":     {"validDays", Adjustment{Amount: 5, Note: "not", ValidDays: &big, RequestID: uuid.NewString()}},
		"amount zero":      {"amount", Adjustment{Amount: 0, Note: "not", RequestID: uuid.NewString()}},
		"amount too big":   {"amount", Adjustment{Amount: 100001, Note: "not", RequestID: uuid.NewString()}},
		"amount too small": {"amount", Adjustment{Amount: -100001, Note: "not", RequestID: uuid.NewString()}},
		"note short":       {"note", Adjustment{Amount: 5, Note: " ab ", RequestID: uuid.NewString()}},
		"note long":        {"note", Adjustment{Amount: 5, Note: strings.Repeat("a", 501), RequestID: uuid.NewString()}},
		"request id":       {"requestId", Adjustment{Amount: 5, Note: "not", RequestID: "abc"}},
	} {
		_, err := e.a.Adjust(ctx, id, tc.in, "a@b.c")
		var fe *FieldError
		if !errors.Is(err, ErrAdjustInvalid) || !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: err = %v, want ErrAdjustInvalid on %s", name, err, tc.field)
		}
	}
	if bal, _ := e.store.Balance(ctx, id, e.clock()); bal != 0 {
		t.Fatalf("a refused adjustment changed the balance to %d", bal)
	}
	max := 100000
	if _, err := e.a.Adjust(ctx, id, Adjustment{Amount: max, Note: "tavan", ValidDays: ptr(3650), RequestID: uuid.NewString()}, "a@b.c"); err != nil {
		t.Fatalf("the bounds themselves must pass: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAdmin_MovementsFilterKindCampaignDates(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	c := e.campaign(10, 50, 30, nil)
	e.setNow(e.clock().Add(time.Minute))
	idA, _ := e.user("Ayse", e.clock())
	idB, _ := e.user("Mert", e.clock())
	e.join(idA, "a-"+e.tag+"@ornek.com")
	if _, err := e.store.Debit(ctx, idA, "render", "job-"+e.tag, 12, Meta{Reason: "render_mix", ProjectID: uuid.NewString()}, e.clock()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Adjust(ctx, idB, Adjustment{Amount: 7, Note: "elle ekleme", RequestID: uuid.NewString()}, "admin@ornek.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Grant(ctx, idB, "paket", "pack-"+e.tag, Grant{Source: LotPack, Amount: 100}, e.clock()); err != nil {
		t.Fatal(err)
	}

	query := func(q MovementQuery) []AdminMovement {
		t.Helper()
		page, err := e.a.Movements(ctx, q)
		if err != nil {
			t.Fatalf("Movements(%+v): %v", q, err)
		}
		return page.Items
	}
	if got := query(MovementQuery{UserID: idA}); len(got) != 2 {
		t.Fatalf("user A has %d movements, want 2", len(got))
	}
	// Kind.
	adj := query(MovementQuery{UserID: idB, Kind: "admin_adjust"})
	if len(adj) != 1 || adj[0].Kind != "admin_adjust" || adj[0].Amount != 7 || *adj[0].By != "admin@ornek.com" || *adj[0].Note != "elle ekleme" {
		t.Fatalf("admin_adjust filter = %+v", adj)
	}
	if got := query(MovementQuery{UserID: idB, Kind: "pack"}); len(got) != 1 || got[0].By != nil || got[0].Note != nil {
		t.Fatalf("pack filter = %+v", got)
	}
	// Campaign: the gift and the spend from the gift lot, not the other users' rows.
	byCampaign := query(MovementQuery{CampaignID: &c.ID})
	if len(byCampaign) != 2 {
		t.Fatalf("campaign filter = %+v, want gift + spend", byCampaign)
	}
	for _, m := range byCampaign {
		if m.UserID != idA || m.CampaignID == nil || *m.CampaignID != c.ID {
			t.Errorf("campaign row = %+v", m)
		}
	}
	spend := query(MovementQuery{UserID: idA, Kind: "render"})
	if len(spend) != 1 || spend[0].Amount != -12 || spend[0].Operation == nil || *spend[0].Operation != "render_mix" || spend[0].ProjectID == nil {
		t.Fatalf("render row = %+v", spend)
	}
	other := int64(c.ID + 1)
	if got := query(MovementQuery{CampaignID: &other}); len(got) != 0 {
		t.Fatalf("other campaign filter = %+v", got)
	}
	for _, q := range []MovementQuery{{Kind: "bogus"}, {UserID: "nope"}} {
		if _, err := e.a.Movements(ctx, q); !errors.Is(err, ErrBadFilter) {
			t.Errorf("Movements(%+v) err = %v, want ErrBadFilter", q, err)
		}
	}
	if _, err := e.a.Movements(ctx, MovementQuery{Cursor: "zzz"}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("bad cursor err = %v", err)
	}

	// Dates are Europe/Istanbul days, inclusive. Put B's three movements at
	// 17 Oct 20:59 UTC (23:59 on the 17th there), 17 Oct 21:00 UTC (00:00 on
	// the 18th) and 18 Oct 20:59 UTC (23:59 on the 18th).
	times := []time.Time{
		time.Date(2026, 10, 17, 20, 59, 0, 0, time.UTC),
		time.Date(2026, 10, 17, 21, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 18, 20, 59, 0, 0, time.UTC),
	}
	if _, err := e.store.Grant(ctx, idB, "paket", "pack2-"+e.tag, Grant{Source: LotPack, Amount: 1}, e.clock()); err != nil {
		t.Fatal(err)
	}
	rows, err := e.pool.Query(ctx, `SELECT id FROM kredi.hareket WHERE kullanici_id = $1 ORDER BY id`, idB)
	if err != nil {
		t.Fatal(err)
	}
	var bIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		bIDs = append(bIDs, id)
	}
	rows.Close()
	if len(bIDs) != 3 {
		t.Fatalf("user B has %d movements", len(bIDs))
	}
	for i, id := range bIDs {
		if _, err := e.pool.Exec(ctx, `UPDATE kredi.hareket SET olusturuldu = $2 WHERE id = $1`, id, times[i]); err != nil {
			t.Fatal(err)
		}
	}
	d17, d18 := adminDay(2026, 10, 17), adminDay(2026, 10, 18)
	for name, tc := range map[string]struct {
		from, to *time.Time
		want     int
	}{
		"17th only":      {d17, d17, 1},
		"18th only":      {d18, d18, 2},
		"17th to 18th":   {d17, d18, 3},
		"from the 18th":  {d18, nil, 2},
		"up to the 17th": {nil, d17, 1},
		"after the 18th": {adminDay(2026, 10, 19), nil, 0},
	} {
		got := query(MovementQuery{UserID: idB, From: tc.from, To: tc.to})
		if len(got) != tc.want {
			t.Errorf("%s: %d movements, want %d", name, len(got), tc.want)
		}
	}

	// Paging with a cursor, newest first.
	for i := 0; i < 22; i++ {
		if _, err := e.a.Adjust(ctx, idA, Adjustment{Amount: 1, Note: "sayfa", RequestID: uuid.NewString()}, "a@b.c"); err != nil {
			t.Fatal(err)
		}
	}
	p1, err := e.a.Movements(ctx, MovementQuery{UserID: idA})
	if err != nil || len(p1.Items) != 20 || p1.NextCursor == nil {
		t.Fatalf("page 1 = %d items, next %v, %v", len(p1.Items), p1.NextCursor, err)
	}
	p2, err := e.a.Movements(ctx, MovementQuery{UserID: idA, Cursor: *p1.NextCursor})
	if err != nil || len(p2.Items) != 4 || p2.NextCursor != nil {
		t.Fatalf("page 2 = %d items, next %v, %v", len(p2.Items), p2.NextCursor, err)
	}
	if p2.Items[0].ID == p1.Items[19].ID {
		t.Fatal("the cursor repeated a row")
	}
}

func TestShortID(t *testing.T) {
	if got := ShortID("8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67"); got != "8F3A21" {
		t.Fatalf("ShortID = %q", got)
	}
}

// The month of the summary starts at 00:00 on the 1st in Europe/Istanbul.
func TestAdmin_DistributedThisMonthStartsInIstanbul(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	e.setNow(time.Date(2031, 9, 30, 19, 0, 0, 0, time.UTC))
	e.campaign(10, 7, 30, nil)
	e.setNow(time.Date(2031, 9, 30, 20, 59, 0, 0, time.UTC)) // 23:59 on 30 Sep in Istanbul
	idA, mailA := e.user("eylul", e.clock())
	e.join(idA, mailA)
	e.setNow(time.Date(2031, 9, 30, 21, 0, 0, 0, time.UTC)) // 00:00 on 1 Oct in Istanbul
	idB, mailB := e.user("ekim", e.clock())
	e.join(idB, mailB)
	e.setNow(time.Date(2031, 10, 18, 12, 0, 0, 0, time.UTC))
	o, err := e.camp.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.MonthPeople != 1 || o.MonthCredits != 7 {
		t.Fatalf("month = %d people, %d credits; want 1, 7", o.MonthPeople, o.MonthCredits)
	}
}

// raceLedger hides the pre-check: MovementOwner answers "free", as if the
// other user's movement committed after the check ran.
type raceLedger struct{ *PostgresStore }

func (raceLedger) MovementOwner(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

func TestAdmin_AdjustOwnerRaceInsideLockIs400(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	a, _ := e.user("a", e.clock())
	b, _ := e.user("b", e.clock())
	req := uuid.NewString()
	adm := NewAdmin(raceLedger{e.store}, e.store, identity.NewDirectory(e.pool), e.clock)
	if _, err := adm.Adjust(ctx, a, Adjustment{Amount: 5, Note: "ilk", RequestID: req}, "a@b.c"); err != nil {
		t.Fatal(err)
	}
	_, err := adm.Adjust(ctx, b, Adjustment{Amount: 5, Note: "ikinci", RequestID: req}, "a@b.c")
	var fe *FieldError
	if !errors.Is(err, ErrAdjustInvalid) || !errors.As(err, &fe) || fe.Field != "requestId" {
		t.Fatalf("grant race err = %v, want requestId validation error", err)
	}
	_, err = adm.Adjust(ctx, b, Adjustment{Amount: -1, Note: "ikinci", RequestID: req}, "a@b.c")
	fe = nil
	if !errors.Is(err, ErrAdjustInvalid) || !errors.As(err, &fe) || fe.Field != "requestId" {
		t.Fatalf("deduct race err = %v, want requestId validation error", err)
	}
}

func TestStore_CampaignOverviewIsNotCapped(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO kredi.kampanya (ad, kota, kisi_basi, gecerlilik_gun, durum, baslangic, olusturan)
		SELECT 'ovw-' || g, 10, 24, 30, 'bitti', now(), 'a@b.c' FROM generate_series(1, 105) g`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.pool.Exec(ctx, `DELETE FROM kredi.kampanya WHERE ad LIKE 'ovw-%'`) })
	var total int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM kredi.kampanya`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	o, err := e.store.CampaignOverview(ctx, e.clock())
	if err != nil || len(o.Campaigns) != total || total < 105 {
		t.Fatalf("campaigns = %d of %d, %v", len(o.Campaigns), total, err)
	}
}
