package credit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/identity"
)

type campaignEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store *PostgresStore
	c     *Campaigns
	now   time.Time
	mu    sync.Mutex
}

func newCampaignEnv(t *testing.T) *campaignEnv {
	t.Helper()
	e := &campaignEnv{t: t, pool: testPool(t), now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	e.store = NewPostgresStore(e.pool)
	e.c = NewCampaigns(e.store, func() time.Time {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.now
	})
	return e
}

func (e *campaignEnv) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

func (e *campaignEnv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *campaignEnv) start(quota, perUser, days int, endsAt *time.Time) Campaign {
	e.t.Helper()
	c, _, err := e.c.Create(context.Background(), NewCampaign{Name: "Hos geldin", Quota: quota, PerUser: perUser, ValidDays: days, EndsAt: endsAt}, "admin@ornek.com")
	if err != nil {
		e.t.Fatalf("Create: %v", err)
	}
	return c
}

func (e *campaignEnv) signup(email string, createdAt time.Time) identity.NewUser {
	u := identity.NewUser{ID: uuid.NewString(), Email: email, CreatedAt: createdAt}
	e.c.GrantSignupGift(context.Background(), u)
	return u
}

func (e *campaignEnv) campaign(id int64) Campaign {
	e.t.Helper()
	d, err := e.c.Get(context.Background(), id)
	if err != nil {
		e.t.Fatalf("Get: %v", err)
	}
	return d.Campaign
}

func (e *campaignEnv) giftLots(campaignID int64) int {
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM kredi.dilim WHERE kaynak = 'kampanya' AND ref LIKE $1`, fmt.Sprintf("%d:%%", campaignID)).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestCampaigns_CreateReplacesActive(t *testing.T) {
	e := newCampaignEnv(t)
	first := e.start(10, 50, 60, nil)
	e.advance(time.Hour)
	second, replaced, err := e.c.Create(context.Background(), NewCampaign{Name: "Yeni", Quota: 5, PerUser: 20, ValidDays: 30}, "admin2@ornek.com")
	if err != nil {
		t.Fatal(err)
	}
	if replaced == nil || replaced.ID != first.ID || replaced.Status != CampaignStopped || replaced.StoppedAt == nil {
		t.Fatalf("replaced = %+v, want the first campaign stopped", replaced)
	}
	if second.Status != CampaignActive || second.CreatedBy != "admin2@ornek.com" || second.StartedAt.IsZero() {
		t.Fatalf("created = %+v", second)
	}
	if got := e.campaign(first.ID).Status; got != CampaignStopped {
		t.Fatalf("first status = %s, want durduruldu", got)
	}
	o, err := e.c.List(context.Background())
	if err != nil || o.Active == nil || o.Active.ID != second.ID || len(o.Campaigns) != 2 || o.QuotaLeft != 5 {
		t.Fatalf("overview = %+v, %v", o, err)
	}
}

func TestCampaigns_CreateValidates(t *testing.T) {
	e := newCampaignEnv(t)
	past := e.clock().AddDate(0, 0, -1)
	for name, in := range map[string]NewCampaign{
		"no name":   {Quota: 1, PerUser: 1, ValidDays: 1},
		"no quota":  {Name: "x", PerUser: 1, ValidDays: 1},
		"no credit": {Name: "x", Quota: 1, ValidDays: 1},
		"no days":   {Name: "x", Quota: 1, PerUser: 1},
		"past end":  {Name: "x", Quota: 1, PerUser: 1, ValidDays: 1, EndsAt: &past},
	} {
		if _, _, err := e.c.Create(context.Background(), in, "a"); !errors.Is(err, ErrCampaignInvalid) {
			t.Errorf("%s: err = %v, want ErrCampaignInvalid", name, err)
		}
	}
}

func TestCampaigns_StopIsFinal(t *testing.T) {
	e := newCampaignEnv(t)
	c := e.start(10, 50, 60, nil)
	stopped, err := e.c.Stop(context.Background(), c.ID)
	if err != nil || stopped.Status != CampaignStopped || stopped.StoppedAt == nil {
		t.Fatalf("Stop = %+v, %v", stopped, err)
	}
	if _, err := e.c.Stop(context.Background(), c.ID); !errors.Is(err, ErrCampaignNotActive) {
		t.Fatalf("second stop err = %v, want ErrCampaignNotActive", err)
	}
	if _, err := e.c.Stop(context.Background(), c.ID+999); !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("unknown stop err = %v, want ErrCampaignNotFound", err)
	}
	e.advance(time.Minute)
	e.signup("a@ornek.com", e.clock())
	if n := e.giftLots(c.ID); n != 0 {
		t.Fatalf("a stopped campaign gave %d gifts", n)
	}
}

func TestCampaigns_SignupAfterStartGetsGift(t *testing.T) {
	e := newCampaignEnv(t)
	c := e.start(10, 50, 60, nil)
	e.advance(time.Minute)
	u := e.signup("a@ornek.com", e.clock())

	lots, err := e.store.Lots(context.Background(), u.ID, e.clock())
	if err != nil || len(lots) != 1 {
		t.Fatalf("lots = %+v, %v", lots, err)
	}
	if lots[0].Source != LotCampaign || lots[0].Amount != 50 || lots[0].ExpiresAt == nil ||
		!lots[0].ExpiresAt.Equal(e.clock().AddDate(0, 0, 60)) {
		t.Fatalf("lot = %+v, want campaign 50 ending +60d", lots[0])
	}
	var tur string
	var kampanya *int64
	var tutar int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT tur, kampanya_id, tutar FROM kredi.hareket WHERE kullanici_id = $1::uuid`, u.ID).Scan(&tur, &kampanya, &tutar); err != nil {
		t.Fatal(err)
	}
	if tur != "kampanya" || kampanya == nil || *kampanya != c.ID || tutar != 50 {
		t.Fatalf("movement = %s %v %d", tur, kampanya, tutar)
	}
	if got := e.campaign(c.ID); got.Joined != 1 || got.Status != CampaignActive {
		t.Fatalf("campaign = %+v", got)
	}
	// The hook is idempotent: a second call for the same user changes nothing.
	e.c.GrantSignupGift(context.Background(), u)
	if got := e.campaign(c.ID).Joined; got != 1 {
		t.Fatalf("joined = %d after a repeat, want 1", got)
	}
	gift, err := e.store.Gift(context.Background(), u.ID, e.clock())
	if err != nil || gift == nil || gift.Amount != 50 {
		t.Fatalf("Gift = %+v, %v", gift, err)
	}
}

func TestCampaigns_SignupBeforeStartGetsNothing(t *testing.T) {
	e := newCampaignEnv(t)
	before := e.clock().Add(-time.Hour)
	c := e.start(10, 50, 60, nil)
	u := e.signup("old@ornek.com", before)
	if n := e.giftLots(c.ID); n != 0 {
		t.Fatalf("a user created before the start got %d gifts", n)
	}
	if lots, _ := e.store.Lots(context.Background(), u.ID, e.clock()); len(lots) != 0 {
		t.Fatalf("lots = %+v", lots)
	}
}

func TestCampaigns_NoActiveCampaignNoGift(t *testing.T) {
	e := newCampaignEnv(t)
	u := e.signup("a@ornek.com", e.clock())
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM kredi.dilim WHERE kullanici_id = $1::uuid`, u.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("lots = %d, %v; want 0", n, err)
	}
}

func TestCampaigns_QuotaNeverExceededUnderConcurrency(t *testing.T) {
	e := newCampaignEnv(t)
	c := e.start(3, 50, 60, nil)
	e.advance(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.signup(fmt.Sprintf("kisi%d@ornek.com", i), e.clock())
		}()
	}
	wg.Wait()
	if n := e.giftLots(c.ID); n != 3 {
		t.Fatalf("gift lots = %d, want exactly the quota 3", n)
	}
	got := e.campaign(c.ID)
	if got.Joined != 3 || got.Status != CampaignQuotaFull {
		t.Fatalf("campaign = %+v, want 3 joined and kota_doldu", got)
	}
	o, _ := e.c.List(context.Background())
	if o.Active != nil || o.QuotaLeft != 0 || o.MonthPeople != 3 || o.MonthCredits != 150 {
		t.Fatalf("overview = %+v", o)
	}
}

func TestCampaigns_SameEmailHashNeverGetsTwice(t *testing.T) {
	e := newCampaignEnv(t)
	c := e.start(10, 50, 60, nil)
	e.advance(time.Minute)
	first := e.signup("Foo@x.com ", e.clock())
	if e.giftLots(c.ID) != 1 {
		t.Fatal("the first signup got no gift")
	}
	// The account is deleted, the person registers again.
	if err := e.store.DeleteUserData(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM kredi.kampanya_katilim WHERE kullanici_id IS NULL AND dilim_id IS NULL`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("anonymised participation rows = %d, %v; want 1", left, err)
	}
	e.advance(time.Minute)
	again := e.signup("foo@x.com", e.clock())
	if n := e.giftLots(c.ID); n != 0 {
		t.Fatalf("gift lots = %d after the delete, want 0 (the lot went with the account)", n)
	}
	if lots, _ := e.store.Lots(context.Background(), again.ID, e.clock()); len(lots) != 0 {
		t.Fatalf("re-registered person got %+v", lots)
	}
	if got := e.campaign(c.ID).Joined; got != 1 {
		t.Fatalf("joined = %d, want 1", got)
	}
}

func TestCampaigns_SweepEndsByDateAndRetriesMissingGift(t *testing.T) {
	e := newCampaignEnv(t)
	ctx := context.Background()
	if err := identity.Migrate(ctx, e.pool); err != nil {
		t.Fatal(err)
	}
	// A clock in the future: users other tests left in the shared table are
	// older than the campaign and never qualify.
	e.advance(time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC).Sub(e.clock()))
	ends := e.clock().AddDate(0, 0, 2)
	c := e.start(10, 50, 60, &ends)

	// A user whose hook never ran: the row exists, the participation does not.
	e.advance(time.Hour)
	missing := uuid.NewString()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at) VALUES ($1, $2, false, 'Eksik', 'x', $3)`,
		missing, "Eksik@ornek.com", e.clock()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, missing) })
	// A user from before the campaign must not be picked up.
	old := uuid.NewString()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at) VALUES ($1, $2, false, 'Eski', 'x', $3)`,
		old, "eski@ornek.com", e.clock().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, old) })

	e.advance(time.Hour)
	ended, gifts, err := e.c.Sweep(ctx, e.clock())
	if err != nil || ended != 0 || gifts != 1 {
		t.Fatalf("Sweep = %d ended, %d gifts, %v; want 0, 1", ended, gifts, err)
	}
	if lots, _ := e.store.Lots(ctx, missing, e.clock()); len(lots) != 1 || lots[0].Amount != 50 {
		t.Fatalf("missing user lots = %+v", lots)
	}
	if lots, _ := e.store.Lots(ctx, old, e.clock()); len(lots) != 0 {
		t.Fatalf("old user got %+v", lots)
	}
	// Idempotent.
	if _, gifts, _ := e.c.Sweep(ctx, e.clock()); gifts != 0 {
		t.Fatalf("second sweep gave %d gifts", gifts)
	}

	// Still active on the end date; ended the day after.
	e.advance(48 * time.Hour) // 3 March, the end date itself
	if ended, _, _ := e.c.Sweep(ctx, e.clock()); ended != 0 {
		t.Fatalf("ended = %d on the last day, want 0", ended)
	}
	e.advance(24 * time.Hour)
	ended, _, err = e.c.Sweep(ctx, e.clock())
	if err != nil || ended != 1 {
		t.Fatalf("ended = %d, %v after the end date, want 1", ended, err)
	}
	if got := e.campaign(c.ID).Status; got != CampaignEnded {
		t.Fatalf("status = %s, want bitti", got)
	}
	e.signup("gec@ornek.com", e.clock())
	if n := e.giftLots(c.ID); n != 1 {
		t.Fatalf("gift lots = %d, an ended campaign must give nothing more", n)
	}
}

func TestCampaigns_DetailCountsUsedCredits(t *testing.T) {
	e := newCampaignEnv(t)
	ctx := context.Background()
	c := e.start(10, 50, 60, nil)
	e.advance(time.Minute)
	u := e.signup("a@ornek.com", e.clock())
	e.signup("b@ornek.com", e.clock())
	if _, err := e.store.Debit(ctx, u.ID, "render", "job-1", 20, Meta{}, e.clock()); err != nil {
		t.Fatal(err)
	}
	d, err := e.c.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Distributed != 100 || d.Used != 20 || d.UsedPercent != 20 || len(d.Recent) != 2 {
		t.Fatalf("detail = %+v", d)
	}
	if _, err := e.c.Get(ctx, c.ID+999); !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("unknown get err = %v", err)
	}
}

func TestCampaignStatus_JSONNames(t *testing.T) {
	for s, want := range map[CampaignStatus]string{
		CampaignActive: "active", CampaignQuotaFull: "quota_full", CampaignEnded: "ended", CampaignStopped: "stopped",
	} {
		if got := s.JSON(); got != want {
			t.Errorf("%s.JSON() = %q, want %q", s, got, want)
		}
	}
}

func TestStore_DeleteUserDataRemovesLotsAndAnonymizesCampaignJoin(t *testing.T) {
	e := newCampaignEnv(t)
	ctx := context.Background()
	c := e.start(10, 50, 60, nil)
	e.advance(time.Minute)
	u := e.signup("Silinecek@ornek.com", e.clock())
	if _, err := e.store.SpendAction(ctx, u.ID, OpLyricsGenerate, "act-del", 2, 10*time.Minute, 12, e.clock()); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DeleteUserData(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"hesap", "hareket", "dilim"} {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM kredi.`+table+` WHERE kullanici_id = $1::uuid`, u.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s rows = %d, %v; want 0", table, n, err)
		}
	}
	var orphans int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM kredi.dilim_hareket dh
		WHERE NOT EXISTS (SELECT 1 FROM kredi.dilim d WHERE d.id = dh.dilim_id)`).Scan(&orphans); err != nil || orphans != 0 {
		t.Errorf("orphan lot movements = %d, %v", orphans, err)
	}
	var kept int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM kredi.kampanya_katilim
		WHERE kampanya_id = $1 AND kullanici_id IS NULL AND dilim_id IS NULL AND eposta_karma <> ''`, c.ID).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("anonymised joins = %d, %v; want 1 with the e-mail hash kept", kept, err)
	}
	if got := e.campaign(c.ID).Joined; got != 1 {
		t.Errorf("joined = %d, want 1 (counters unchanged)", got)
	}
}
