package social

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
)

// fakeCopier records the copies; when err is set it returns it on every copy.
type fakeCopier struct {
	copies [][2]string
	err    error
}

func (rec *fakeCopier) Copy(_ context.Context, src, dst string) error {
	if rec.err != nil {
		return rec.err
	}
	rec.copies = append(rec.copies, [2]string{src, dst})
	return nil
}

// coverStore: pubX is userB's everyone-visible publication with backing
// enabled and two stem copies.
func coverStore() *fakeStore {
	store := newFakeStore()
	rec := fakePublication(pubX, userB, VisibilityEveryone, StatusPublished)
	rec.Genre, rec.BPM, rec.MusicalKey, rec.DurationSec = "pop", 120, MusicalKey{Tonic: "A", Mode: "minor"}, 180
	rec.Structure = []StructureItem{{ID: "v1", Type: "verse", Bars: 8}}
	rec.Lyrics = Lyrics{Sections: []LyricsSection{{SectionID: "v1", Type: "verse", Lines: []string{"la"}}}}
	store.publications[pubX] = rec
	store.stemLists = map[string][]StemRecord{pubX: {
		{ID: "drums", Instrument: "Davul", Duration: 180.5, Key: "sosyal/" + pubX + "/stems/drums.mp3"},
		{ID: "bass", Instrument: "Bas", Duration: 179, Key: "sosyal/" + pubX + "/stems/bass"},
	}}
	return store
}

func TestService_StartCover(t *testing.T) {
	store := coverStore()
	s, _ := newTestService(store)
	copier := &fakeCopier{}
	s.WithCopier(copier)

	b, err := s.StartCover(context.Background(), userA, pubX)
	if err != nil {
		t.Fatalf("StartCover: %v", err)
	}
	if b.Source != (SourceSummary{PublicationID: pubX, Title: "Orijinal", OwnerUsername: "sahip"}) {
		t.Fatalf("source = %+v", b.Source)
	}
	if b.Genre != "pop" || b.BPM != 120 || b.MusicalKey.Mode != "minor" || b.DurationSec != 180 || len(b.Structure) != 1 || len(b.Lyrics.Sections) != 1 {
		t.Fatalf("start = %+v", b)
	}
	if len(copier.copies) != 2 || len(b.Stems) != 2 {
		t.Fatalf("copies = %v, stems = %+v", copier.copies, b.Stems)
	}
	folder := "stems/" + userA + "/cover-" + pubX + "/"
	if copier.copies[0] != [2]string{"sosyal/" + pubX + "/stems/drums.mp3", folder + "drums.mp3"} {
		t.Fatalf("first copy = %v", copier.copies[0])
	}
	if copier.copies[1][1] != folder+"bass" {
		t.Fatalf("second copy in the same folder, without extension = %v", copier.copies[1])
	}
	stem := b.Stems[0]
	if stem.ID != "drums" || stem.Instrument != "Davul" || stem.Duration != 180.5 ||
		!strings.HasSuffix(stem.FileURL, "/"+copier.copies[0][1]) || !strings.Contains(stem.FileURL, "makeasinger-private") {
		t.Fatalf("stem = %+v", stem)
	}

	// A repeat start by the same caller overwrites the same keys.
	if _, err := s.StartCover(context.Background(), userA, pubX); err != nil {
		t.Fatal(err)
	}
	if len(copier.copies) != 4 || copier.copies[2][1] != copier.copies[0][1] || copier.copies[3][1] != copier.copies[1][1] {
		t.Fatalf("a repeat start must reuse the same keys: %v", copier.copies)
	}

	// A different caller gets a different folder under their own prefix.
	if _, err := s.StartCover(context.Background(), userC, pubX); err != nil {
		t.Fatal(err)
	}
	other := copier.copies[4][1]
	if other == copier.copies[0][1] || !strings.HasPrefix(other, "stems/"+userC+"/cover-"+pubX+"/") {
		t.Fatalf("a different caller must get their own folder: %v", other)
	}

	// A different source publication gets a different folder.
	store.publications[pubY] = fakePublication(pubY, userB, VisibilityEveryone, StatusPublished)
	store.stemLists[pubY] = []StemRecord{{ID: "drums", Instrument: "Davul", Duration: 1, Key: "sosyal/" + pubY + "/stems/drums.mp3"}}
	if _, err := s.StartCover(context.Background(), userA, pubY); err != nil {
		t.Fatal(err)
	}
	if got := copier.copies[6][1]; got == copier.copies[0][1] || !strings.HasPrefix(got, "stems/"+userA+"/cover-"+pubY+"/") {
		t.Fatalf("a different source must get its own folder: %v", got)
	}
}

func TestService_StartCoverSignedURL(t *testing.T) {
	store := coverStore()
	s := NewService(store, &fakeQueue{}, signingStorage{}, testR2()).WithCopier(&fakeCopier{})
	b, err := s.StartCover(context.Background(), userA, pubX)
	if err != nil || !strings.HasPrefix(b.Stems[0].FileURL, "https://imzali.example/stems/"+userA+"/") {
		t.Fatalf("signed URL = %+v, %v", b, err)
	}
}

// The returned addresses are ordinary user URLs: /api/files/sign resolves
// them with ResolveOwnedKey for the caller (and for nobody else), and account
// deletion's stems/{caller}/ prefix covers them.
func TestService_StartCoverURLResolvesUnderCallerPrefix(t *testing.T) {
	s, _ := newTestService(coverStore())
	b, err := s.StartCover(context.Background(), userA, pubX)
	if err != nil {
		t.Fatal(err)
	}
	for _, stem := range b.Stems {
		key, err := client.ResolveOwnedKey(stem.FileURL, userA, testR2())
		if err != nil || !strings.HasPrefix(key, "stems/"+userA+"/cover-"+pubX+"/") {
			t.Fatalf("key = %q, %v", key, err)
		}
		if _, err := client.ResolveOwnedKey(stem.FileURL, userC, testR2()); !errors.Is(err, client.ErrKeyNotOwned) {
			t.Fatalf("another user must not own the key: %v", err)
		}
	}
	prefixes, err := client.UserPrefixes(userA)
	if err != nil || !slices.Contains(prefixes, "stems/"+userA+"/") {
		t.Fatalf("prefixes = %v, %v", prefixes, err)
	}
}

func TestService_StartCoverWithoutCopier(t *testing.T) {
	s, _ := newTestService(coverStore())
	b, err := s.StartCover(context.Background(), userA, pubX)
	if err != nil || len(b.Stems) != 2 {
		t.Fatalf("start without R2 (dev) = %+v, %v", b, err)
	}
}

func TestService_StartCoverErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(store *fakeStore)
		user  string
		pub   string
		want  error
	}{
		{"backing track disabled", func(store *fakeStore) { store.publications[pubX].BackingEnabled = false }, userA, pubX, ErrBackingDisabled},
		{"cover publication", func(store *fakeStore) { store.publications[pubX].SourcePublicationID = pubY }, userA, pubX, ErrBackingDisabled},
		{"no stem copy", func(store *fakeStore) { store.stemLists[pubX] = []StemRecord{} }, userA, pubX, ErrBackingDisabled},
		{"invisible", func(store *fakeStore) { store.publications[pubX].Visibility = VisibilityFollowers }, userA, pubX, ErrNotFound},
		{"missing", nil, userA, pubY, ErrNotFound},
		{"malformed id", nil, userA, "x", ErrNotFound},
		{"owner's publication in preparation", func(store *fakeStore) { store.publications[pubX].Status = StatusPreparing }, userB, pubX, ErrInvalidStatus},
		{"someone else's publication in preparation", func(store *fakeStore) { store.publications[pubX].Status = StatusPreparing }, userA, pubX, ErrNotFound},
	}
	for _, c := range cases {
		store := coverStore()
		if c.setup != nil {
			c.setup(store)
		}
		s, _ := newTestService(store)
		copier := &fakeCopier{}
		s.WithCopier(copier)
		if _, err := s.StartCover(ctx, c.user, c.pub); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if len(copier.copies) != 0 {
			t.Errorf("%s: no copy must be made", c.name)
		}
	}

	s, _ := newTestService(coverStore())
	s.WithCopier(&fakeCopier{err: errors.New("r2 is down")})
	if _, err := s.StartCover(ctx, userA, pubX); err == nil || errors.Is(err, ErrBackingDisabled) {
		t.Fatalf("a copy error must be a server error: %v", err)
	}
}

func TestService_Covers(t *testing.T) {
	store := coverStore()
	store.feedItems = sampleSummaries(3)
	s, _ := newTestService(store)
	ctx := context.Background()

	page, err := s.Covers(ctx, userA, strings.ToUpper(pubX), "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if store.lastFeed.kind != "cover" || store.lastFeed.owner != pubX || store.lastFeed.viewer != userA || store.lastFeed.limit != 2 {
		t.Fatalf("store call = %+v", store.lastFeed)
	}

	store.lastFeed = feedCall{}
	store.publications[pubX].BackingEnabled = false
	empty, err := s.Covers(ctx, userA, pubX, "", 20)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("empty page with the backing track disabled = %+v, %v", empty, err)
	}
	if store.lastFeed.kind != "" {
		t.Fatal("the store must not be queried while the backing track is disabled")
	}

	store.publications[pubX].Visibility = VisibilityFollowers
	if _, err := s.Covers(ctx, userA, pubX, "", 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invisible source: %v", err)
	}
	if _, err := s.Covers(ctx, userA, pubX, "bozuk!", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("malformed cursor: %v", err)
	}
}
