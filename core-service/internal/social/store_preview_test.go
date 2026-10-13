package social

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresDepo_OnizlemeYalnizcaHerkesVeLink(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")

	durumlar := []struct {
		y        testYayin
		gorunur  bool
		onizleme bool
	}{
		{testYayin{Slug: "Herkes01", Gorunurluk: VisibilityEveryone}, true, true},
		{testYayin{Slug: "Linkli01", Gorunurluk: VisibilityLink}, true, false},
		{testYayin{Slug: "Takipci1", Gorunurluk: VisibilityFollowers}, false, false},
		{testYayin{Slug: "Hazirla1", Durum: StatusPreparing}, false, false},
		{testYayin{Slug: "Hatali01", Durum: StatusFailed}, false, false},
		{testYayin{Slug: "Silinmis", Silindi: true}, false, false},
	}
	for _, c := range durumlar {
		c.y.SahipID = sahip
		id := testYayinEkle(t, havuz, c.y)
		if c.onizleme {
			if _, err := havuz.Exec(ctx, `UPDATE sosyal.yayin SET onizleme_anahtari = $2, dalga_formu = '[0.25, 1]' WHERE id = $1`,
				id, "sosyal/"+id+"/onizleme.mp3"); err != nil {
				t.Fatal(err)
			}
		}
		k, err := d.GetPreview(ctx, c.y.Slug)
		if !c.gorunur {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: %v, beklenen ErrBulunamadi", c.y.Slug, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.y.Slug, err)
		}
		if k.Title != "Baslik" || k.DurationSec != 180 || k.Genre != "pop" || k.OwnerID != sahip ||
			k.Owner != (ProfileSummary{Username: "sahip", DisplayName: "sahip", AvatarColor: AvatarColor(sahip)}) {
			t.Errorf("%s: kayit = %+v", c.y.Slug, k)
		}
		if c.onizleme && (k.PreviewKey != "sosyal/"+id+"/onizleme.mp3" || len(k.Waveform) != 2) {
			t.Errorf("%s: onizleme = %q dalga %v", c.y.Slug, k.PreviewKey, k.Waveform)
		}
		if !c.onizleme && (k.PreviewKey != "" || k.Waveform == nil || len(k.Waveform) != 0) {
			t.Errorf("%s: onizlemesiz = %q dalga %#v", c.y.Slug, k.PreviewKey, k.Waveform)
		}
	}
	if _, err := d.GetPreview(ctx, "Yok12345"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan slug: %v", err)
	}
}

// Profili olmayan sahibin herkes yayini da onizlenmez (404).
func TestPostgresDepo_OnizlemeProfilsizSahip(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	testYayinEkle(t, havuz, testYayin{Slug: "Profsiz1", SahipID: uuid.NewString()})
	if _, err := d.GetPreview(context.Background(), "Profsiz1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("profilsiz sahip: %v, beklenen ErrBulunamadi", err)
	}
}
