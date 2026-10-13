package social

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresStore_DuplicateReportCreatesNoRow(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner, reporter := uuid.NewString(), uuid.NewString()
	pub := testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: "hazirlaniyor"})
	comment := testInsertComment(t, pool, "", pub, owner, nil, time.Now())

	newReport := func(kind, target, reason string) ReportInsert {
		return ReportInsert{ID: uuid.NewString(), ReporterID: reporter, TargetType: kind, TargetID: target, PublicationID: pub, Reason: reason}
	}
	for _, s := range []ReportInsert{
		newReport(TargetPublication, pub, "spam"),
		newReport(TargetPublication, pub, "telif"), // duplicate: the first record stays
		newReport(TargetComment, comment, "taciz"),
	} {
		if err := store.InsertReport(ctx, s); err != nil {
			t.Fatalf("SikayetEkle: %v", err)
		}
	}
	var n int
	var reason string
	if err := pool.QueryRow(ctx, `SELECT count(*), min(neden) FILTER (WHERE hedef_turu = 'yayin')
		FROM sosyal.sikayet WHERE bildiren_id = $1 AND yayin_id = $2`, reporter, pub).Scan(&n, &reason); err != nil {
		t.Fatal(err)
	}
	if n != 2 || reason != "spam" {
		t.Fatalf("kayit sayisi %d, yayin nedeni %q; beklenen 2 ve ilk neden spam", n, reason)
	}

	// When a publication is permanently deleted, its reports go by cascade.
	if err := store.RemovePublication(ctx, pub); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sosyal.sikayet WHERE bildiren_id = $1`, reporter).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade sonrasi %d sikayet, %v", n, err)
	}
}
