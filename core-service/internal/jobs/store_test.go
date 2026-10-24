package jobs

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/identity"
	"github.com/makeasinger/api/internal/model"
)

// testPool connects to the local Postgres (same default DSN as the credit
// tests), applies the migrations and empties the uretim tables. The test is
// skipped when Postgres is absent. TRUNCATE touches only uretim.* tables.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := identity.Connect(ctx, dsn)
	if err != nil {
		t.Skip("no local Postgres, skipping")
	}
	t.Cleanup(pool.Close)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE uretim.isler, uretim.is_olaylari, uretim.cihazlar`); err != nil {
		t.Fatalf("could not truncate the uretim tables: %v", err)
	}
	return pool
}

func TestMigrations_EmbeddedAndSorted(t *testing.T) {
	names, err := migrationNames()
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations: %v %v", names, err)
	}
	if names[0] != "001_isler.sql" {
		t.Errorf("first migration: %q", names[0])
	}
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) Publish(_ context.Context, ev []Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev...)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func newJob(owner, typ, project string) *model.Job {
	return &model.Job{ID: uuid.NewString(), Type: typ, OwnerID: owner, ProjectID: project,
		Status: model.JobStatusQueued, Payload: []byte(`{"a":1}`)}
}

func ctx() context.Context { return context.Background() }

func scenarioConsecutiveSeq(t *testing.T, r Repo, sonSeq func(id string) int64) {
	job := newJob("u1", model.JobTypeRender, "p1")
	if err := r.Create(ctx(), job); err != nil {
		t.Fatal(err)
	}
	if err := r.Update(ctx(), job.ID, func(j *model.Job) error {
		j.Status, j.Progress, j.CurrentStep = model.JobStatusRunning, 10, "a"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Update(ctx(), job.ID, func(j *model.Job) error { j.Progress = 50; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateWith(ctx(), job.ID, func(j *model.Job, e Emitter) error {
		e.Emit(KindLine, map[string]any{"section": 0, "text": "a"})
		e.Emit(KindLine, map[string]any{"section": 0, "text": "b"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	evs, err := r.EventsAfter(ctx(), "u1", job.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 {
		t.Fatalf("want 5 events, got %d", len(evs))
	}
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Errorf("event %d seq %d", i, e.Seq)
		}
	}
	if evs[0].Kind != KindQueued || evs[1].Kind != KindProgress || evs[4].Kind != KindLine {
		t.Errorf("kinds: %v %v %v", evs[0].Kind, evs[1].Kind, evs[4].Kind)
	}
	tail, _ := r.EventsAfter(ctx(), "u1", job.ID, 3, 100)
	if len(tail) != 2 || tail[0].Seq != 4 {
		t.Errorf("after 3: %+v", tail)
	}
	got, _ := r.Get(ctx(), job.ID)
	if got.LastSeq != 5 || sonSeq(job.ID) != 5 {
		t.Errorf("son_seq: %d / %d", got.LastSeq, sonSeq(job.ID))
	}
	if string(got.Payload) == "" || got.Progress != 50 || got.CurrentStep != "a" {
		t.Errorf("job not persisted: %+v", got)
	}
}

func scenarioSkipWrite(t *testing.T, r Repo) {
	job := newJob("u1", model.JobTypeRender, "p1")
	_ = r.Create(ctx(), job)
	err := r.Update(ctx(), job.ID, func(j *model.Job) error { j.Progress = 99; return ErrSkipWrite })
	if !errors.Is(err, ErrSkipWrite) {
		t.Fatalf("want ErrSkipWrite, got %v", err)
	}
	got, _ := r.Get(ctx(), job.ID)
	if got.Progress != 0 || got.LastSeq != 1 {
		t.Errorf("row changed: %+v", got)
	}
	evs, _ := r.EventsAfter(ctx(), "u1", job.ID, 0, 10)
	if len(evs) != 1 {
		t.Errorf("events: %d", len(evs))
	}
}

func scenarioOwnerOnly(t *testing.T, r Repo) {
	job := newJob("u1", model.JobTypeRender, "p1")
	_ = r.Create(ctx(), job)
	if _, err := r.EventsAfter(ctx(), "u2", job.ID, 0, 10); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("EventsAfter other user: %v", err)
	}
	if _, err := r.GetOwned(ctx(), "u2", job.ID); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("GetOwned other user: %v", err)
	}
	if _, err := r.Get(ctx(), uuid.NewString()); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("Get missing: %v", err)
	}
	if _, err := r.Get(ctx(), "not-a-uuid"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("Get bad id: %v", err)
	}
	if err := r.Update(ctx(), uuid.NewString(), func(*model.Job) error { return nil }); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("Update missing: %v", err)
	}
	list, _ := r.List(ctx(), "u1", time.Now().Add(-time.Hour), 10)
	if len(list) != 1 {
		t.Errorf("list: %d", len(list))
	}
	if list, _ := r.List(ctx(), "u2", time.Now().Add(-time.Hour), 10); len(list) != 0 {
		t.Errorf("other user list: %d", len(list))
	}
}

func scenarioActiveLyrics(t *testing.T, r Repo) {
	first := newJob("u1", model.JobTypeLyrics, "p1")
	if err := r.Create(ctx(), first); err != nil {
		t.Fatal(err)
	}
	second := newJob("u1", model.JobTypeLyrics, "p1")
	err := r.Create(ctx(), second)
	if !errors.Is(err, ErrActiveLyricsJob) || ActiveJobID(err) != first.ID {
		t.Fatalf("want ErrActiveLyricsJob with id %s, got %v (%q)", first.ID, err, ActiveJobID(err))
	}
	if err := r.Create(ctx(), newJob("u1", model.JobTypeLyrics, "p2")); err != nil {
		t.Errorf("other project: %v", err)
	}
	if err := r.Create(ctx(), newJob("u2", model.JobTypeLyrics, "p1")); err != nil {
		t.Errorf("other owner: %v", err)
	}
	if err := r.Create(ctx(), newJob("u1", model.JobTypeRender, "p1")); err != nil {
		t.Errorf("render same project: %v", err)
	}
	_ = r.Update(ctx(), first.ID, func(j *model.Job) error { j.Status = model.JobStatusSucceeded; return nil })
	if err := r.Create(ctx(), second); err != nil {
		t.Errorf("after success: %v", err)
	}
}

func scenarioSweep(t *testing.T, r Repo, backdate func(id string, created, completed time.Time)) {
	old := newJob("u1", model.JobTypeRender, "p1")
	finished := newJob("u1", model.JobTypeRender, "p2")
	fresh := newJob("u1", model.JobTypeRender, "p3")
	for _, j := range []*model.Job{old, finished, fresh} {
		if err := r.Create(ctx(), j); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.Update(ctx(), finished.ID, func(j *model.Job) error { j.Status = model.JobStatusSucceeded; return nil })
	now := time.Now().UTC()
	backdate(old.ID, now.Add(-31*24*time.Hour), time.Time{})
	backdate(finished.ID, now.Add(-2*24*time.Hour), now.Add(-25*time.Hour))
	jobsN, eventsN, err := r.Sweep(ctx(), now)
	if err != nil {
		t.Fatal(err)
	}
	if jobsN != 1 || eventsN != 2 {
		t.Errorf("swept jobs=%d events=%d, want 1 and 2", jobsN, eventsN)
	}
	if _, err := r.Get(ctx(), old.ID); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("expired job kept: %v", err)
	}
	if _, err := r.Get(ctx(), finished.ID); err != nil {
		t.Errorf("finished job must stay: %v", err)
	}
	if evs, _ := r.EventsAfter(ctx(), "u1", finished.ID, 0, 10); len(evs) != 0 {
		t.Errorf("old events kept: %d", len(evs))
	}
	if evs, _ := r.EventsAfter(ctx(), "u1", fresh.ID, 0, 10); len(evs) != 1 {
		t.Errorf("fresh events lost: %d", len(evs))
	}
}

func scenarioOnFinished(t *testing.T, r Repo, hook func(func(context.Context, model.Job))) {
	var finished []model.Job
	hook(func(_ context.Context, j model.Job) { finished = append(finished, j) })
	run := func(final model.JobStatus) {
		job := newJob("u1", model.JobTypeRender, "p-"+string(final))
		_ = r.Create(ctx(), job)
		_ = r.Update(ctx(), job.ID, func(j *model.Job) error { j.Status, j.Progress = model.JobStatusRunning, 5; return nil })
		_ = r.Update(ctx(), job.ID, func(j *model.Job) error { j.Status = final; return nil })
	}
	run(model.JobStatusCanceled)
	if len(finished) != 0 {
		t.Fatalf("canceled must not fire: %d", len(finished))
	}
	run(model.JobStatusSucceeded)
	run(model.JobStatusFailed)
	if len(finished) != 2 || finished[0].Status != model.JobStatusSucceeded || finished[1].Status != model.JobStatusFailed {
		t.Errorf("hooks: %+v", finished)
	}
	if finished[0].CompletedAt == nil {
		t.Error("bitis not set on finish")
	}
}

func TestStore_UpdateAppendsEventsWithConsecutiveSeq(t *testing.T) {
	pool := testPool(t)
	rec := &recorder{}
	s := NewPostgresStore(pool, rec)
	scenarioConsecutiveSeq(t, s, func(id string) int64 {
		var n int64
		_ = pool.QueryRow(ctx(), `SELECT son_seq FROM uretim.isler WHERE id=$1`, id).Scan(&n)
		return n
	})
	if rec.count() != 5 {
		t.Errorf("published %d events, want 5", rec.count())
	}
}

func TestStore_SkipWriteLeavesRow(t *testing.T) {
	scenarioSkipWrite(t, NewPostgresStore(testPool(t), nil))
}

func TestStore_EventsAfterOwnerOnly(t *testing.T) {
	scenarioOwnerOnly(t, NewPostgresStore(testPool(t), nil))
}

func TestStore_SecondActiveLyricsJobRejected(t *testing.T) {
	scenarioActiveLyrics(t, NewPostgresStore(testPool(t), nil))
}

func pgBackdate(pool *pgxpool.Pool) func(string, time.Time, time.Time) {
	return func(id string, created, completed time.Time) {
		var done *time.Time
		if !completed.IsZero() {
			done = &completed
		}
		if _, err := pool.Exec(ctx(), `UPDATE uretim.isler SET olusturma=$2::timestamptz,
			son_kullanma=$2::timestamptz + interval '30 days', bitis=$3::timestamptz WHERE id=$1`,
			id, created, done); err != nil {
			panic(err)
		}
	}
}

func TestStore_SweepDeletesExpiredAndOldEvents(t *testing.T) {
	pool := testPool(t)
	scenarioSweep(t, NewPostgresStore(pool, nil), pgBackdate(pool))
}

func TestStore_OnFinishedOnlyOnTerminal(t *testing.T) {
	s := NewPostgresStore(testPool(t), nil)
	scenarioOnFinished(t, s, s.OnFinished)
}

func TestStore_DeleteOwnerRemovesJobsEventsDevices(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool, nil)
	a, b := newJob("u1", model.JobTypeRender, "p"), newJob("u2", model.JobTypeRender, "p")
	_ = s.Create(ctx(), a)
	_ = s.Create(ctx(), b)
	_, err := pool.Exec(ctx(), `INSERT INTO uretim.cihazlar (token, sahip, platform, dil, guncelleme)
		VALUES ('t1','u1','ios','tr',now()), ('t2','u2','ios','tr',now())`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOwner(ctx(), "u1"); err != nil {
		t.Fatal(err)
	}
	var jobsN, evN, devN int
	_ = pool.QueryRow(ctx(), `SELECT (SELECT count(*) FROM uretim.isler), (SELECT count(*) FROM uretim.is_olaylari),
		(SELECT count(*) FROM uretim.cihazlar)`).Scan(&jobsN, &evN, &devN)
	if jobsN != 1 || evN != 1 || devN != 1 {
		t.Errorf("remaining jobs=%d events=%d devices=%d, want 1 each", jobsN, evN, devN)
	}
}

func TestStore_ConcurrentUpdatesKeepSeqConsecutive(t *testing.T) {
	pool := testPool(t)
	s := NewPostgresStore(pool, nil)
	ctx := context.Background()
	job := &model.Job{ID: uuid.NewString(), Type: model.JobTypeRender, OwnerID: "u1", Status: model.JobStatusRunning}
	if err := s.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	const n = 24
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = s.Update(ctx, job.ID, func(j *model.Job) error { j.Progress = i; j.CurrentStep = "s"; return nil })
			} else {
				err = s.UpdateWith(ctx, job.ID, func(j *model.Job, em Emitter) error {
					em.Emit(KindLine, map[string]any{"section": 0, "text": "x"})
					return nil
				})
			}
			if err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	evs, err := s.EventsAfter(ctx, "u1", job.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d, want %d (gap or duplicate)", i, e.Seq, i+1)
		}
	}
	got, _ := s.Get(ctx, job.ID)
	if got.LastSeq != int64(len(evs)) || len(evs) < n/2+1 {
		t.Errorf("son_seq = %d with %d events", got.LastSeq, len(evs))
	}
}

// scenarioList: active jobs are always listed; a finished one only when it
// finished at or after since; the limit applies.
func scenarioList(t *testing.T, r Repo, backdate func(id string, created, completed time.Time)) {
	active := newJob("u1", model.JobTypeRender, "p1")
	oldDone := newJob("u1", model.JobTypeRender, "p2")
	newDone := newJob("u1", model.JobTypeRender, "p3")
	for _, j := range []*model.Job{active, oldDone, newDone} {
		if err := r.Create(ctx(), j); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{oldDone.ID, newDone.ID} {
		_ = r.Update(ctx(), id, func(j *model.Job) error { j.Status = model.JobStatusSucceeded; return nil })
	}
	now := time.Now().UTC()
	backdate(active.ID, now.Add(-72*time.Hour), time.Time{}) // created long ago, still running
	backdate(oldDone.ID, now.Add(-72*time.Hour), now.Add(-48*time.Hour))
	backdate(newDone.ID, now.Add(-time.Hour), now.Add(-time.Minute))
	list, err := r.List(ctx(), "u1", now.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, j := range list {
		got[j.ID] = true
	}
	if len(list) != 2 || !got[active.ID] || !got[newDone.ID] {
		t.Errorf("list = %d jobs %v, want the active and the recently finished job", len(list), got)
	}
	if one, _ := r.List(ctx(), "u1", now.Add(-24*time.Hour), 1); len(one) != 1 {
		t.Errorf("limit 1 gave %d", len(one))
	}
}

// scenarioStale: only queued or running lyrics jobs without activity since
// `before` are stale.
func scenarioStale(t *testing.T, r Repo) {
	lyr := newJob("u1", model.JobTypeLyrics, "p1")
	render := newJob("u1", model.JobTypeRender, "p2")
	done := newJob("u1", model.JobTypeLyrics, "p3")
	for _, j := range []*model.Job{lyr, render, done} {
		if err := r.Create(ctx(), j); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.Update(ctx(), done.ID, func(j *model.Job) error { j.Status = model.JobStatusFailed; return nil })
	ids, err := r.StaleLyricsJobs(ctx(), time.Now().Add(-time.Minute))
	if err != nil || len(ids) != 0 {
		t.Fatalf("fresh jobs: %v %v, want none", ids, err)
	}
	ids, err = r.StaleLyricsJobs(ctx(), time.Now().Add(time.Minute))
	if err != nil || len(ids) != 1 || ids[0] != lyr.ID {
		t.Fatalf("stale = %v %v, want only the running lyrics job", ids, err)
	}
}

func TestPostgresStore_DeviceRegistry(t *testing.T) {
	s := NewPostgresStore(testPool(t), nil)
	ctx := context.Background()
	if err := s.UpsertDevice(ctx, "alice", model.Device{Token: "t1", Platform: "ios", Language: "tr", AppVersion: "1.0"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDevice(ctx, "alice", model.Device{Token: "t1", Platform: "ios", Language: "en", AppVersion: "1.1"}); err != nil {
		t.Fatal(err)
	}
	ds, err := s.Devices(ctx, "alice")
	if err != nil || len(ds) != 1 || ds[0].Language != "en" || ds[0].AppVersion != "1.1" {
		t.Fatalf("devices = %+v, %v", ds, err)
	}
	// The token moves to bob.
	if err := s.UpsertDevice(ctx, "bob", model.Device{Token: "t1", Platform: "android", Language: "fr"}); err != nil {
		t.Fatal(err)
	}
	if ds, _ := s.Devices(ctx, "alice"); len(ds) != 0 {
		t.Errorf("alice still has %+v", ds)
	}
	if ds, _ := s.Devices(ctx, "bob"); len(ds) != 1 || ds[0].Platform != "android" {
		t.Errorf("bob = %+v", ds)
	}
	// Alice cannot delete bob's token; bob can.
	if err := s.DeleteDevice(ctx, "alice", "t1"); err != nil {
		t.Fatal(err)
	}
	if ds, _ := s.Devices(ctx, "bob"); len(ds) != 1 {
		t.Fatalf("other user's delete removed the row")
	}
	if err := s.DeleteDevice(ctx, "bob", "t1"); err != nil {
		t.Fatal(err)
	}
	_ = s.UpsertDevice(ctx, "bob", model.Device{Token: "t2", Platform: "ios", Language: "tr"})
	if err := s.DeleteToken(ctx, "t2"); err != nil {
		t.Fatal(err)
	}
	if ds, _ := s.Devices(ctx, "bob"); len(ds) != 0 {
		t.Errorf("bob = %+v", ds)
	}
}
