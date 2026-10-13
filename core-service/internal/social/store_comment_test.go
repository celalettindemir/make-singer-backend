package social

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testInsertComment writes a comment at the given time and returns its id.
func testInsertComment(t *testing.T, pool *pgxpool.Pool, id, pub, author string, timeMs *int, createdAt time.Time) string {
	t.Helper()
	if id == "" {
		id = uuid.NewString()
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, zaman_ms, olusturma)
		VALUES ($1, $2, $3, 'metin', $4, $5)`, id, pub, author, timeMs, createdAt); err != nil {
		t.Fatalf("could not add the comment: %v", err)
	}
	return id
}

// allCommentPages walks the comment list in pages of limit; it builds each
// page's cursor from the last item, as the service does.
func allCommentPages(t *testing.T, store *PostgresStore, pub, viewer string, limit int) []Comment {
	t.Helper()
	var (
		all    []Comment
		cursor *CommentCursor
	)
	for pageNo := 0; pageNo < 20; pageNo++ {
		items, err := store.ListComments(context.Background(), pub, viewer, cursor, limit)
		if err != nil {
			t.Fatalf("page %d: %v", pageNo, err)
		}
		s := buildCommentPage(items, limit)
		all = append(all, s.Items...)
		if s.NextCursor == nil {
			return all
		}
		if cursor, err = decodeCommentCursor(*s.NextCursor); err != nil {
			t.Fatalf("could not decode the cursor: %v", err)
		}
	}
	t.Fatal("pagination did not finish")
	return nil
}

func TestPostgresStore_CommentOrderAndPaging(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	owner, author, viewer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	testInsertProfile(t, store, author, "yazar")
	pub := testInsertPublication(t, pool, testPublication{OwnerID: owner})
	otherPub := testInsertPublication(t, pool, testPublication{OwnerID: owner})
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)

	// Expected order: timed ones by time_ms ascending (ties: created, id
	// ascending), then untimed ones by created descending (ties: id descending).
	z100a := testInsertComment(t, pool, "00000000-0000-4000-8000-0000000000a1", pub, author, intp(100), t0)
	z100b := testInsertComment(t, pool, "00000000-0000-4000-8000-0000000000a2", pub, author, intp(100), t0)
	z100c := testInsertComment(t, pool, "", pub, owner, intp(100), t0.Add(time.Second))
	z0 := testInsertComment(t, pool, "", pub, author, intp(0), t0.Add(5*time.Minute))
	z900 := testInsertComment(t, pool, "", pub, author, intp(900), t0.Add(-time.Minute))
	newest := testInsertComment(t, pool, "", pub, author, nil, t0.Add(10*time.Minute))
	sameInstantB := testInsertComment(t, pool, "ffffffff-0000-4000-8000-0000000000b1", pub, author, nil, t0.Add(5*time.Minute))
	sameInstantA := testInsertComment(t, pool, "00000000-0000-4000-8000-0000000000b1", pub, owner, nil, t0.Add(5*time.Minute))
	oldest := testInsertComment(t, pool, "", pub, author, nil, t0)
	// Not in the list: a deleted comment, a comment on another publication.
	softDeleted := testInsertComment(t, pool, "", pub, author, intp(50), t0)
	if err := store.DeleteComment(context.Background(), softDeleted); err != nil {
		t.Fatal(err)
	}
	testInsertComment(t, pool, "", otherPub, author, nil, t0)

	want := []string{z0, z100a, z100b, z100c, z900, newest, sameInstantB, sameInstantA, oldest}
	for _, limit := range []int{1, 2, 3, 4, 50} {
		var ids []string
		for _, pub := range allCommentPages(t, store, pub, viewer, limit) {
			ids = append(ids, pub.ID)
		}
		assertOrder(t, "comment order", ids, want)
	}

	// canDelete depends on the viewer: the author their own comment, the owner all of them.
	for viewerID, wantCanDelete := range map[string]int{viewer: 0, author: 7, owner: 9} {
		n := 0
		for _, pub := range allCommentPages(t, store, pub, viewerID, 50) {
			if pub.CanDelete {
				n++
			}
		}
		if n != wantCanDelete {
			t.Errorf("viewer %s: deletable %d, want %d", viewerID, n, wantCanDelete)
		}
	}

	// commentCount does not count deleted ones.
	rec, err := store.GetPublication(context.Background(), pub, viewer)
	if err != nil || rec.CommentCount != 9 {
		t.Fatalf("yorumSayisi = %d, %v; want 9", rec.CommentCount, err)
	}
}

func TestPostgresStore_InsertGetDeleteComment(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner, author := uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	testInsertProfile(t, store, author, "yazar")
	pub := testInsertPublication(t, pool, testPublication{OwnerID: owner})

	id := uuid.NewString()
	ins, err := store.InsertComment(ctx, CommentInsert{ID: id, PublicationID: pub, AuthorID: author, Text: "selam", TimeMs: intp(1200)})
	if err != nil {
		t.Fatalf("InsertComment: %v", err)
	}
	if ins.ID != id || ins.Text != "selam" || *ins.TimeMs != 1200 || ins.Author.Username != "yazar" ||
		ins.Author.AvatarColor != AvatarColor(author) || ins.CreatedAt.IsZero() || ins.CreatedAt.Location() != time.UTC {
		t.Fatalf("comment = %+v", ins)
	}
	untimed, err := store.InsertComment(ctx, CommentInsert{ID: uuid.NewString(), PublicationID: pub, AuthorID: author, Text: "a"})
	if err != nil || untimed.TimeMs != nil {
		t.Fatalf("without a timestamp = %+v, %v", untimed, err)
	}

	rec, err := store.GetComment(ctx, id)
	if err != nil || rec.PublicationID != pub || rec.AuthorID != author || rec.PublicationOwnerID != owner || rec.Deleted {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	if err := store.DeleteComment(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteComment(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete again: %v", err)
	}
	if rec, err := store.GetComment(ctx, id); err != nil || !rec.Deleted {
		t.Fatalf("deleted record = %+v, %v", rec, err)
	}
	if _, err := store.GetComment(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing comment: %v", err)
	}
	if _, err := store.InsertComment(ctx, CommentInsert{ID: uuid.NewString(), PublicationID: pub, AuthorID: author, Text: "x", TimeMs: intp(-1)}); err == nil {
		t.Fatal("a negative zaman_ms must hit the CHECK constraint")
	}
}

// A comment by an author with no profile (being deleted) is not listed.
func TestPostgresStore_ListCommentsHidesAuthorWithoutProfile(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	owner, author, noProfile := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	testInsertProfile(t, store, author, "yazar")
	pub := testInsertPublication(t, pool, testPublication{OwnerID: owner})
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	visible := testInsertComment(t, pool, "", pub, author, nil, t0)
	testInsertComment(t, pool, "", pub, noProfile, nil, t0.Add(time.Minute))
	testInsertComment(t, pool, "", pub, noProfile, intp(10), t0)

	items := allCommentPages(t, store, pub, owner, 20)
	if len(items) != 1 || items[0].ID != visible {
		t.Fatalf("comments = %+v, want only %s", items, visible)
	}
}
