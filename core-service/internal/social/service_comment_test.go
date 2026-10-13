package social

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

const (
	userC    = "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	comment1 = "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	commentC = "c21c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

// commentStore: userA has a profile; pubX is userB's public
// publication (180 s, comments enabled).
func commentStore() *fakeStore {
	store := storeWithProfile()
	rec := fakePublication(pubX, userB, VisibilityEveryone, StatusPublished)
	rec.CommentsEnabled, rec.DurationSec = true, 180
	store.publications[pubX] = rec
	return store
}

func intp(n int) *int { return &n }

func TestService_PostComment(t *testing.T) {
	store := commentStore()
	s, _ := newTestService(store)
	comment, err := s.PostComment(context.Background(), userA, pubX, CommentInput{Text: "  harika  ", TimeMs: intp(180000)})
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if !comment.CanDelete || comment.Text != "harika" || comment.Author.Username != "ali" || *comment.TimeMs != 180000 {
		t.Fatalf("comment = %+v", comment)
	}
	if len(store.insertedComments) != 1 || store.insertedComments[0].PublicationID != pubX || store.insertedComments[0].AuthorID != userA {
		t.Fatalf("inserted = %+v", store.insertedComments)
	}
	if _, err := s.PostComment(context.Background(), userA, pubX, CommentInput{Text: "damgasiz"}); err != nil {
		t.Fatalf("comment without a timestamp: %v", err)
	}
	if store.insertedComments[1].TimeMs != nil {
		t.Fatal("the time of a comment without a timestamp must stay nil")
	}
}

func TestService_PostCommentErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(store *fakeStore)
		user  string
		pub   string
		input CommentInput
		want  error
	}{
		{"empty text", nil, userA, pubX, CommentInput{Text: "   "}, ErrValidation},
		{"long text", nil, userA, pubX, CommentInput{Text: strings.Repeat("ş", 501)}, ErrValidation},
		{"text containing NUL", nil, userA, pubX, CommentInput{Text: "a\x00b"}, ErrValidation},
		{"negative time", nil, userA, pubX, CommentInput{Text: "a", TimeMs: intp(-1)}, ErrValidation},
		{"time past the duration", nil, userA, pubX, CommentInput{Text: "a", TimeMs: intp(180001)}, ErrValidation},
		{"time past int32", func(store *fakeStore) { store.publications[pubX].DurationSec = 3_000_000 }, userA, pubX, CommentInput{Text: "a", TimeMs: intp(math.MaxInt32 + 1)}, ErrValidation},
		{"invisible publication", func(store *fakeStore) { store.publications[pubX].Visibility = VisibilityFollowers }, userA, pubX, CommentInput{Text: "a"}, ErrNotFound},
		{"missing publication", nil, userA, pubY, CommentInput{Text: "a"}, ErrNotFound},
		{"malformed publication id", nil, userA, "x", CommentInput{Text: "a"}, ErrNotFound},
		{"comments disabled", func(store *fakeStore) { store.publications[pubX].CommentsEnabled = false }, userA, pubX, CommentInput{Text: "a"}, ErrCommentsDisabled},
		{"comments disabled, even the owner cannot write", func(store *fakeStore) {
			store.profiles[userB] = &ProfileRecord{UserID: userB, Username: "sahip"}
			store.publications[pubX].CommentsEnabled = false
		}, userB, pubX, CommentInput{Text: "a"}, ErrCommentsDisabled},
		{"no profile", nil, userC, pubX, CommentInput{Text: "a"}, ErrNoProfile},
	}
	for _, c := range cases {
		store := commentStore()
		if c.setup != nil {
			c.setup(store)
		}
		s, _ := newTestService(store)
		if _, err := s.PostComment(ctx, c.user, c.pub, c.input); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if len(store.insertedComments) != 0 {
			t.Errorf("%s: no comment must be written", c.name)
		}
	}
}

// With comments disabled the owner also gets 403; visibility is checked before comments_disabled.
func TestService_CommentsDisabledAndVisibility(t *testing.T) {
	store := commentStore()
	store.publications[pubX].CommentsEnabled = false
	s, _ := newTestService(store)
	ctx := context.Background()
	for _, rec := range []string{userA, userB} {
		if _, err := s.Comments(ctx, rec, pubX, "", 20); !errors.Is(err, ErrCommentsDisabled) {
			t.Errorf("%s: %v, want ErrCommentsDisabled", rec, err)
		}
	}
	store.publications[pubX].Visibility = VisibilityFollowers
	if _, err := s.Comments(ctx, userA, pubX, "", 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invisible publication: %v", err)
	}
}

func TestService_CommentsPageAndCursor(t *testing.T) {
	store := commentStore()
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	store.commentItems = []Comment{
		{ID: comment1, TimeMs: intp(500), CreatedAt: t0},
		{ID: commentC, CreatedAt: t0.Add(time.Minute)},
		{ID: "c31c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", CreatedAt: t0},
	}
	s, _ := newTestService(store)
	ctx := context.Background()

	page, err := s.Comments(ctx, userA, pubX, "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	if _, err := s.Comments(ctx, userA, pubX, *page.NextCursor, 1); err != nil {
		t.Fatal(err)
	}
	cur := store.lastCommentCursor
	if cur == nil || cur.Group != 0 || cur.TimeMs == nil || *cur.TimeMs != 500 || cur.ID != comment1 || !cur.T.Equal(t0) {
		t.Fatalf("timed cursor = %+v", cur)
	}

	page, err = s.Comments(ctx, userA, pubX, "", 2)
	if err != nil || page.NextCursor == nil {
		t.Fatal(err)
	}
	if _, err := s.Comments(ctx, userA, pubX, *page.NextCursor, 2); err != nil {
		t.Fatal(err)
	}
	cur = store.lastCommentCursor
	if cur == nil || cur.Group != 1 || cur.TimeMs != nil || cur.ID != commentC {
		t.Fatalf("cursor without a timestamp = %+v", cur)
	}

	store.commentItems = nil
	empty, err := s.Comments(ctx, userA, pubX, "", 20)
	if err != nil || empty.Items == nil || empty.NextCursor != nil {
		t.Fatalf("empty page = %+v, %v", empty, err)
	}
}

func TestService_CommentCursorValidation(t *testing.T) {
	validID := "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	for _, raw := range []string{
		"bozuk!",
		*encodeCursor(CommentCursor{Group: 0, T: time.Now(), ID: validID}),                  // G=0 but no Z
		*encodeCursor(CommentCursor{Group: 1, TimeMs: intp(3), T: time.Now(), ID: validID}), // G=1 but Z present
		*encodeCursor(CommentCursor{Group: 2, T: time.Now(), ID: validID}),
		*encodeCursor(CommentCursor{Group: 0, TimeMs: intp(-1), T: time.Now(), ID: validID}),
		*encodeCursor(CommentCursor{Group: 1, T: time.Now(), ID: "uuid-degil"}),
		*encodeCursor(CommentCursor{Group: 1, ID: validID}),
	} {
		if _, err := decodeCommentCursor(raw); !errors.Is(err, ErrValidation) {
			t.Errorf("%q: %v, want ErrValidation", raw, err)
		}
	}
}

func TestService_DeleteComment(t *testing.T) {
	ctx := context.Background()
	newRecord := func() *CommentRecord {
		return &CommentRecord{ID: comment1, PublicationID: pubX, AuthorID: userA, PublicationOwnerID: userB}
	}
	// The author and the publication owner can delete.
	for _, rec := range []string{userA, userB} {
		store := commentStore()
		store.commentRecords[comment1] = newRecord()
		s, _ := newTestService(store)
		if err := s.DeleteComment(ctx, rec, comment1); err != nil {
			t.Fatalf("%s could not delete: %v", rec, err)
		}
		if err := s.DeleteComment(ctx, rec, comment1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleting again must be 404: %v", err)
		}
	}
	// A third person: 403 if they can see the publication, 404 if not.
	store := commentStore()
	store.commentRecords[comment1] = newRecord()
	s, _ := newTestService(store)
	if err := s.DeleteComment(ctx, userC, comment1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else: %v, want ErrForbidden", err)
	}
	store.publications[pubX].Visibility = VisibilityFollowers
	if err := s.DeleteComment(ctx, userC, comment1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invisible publication: %v, want ErrNotFound", err)
	}
	if len(store.deletedComments) != 0 {
		t.Fatal("an unauthorized delete must not be written")
	}
	for _, id := range []string{"x", commentC} {
		if err := s.DeleteComment(ctx, userA, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", id, err)
		}
	}
}
