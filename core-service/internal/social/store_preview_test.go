package social

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresStore_GetPreviewOnlyEveryoneAndLink(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")

	cases := []struct {
		row     testPublication
		visible bool
		preview bool
	}{
		{testPublication{Slug: "Herkes01", Visibility: VisibilityEveryone}, true, true},
		{testPublication{Slug: "Linkli01", Visibility: VisibilityLink}, true, false},
		{testPublication{Slug: "Takipci1", Visibility: VisibilityFollowers}, false, false},
		{testPublication{Slug: "Hazirla1", Status: StatusPreparing}, false, false},
		{testPublication{Slug: "Hatali01", Status: StatusFailed}, false, false},
		{testPublication{Slug: "Silinmis", Deleted: true}, false, false},
	}
	for _, c := range cases {
		c.row.OwnerID = owner
		id := testInsertPublication(t, pool, c.row)
		if c.preview {
			if _, err := pool.Exec(ctx, `UPDATE sosyal.yayin SET onizleme_anahtari = $2, dalga_formu = '[0.25, 1]' WHERE id = $1`,
				id, "sosyal/"+id+"/onizleme.mp3"); err != nil {
				t.Fatal(err)
			}
		}
		rec, err := store.GetPreview(ctx, c.row.Slug)
		if !c.visible {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: %v, beklenen ErrBulunamadi", c.row.Slug, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.row.Slug, err)
		}
		if rec.Title != "Baslik" || rec.DurationSec != 180 || rec.Genre != "pop" || rec.OwnerID != owner ||
			rec.Owner != (ProfileSummary{Username: "sahip", DisplayName: "sahip", AvatarColor: AvatarColor(owner)}) {
			t.Errorf("%s: kayit = %+v", c.row.Slug, rec)
		}
		if c.preview && (rec.PreviewKey != "sosyal/"+id+"/onizleme.mp3" || len(rec.Waveform) != 2) {
			t.Errorf("%s: onizleme = %q dalga %v", c.row.Slug, rec.PreviewKey, rec.Waveform)
		}
		if !c.preview && (rec.PreviewKey != "" || rec.Waveform == nil || len(rec.Waveform) != 0) {
			t.Errorf("%s: onizlemesiz = %q dalga %#v", c.row.Slug, rec.PreviewKey, rec.Waveform)
		}
	}
	if _, err := store.GetPreview(ctx, "Yok12345"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan slug: %v", err)
	}
}

// An everyone-visible publication by an owner with no profile is not previewable (404).
func TestPostgresStore_GetPreviewOwnerWithoutProfile(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	testInsertPublication(t, pool, testPublication{Slug: "Profsiz1", OwnerID: uuid.NewString()})
	if _, err := store.GetPreview(context.Background(), "Profsiz1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("profilsiz sahip: %v, beklenen ErrBulunamadi", err)
	}
}
