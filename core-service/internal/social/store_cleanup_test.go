package social

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresStore_DeleteUserData(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	leaver, stayer := uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, leaver, "giden")
	testInsertProfile(t, store, stayer, "kalan")

	live := testInsertPublication(t, pool, testPublication{OwnerID: leaver, Title: "Orijinal"})
	deleted := testInsertPublication(t, pool, testPublication{OwnerID: leaver, Deleted: true})
	stayerPublication := testInsertPublication(t, pool, testPublication{OwnerID: stayer})
	cover := testInsertPublication(t, pool, testPublication{OwnerID: stayer, SourceID: live})
	testInsertFollow(t, pool, leaver, stayer)
	testInsertFollow(t, pool, stayer, leaver)
	for _, query := range []string{
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + leaver + `', '` + stayerPublication + `')`,
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + stayer + `', '` + live + `')`,
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + stayer + `', '` + stayerPublication + `')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), '` + stayerPublication + `', '` + leaver + `', 'g')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), '` + stayerPublication + `', '` + stayer + `', 'k')`,
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), '` + leaver + `', 'yayin', '` + stayerPublication + `', '` + stayerPublication + `', 'spam')`,
	} {
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	ids, err := store.ListPublicationIDs(ctx, leaver)
	if err != nil {
		t.Fatalf("ListPublicationIDs: %v", err)
	}
	sort.Strings(ids)
	want := []string{live, deleted}
	sort.Strings(want)
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("two publications including deleted ones were expected: %v", ids)
	}

	if err := store.DeleteUserData(ctx, leaver); err != nil {
		t.Fatalf("DeleteUserData: %v", err)
	}
	if err := store.DeleteUserData(ctx, leaver); err != nil {
		t.Fatalf("deleting again must not fail: %v", err)
	}

	var leaverRemaining int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.profil WHERE kullanici_id = $1)
		     + (SELECT count(*) FROM sosyal.yayin WHERE sahip_id = $1)
		     + (SELECT count(*) FROM sosyal.begeni WHERE kullanici_id = $1 OR yayin_id = $2)
		     + (SELECT count(*) FROM sosyal.yorum WHERE yazar_id = $1)
		     + (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1)
		     + (SELECT count(*) FROM sosyal.sikayet WHERE bildiren_id = $1)`, leaver, live).Scan(&leaverRemaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if leaverRemaining != 0 {
		t.Fatalf("%d rows of the deleted user are left", leaverRemaining)
	}

	// Other people's data is intact; the cover stays and its source shows as deleted.
	if _, err := store.GetProfile(ctx, stayer); err != nil {
		t.Fatalf("the remaining profile must not be deleted: %v", err)
	}
	rec, err := store.GetPublication(ctx, stayerPublication, stayer)
	if err != nil || rec.LikeCount != 1 || rec.CommentCount != 1 {
		t.Fatalf("remaining user's publication = %+v, %v", rec, err)
	}
	c, err := store.GetPublication(ctx, cover, stayer)
	if err != nil || c.Source == nil || !c.Source.Deleted || c.Source.PublicationID != live {
		t.Fatalf("the cover must remain and its source must be marked deleted: %+v, %v", c, err)
	}
	if empty, err := store.ListPublicationIDs(ctx, leaver); err != nil || len(empty) != 0 {
		t.Fatalf("the deleted user's publication must not remain: %v %v", empty, err)
	}
}

// traceScenario: the stayer's report on the leaver's comment, the leaver's
// publication, like, follow and own report are set up.
type traceScenario struct {
	leaving, staying, leavingPublication, stayingPublication string
}

func setupTraceScenario(t *testing.T, store *PostgresStore) traceScenario {
	t.Helper()
	pool := store.pool
	s := traceScenario{leaving: uuid.NewString(), staying: uuid.NewString()}
	testInsertProfile(t, store, s.leaving, "giden")
	testInsertProfile(t, store, s.staying, "kalan")
	s.leavingPublication = testInsertPublication(t, pool, testPublication{OwnerID: s.leaving})
	s.stayingPublication = testInsertPublication(t, pool, testPublication{OwnerID: s.staying})
	testInsertFollow(t, pool, s.leaving, s.staying)
	testInsertFollow(t, pool, s.staying, s.leaving)
	leaverComment, stayerComment := uuid.NewString(), uuid.NewString()
	for _, query := range []string{
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + s.leaving + `', '` + s.stayingPublication + `')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES ('` + leaverComment + `', '` + s.stayingPublication + `', '` + s.leaving + `', 'g')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES ('` + stayerComment + `', '` + s.stayingPublication + `', '` + s.staying + `', 'k')`,
		// the stayer reports the leaver's comment: this must go when the comment is deleted.
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), '` + s.staying + `', 'yorum', '` + leaverComment + `', '` + s.stayingPublication + `', 'spam')`,
		// a report on the stayer's own comment (filed by the leaver) goes via the reporter.
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), '` + s.leaving + `', 'yorum', '` + stayerComment + `', '` + s.stayingPublication + `', 'spam')`,
		// a third person's report on the stayer's publication remains.
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), gen_random_uuid(), 'yayin', '` + s.stayingPublication + `', '` + s.stayingPublication + `', 'spam')`,
	} {
		if _, err := pool.Exec(context.Background(), query); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return s
}

func reportCount(t *testing.T, store *PostgresStore) int {
	t.Helper()
	var n int
	if err := store.pool.QueryRow(context.Background(), `SELECT count(*) FROM sosyal.sikayet`).Scan(&n); err != nil {
		t.Fatalf("report count: %v", err)
	}
	return n
}

// Trace deletion removes the profile and interactions, leaves the publication rows;
// reports on the deleted user's comments also go.
func TestPostgresStore_DeleteUserTraces(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()
	s := setupTraceScenario(t, store)

	if err := store.DeleteUserTraces(ctx, s.leaving); err != nil {
		t.Fatalf("DeleteUserTraces: %v", err)
	}
	if err := store.DeleteUserTraces(ctx, s.leaving); err != nil {
		t.Fatalf("deleting traces again must not fail: %v", err)
	}
	if _, err := store.GetProfile(ctx, s.leaving); err == nil {
		t.Fatal("the profile must be deleted")
	}
	if ids, err := store.ListPublicationIDs(ctx, s.leaving); err != nil || len(ids) != 1 || ids[0] != s.leavingPublication {
		t.Fatalf("publication rows must remain: %v %v", ids, err)
	}
	// Without a profile the publication is not visible on any read path.
	if _, err := store.GetPublication(ctx, s.leavingPublication, s.staying); err == nil {
		t.Fatal("the publication of a user whose profile was deleted must not be visible")
	}
	var stayerTraces int
	if err := store.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.begeni WHERE kullanici_id = $1)
		     + (SELECT count(*) FROM sosyal.yorum WHERE yazar_id = $1)
		     + (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1)`, s.leaving).Scan(&stayerTraces); err != nil {
		t.Fatalf("count: %v", err)
	}
	if stayerTraces != 0 {
		t.Fatalf("%d traces of the departed user are left", stayerTraces)
	}
	if n := reportCount(t, store); n != 1 {
		t.Fatalf("only the third party's report must remain, %d left", n)
	}
}

// Full deletion also removes reports on the deleted user's comments.
func TestPostgresStore_DeleteUserData_CommentReports(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	s := setupTraceScenario(t, store)
	if err := store.DeleteUserData(context.Background(), s.leaving); err != nil {
		t.Fatalf("DeleteUserData: %v", err)
	}
	if n := reportCount(t, store); n != 1 {
		t.Fatalf("only the third party's report must remain, %d left", n)
	}
}

// Reports on the deleted user's comments (on others' publications, including
// soft-deleted ones) go; those removed by the publication cascade go;
// reports on others' comments and publications remain.
func TestPostgresStore_DeleteUserData_DeletedCommentAndRemainingReports(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	leaver, stayer, third := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, leaver, "giden")
	testInsertProfile(t, store, stayer, "kalan")
	stayerPublication := testInsertPublication(t, pool, testPublication{OwnerID: stayer})
	leaverPublication := testInsertPublication(t, pool, testPublication{OwnerID: leaver})

	leaverComment := testInsertComment(t, pool, "", stayerPublication, leaver, nil, time.Now())
	deletedComment := testInsertComment(t, pool, "", stayerPublication, leaver, intp(5), time.Now())
	if err := store.DeleteComment(ctx, deletedComment); err != nil {
		t.Fatal(err)
	}
	stayerComment := testInsertComment(t, pool, "", stayerPublication, stayer, nil, time.Now())
	nestedComment := testInsertComment(t, pool, "", leaverPublication, stayer, nil, time.Now())

	fileReport := func(reporter, kind, target, pub string) {
		t.Helper()
		if err := store.InsertReport(ctx, ReportInsert{ID: uuid.NewString(), ReporterID: reporter, TargetType: kind,
			TargetID: target, PublicationID: pub, Reason: "spam"}); err != nil {
			t.Fatal(err)
		}
	}
	fileReport(stayer, TargetComment, leaverComment, stayerPublication)         // goes
	fileReport(third, TargetComment, deletedComment, stayerPublication)         // goes
	fileReport(stayer, TargetPublication, leaverPublication, leaverPublication) // goes via cascade
	fileReport(third, TargetComment, nestedComment, leaverPublication)          // goes via cascade
	fileReport(third, TargetComment, stayerComment, stayerPublication)          // stays
	fileReport(third, TargetPublication, stayerPublication, stayerPublication)  // stays

	if err := store.DeleteUserData(ctx, leaver); err != nil {
		t.Fatalf("DeleteUserData: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT hedef_id::text FROM sosyal.sikayet ORDER BY hedef_turu`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var targets []string
	for rows.Next() {
		var targetID string
		if err := rows.Scan(&targetID); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, targetID)
	}
	assertOrder(t, "remaining reports", targets, []string{stayerPublication, stayerComment})
}
