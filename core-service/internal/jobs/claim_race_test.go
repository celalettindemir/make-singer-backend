package jobs_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/identity"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

// A claim and a cancel race on one queued job in live Postgres: the row lock
// lets exactly one win. When the cancel wins the claim returns false and the
// job is refunded once; when the claim wins the charge is kept.
func TestRenderService_PostgresClaimCancelRace(t *testing.T) {
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := identity.Connect(cctx, dsn)
	if err != nil {
		t.Skip("no local Postgres, skipping")
	}
	t.Cleanup(pool.Close)
	if err := jobs.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `TRUNCATE uretim.isler, uretim.is_olaylari, uretim.cihazlar`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := jobs.NewPostgresStore(pool, nil)
	claimedWins, cancelWins := 0, 0
	for i := 0; i < 40; i++ {
		fake := &credit.FakeSpender{}
		s := service.NewRenderService(store, nil, nil, &config.R2Config{}).WithCredits(fake)
		id := uuid.NewString()
		if err := store.Create(ctx, &model.Job{ID: id, Type: model.JobTypeRender, OwnerID: "u1", Status: model.JobStatusQueued}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var claimed bool
		var claimErr, cancelErr error
		wg.Add(2)
		go func() { defer wg.Done(); claimed, _, claimErr = s.ClaimRun(ctx, id) }()
		go func() { defer wg.Done(); _, cancelErr = s.CancelRender(ctx, "u1", id) }()
		wg.Wait()
		if claimErr != nil || cancelErr != nil {
			t.Fatalf("claim=%v cancel=%v", claimErr, cancelErr)
		}
		if st, _ := s.JobStatus(ctx, id); st != model.JobStatusCanceled {
			t.Fatalf("status = %s, want canceled", st)
		}
		if claimed {
			claimedWins++
			if len(fake.Refunds) != 0 {
				t.Fatalf("claimed job refunded: %+v", fake.Refunds)
			}
		} else {
			cancelWins++
			if len(fake.Refunds) != 1 {
				t.Fatalf("cancel-first job refunds = %+v, want one", fake.Refunds)
			}
		}
	}
	t.Logf("claim won %d, cancel won %d", claimedWins, cancelWins)
}
