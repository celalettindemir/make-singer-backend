package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/model"
)

func TestImportRedisJobs_CopiesAndRefreshesNonTerminal(t *testing.T) {
	pool := testPool(t)
	rdb := busRedis(t)
	ctx := context.Background()
	_ = rdb.FlushDB(ctx).Err()
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err() })
	repo := NewPostgresStore(pool, nil)

	put := func(j model.Job) {
		raw, _ := json.Marshal(j)
		if err := rdb.Set(ctx, "job:"+j.ID, raw, time.Hour).Err(); err != nil {
			t.Fatalf("redis set: %v", err)
		}
	}
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	a := model.Job{ID: uuid.NewString(), Type: model.JobTypeRender, OwnerID: "u1", Status: model.JobStatusRunning,
		Progress: 40, CurrentStep: "x", CreatedAt: created}
	done := time.Now().UTC().Truncate(time.Millisecond)
	b := model.Job{ID: uuid.NewString(), Type: model.JobTypeMaster, OwnerID: "u1", Status: model.JobStatusSucceeded,
		Progress: 100, Result: []byte(`{"fileKey":"k"}`), CreatedAt: created, CompletedAt: &done}
	put(a)
	put(b)

	copied, refreshed, err := ImportRedisJobs(ctx, rdb, repo)
	if err != nil || copied != 2 || refreshed != 0 {
		t.Fatalf("first import: %d %d %v", copied, refreshed, err)
	}
	got, err := repo.Get(ctx, a.ID)
	if err != nil || got.Status != model.JobStatusRunning || got.Progress != 40 {
		t.Fatalf("copied A: %+v %v", got, err)
	}
	var expires time.Time
	if err := pool.QueryRow(ctx, `SELECT son_kullanma FROM uretim.isler WHERE id=$1`, a.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if !expires.Equal(created.Add(ResultRetention)) {
		t.Errorf("son_kullanma = %v, want created + 30d", expires)
	}
	evs, _ := repo.EventsAfter(ctx, "u1", a.ID, 0, 10)
	if len(evs) != 1 || evs[0].Kind != KindQueued {
		t.Errorf("events of a copied job: %+v", evs)
	}
	gb, _ := repo.Get(ctx, b.ID)
	if gb.Status != model.JobStatusSucceeded || string(gb.Result) != `{"fileKey": "k"}` && string(gb.Result) != `{"fileKey":"k"}` {
		t.Errorf("copied B: %+v", gb)
	}

	// Redis A moves on: PG A is refreshed; an unchanged B is left alone.
	a.Progress = 90
	put(a)
	copied, refreshed, err = ImportRedisJobs(ctx, rdb, repo)
	if err != nil || copied != 0 || refreshed != 1 {
		t.Fatalf("second import: %d %d %v", copied, refreshed, err)
	}
	got, _ = repo.Get(ctx, a.ID)
	if got.Progress != 90 {
		t.Errorf("A progress = %d, want 90", got.Progress)
	}
	evs, _ = repo.EventsAfter(ctx, "u1", a.ID, 1, 10)
	if len(evs) != 1 || evs[0].Kind != KindProgress {
		t.Errorf("derived event of the refresh: %+v", evs)
	}

	// A terminal PG row is never overwritten, even if Redis disagrees.
	b.Status, b.Progress, b.Result = model.JobStatusRunning, 10, nil
	put(b)
	copied, refreshed, err = ImportRedisJobs(ctx, rdb, repo)
	if err != nil || copied != 0 || refreshed != 0 {
		t.Fatalf("third import: %d %d %v", copied, refreshed, err)
	}
	gb, _ = repo.Get(ctx, b.ID)
	if gb.Status != model.JobStatusSucceeded {
		t.Errorf("terminal B was overwritten: %+v", gb)
	}
}
