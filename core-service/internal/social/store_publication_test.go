package social

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func samplePublicationInsert(ownerID string) PublicationInsert {
	return PublicationInsert{
		ID: uuid.NewString(), Slug: "Ab3dEf9h", OwnerID: ownerID,
		Title: "Gece", Description: "aciklama", Genre: "pop", BPM: 120,
		MusicalKey: MusicalKey{Tonic: "A", Mode: "minor"}, DurationSec: 184,
		Visibility: VisibilityEveryone, BackingEnabled: true, CommentsEnabled: true,
		Lyrics:    Lyrics{Sections: []LyricsSection{{SectionID: "b1", Type: "verse", Lines: []string{"bir", "iki"}}}},
		Structure: []StructureItem{{ID: "s1", Type: "verse", Bars: 8}},
		Preparation: Preparation{MasterKey: "masters/u/p/m.wav", Stems: []PreparationStem{
			{ID: "s1", Instrument: "drums", Duration: 184, Key: "stems/u/p/s1.mp3"},
		}},
	}
}

func TestPostgresStore_InsertAndGetPublication(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	ins := samplePublicationInsert(owner)
	if err := store.InsertPublication(ctx, ins); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	rec, err := store.GetPublication(ctx, ins.ID, owner)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if rec.Title != "Gece" || rec.Status != StatusPreparing || !rec.IsMine || rec.Visibility != VisibilityEveryone {
		t.Fatalf("kayit = %+v", rec)
	}
	if rec.Owner.Username != "sahip" || rec.Owner.AvatarColor != AvatarColor(owner) {
		t.Fatalf("sahip = %+v", rec.Owner)
	}
	if rec.MusicalKey != (MusicalKey{Tonic: "A", Mode: "minor"}) || rec.DurationSec != 184 || rec.BPM != 120 {
		t.Fatalf("muzik alanlari = %+v", rec.PublicationSummary)
	}
	if rec.Waveform == nil || len(rec.Waveform) != 0 || rec.Source != nil {
		t.Fatalf("dalga formu bos dizi, kaynak nil olmali: %v %v", rec.Waveform, rec.Source)
	}
	if len(rec.Lyrics.Sections) != 1 || rec.Lyrics.Sections[0].Lines[1] != "iki" || len(rec.Structure) != 1 || rec.Structure[0].Bars != 8 {
		t.Fatalf("sozler/yapi = %+v %+v", rec.Lyrics, rec.Structure)
	}
	if !rec.HasStems || rec.MasterKey != "" || rec.Deleted || rec.CreatedAt.Location().String() != "UTC" {
		t.Fatalf("ic alanlar = stem %v master %q silindi %v", rec.HasStems, rec.MasterKey, rec.Deleted)
	}
	if s, err := store.GetPublicationBySlug(ctx, "Ab3dEf9h", uuid.NewString()); err != nil || s.ID != ins.ID || s.IsMine {
		t.Fatalf("slug ile = %+v, %v", s, err)
	}
	if _, err := store.GetPublication(ctx, uuid.NewString(), owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan yayin: %v", err)
	}
}

func TestPostgresStore_SlugConflict(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	if err := store.InsertPublication(ctx, samplePublicationInsert(owner)); err != nil {
		t.Fatalf("ilk: %v", err)
	}
	if err := store.InsertPublication(ctx, samplePublicationInsert(owner)); !errors.Is(err, errSlugConflict) {
		t.Fatalf("errSlugCakisti bekleniyordu: %v", err)
	}
}

// The SQL condition and the Go rule must make the same decision for every combination.
func TestPostgresStore_VisibilitySQLMatchesGo(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner, follower, stranger := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	testInsertFollow(t, pool, follower, owner)

	var pubs []string
	for _, vis := range []string{VisibilityEveryone, VisibilityFollowers, VisibilityLink} {
		for _, status := range []string{StatusPreparing, StatusPublished, StatusFailed} {
			for _, deleted := range []bool{false, true} {
				pubs = append(pubs, testInsertPublication(t, pool, testPublication{OwnerID: owner, Visibility: vis, Status: status, Deleted: deleted}))
			}
		}
	}
	for _, viewer := range []string{owner, follower, stranger} {
		for _, id := range pubs {
			rec, err := store.GetPublication(ctx, id, viewer)
			if err != nil {
				t.Fatalf("YayinGetir: %v", err)
			}
			for _, detail := range []bool{true, false} {
				var sqlDecision bool
				if err := pool.QueryRow(ctx, fmt.Sprintf(
					`SELECT EXISTS (SELECT 1 FROM sosyal.yayin y WHERE y.id = $2 AND %s)`,
					visibilityCondition("y", "$1", detail)), viewer, id).Scan(&sqlDecision); err != nil {
					t.Fatalf("kosul: %v", err)
				}
				goDecision := canSee(rec.visibilityInput(viewer), detail)
				if sqlDecision != goDecision {
					t.Errorf("izleyici %s yayin %s/%s silindi=%v detay=%v: SQL %v, Go %v",
						viewer[:4], rec.Visibility, rec.Status, rec.Deleted, detail, sqlDecision, goDecision)
				}
			}
		}
	}
}

func TestPostgresStore_CountersAndSource(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner, coverOwner, viewer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	testInsertProfile(t, store, coverOwner, "coverci")
	original := testInsertPublication(t, pool, testPublication{OwnerID: owner, Title: "Orijinal", BackingEnabled: true})
	cover := testInsertPublication(t, pool, testPublication{OwnerID: coverOwner, SourceID: original})
	testInsertPublication(t, pool, testPublication{OwnerID: coverOwner, SourceID: original, Visibility: VisibilityLink})
	testInsertPublication(t, pool, testPublication{OwnerID: coverOwner, SourceID: original, Status: StatusFailed})
	testInsertPublication(t, pool, testPublication{OwnerID: coverOwner, SourceID: original, Deleted: true})
	for _, person := range []string{viewer, coverOwner} {
		if _, err := pool.Exec(ctx, `INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)`, person, original); err != nil {
			t.Fatalf("begeni: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, silinme)
		VALUES (gen_random_uuid(), $1, $2, 'a', NULL), (gen_random_uuid(), $1, $2, 'b', now())`, original, viewer); err != nil {
		t.Fatalf("yorum: %v", err)
	}

	rec, err := store.GetPublication(ctx, original, viewer)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	// Cover counter: published and not deleted (visibility does not matter) = 2.
	if rec.LikeCount != 2 || rec.CommentCount != 1 || rec.CoverCount != 2 || !rec.LikedByMe {
		t.Fatalf("sayaclar = begeni %d yorum %d cover %d begendim %v", rec.LikeCount, rec.CommentCount, rec.CoverCount, rec.LikedByMe)
	}

	c, err := store.GetPublication(ctx, cover, viewer)
	if err != nil || c.Source == nil {
		t.Fatalf("cover = %+v, %v", c, err)
	}
	if *c.Source != (SourceSummary{PublicationID: original, Title: "Orijinal", OwnerUsername: "sahip", Deleted: false}) {
		t.Fatalf("canli kaynak = %+v", *c.Source)
	}
	if c.SourcePublicationID != original {
		t.Fatalf("KaynakYayinID = %q", c.SourcePublicationID)
	}

	// Soft-deleted original: last values + deleted.
	if err := store.DeletePublication(ctx, original, owner); err != nil {
		t.Fatalf("YayinSil: %v", err)
	}
	c, _ = store.GetPublication(ctx, cover, viewer)
	if !c.Source.Deleted || c.Source.Title != "Orijinal" {
		t.Fatalf("silinen kaynak = %+v", *c.Source)
	}
	// Hard-deleted original (like account deletion): the snapshot on the cover.
	// RemovePublication deletes only preparing rows, hence raw SQL.
	if _, err := pool.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1`, original); err != nil {
		t.Fatalf("kalici silme: %v", err)
	}
	c, _ = store.GetPublication(ctx, cover, viewer)
	if *c.Source != (SourceSummary{PublicationID: original, Title: "Eski baslik", OwnerUsername: "eski_ad", Deleted: true}) {
		t.Fatalf("kalici silinen kaynak = %+v", *c.Source)
	}
}

func TestPostgresStore_UpdateDeleteRetry(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner, another := uuid.NewString(), uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	id := testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusFailed, BackingEnabled: true})

	description, disabled := "yeni", false
	if err := store.UpdatePublication(ctx, id, owner, PublicationPatch{Description: &description, BackingEnabled: &disabled}); err != nil {
		t.Fatalf("guncelle: %v", err)
	}
	rec, _ := store.GetPublication(ctx, id, owner)
	if rec.Description != "yeni" || rec.BackingEnabled || !rec.CommentsEnabled || rec.Visibility != VisibilityEveryone {
		t.Fatalf("yalnizca gonderilen alanlar degismeli: %+v", rec)
	}
	if err := store.UpdatePublication(ctx, id, another, PublicationPatch{Description: &description}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasinin yamasi: %v", err)
	}

	if err := store.ResetForRetry(ctx, id, another); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi tekrar deneyemez: %v", err)
	}
	if err := store.ResetForRetry(ctx, id, owner); err != nil {
		t.Fatalf("hata -> hazirlaniyor: %v", err)
	}
	if err := store.ResetForRetry(ctx, id, owner); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("hazirlaniyor iken ErrDurum bekleniyordu: %v", err)
	}

	if err := store.DeletePublication(ctx, id, another); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi silemez: %v", err)
	}
	if err := store.DeletePublication(ctx, id, owner); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if err := store.DeletePublication(ctx, id, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci silme 404 olmali: %v", err)
	}
	if rec, err := store.GetPublication(ctx, id, owner); err != nil || !rec.Deleted {
		t.Fatalf("silinmis yayin isaretli donmeli: %+v, %v", rec, err)
	}
	if err := store.UpdatePublication(ctx, id, owner, PublicationPatch{Description: &description}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("silinmis yayin guncellenemez: %v", err)
	}
	if err := store.ResetForRetry(ctx, id, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("silinmis yayin tekrar denenemez: %v", err)
	}
}

func TestPostgresStore_Preparation(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	ins := samplePublicationInsert(owner)
	if err := store.InsertPublication(ctx, ins); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	prep, err := store.GetPreparation(ctx, ins.ID)
	if err != nil || prep.OwnerID != owner || prep.Status != StatusPreparing || prep.Deleted {
		t.Fatalf("hazirlik = %+v, %v", prep, err)
	}
	if prep.Preparation.MasterKey != "masters/u/p/m.wav" || len(prep.Preparation.Stems) != 1 || prep.Preparation.Stems[0].Key != "stems/u/p/s1.mp3" {
		t.Fatalf("hazirlik girdisi = %+v", prep.Preparation)
	}
	waveform := make([]float64, 64)
	waveform[3] = 1
	result := PreparationResult{
		MasterKey: "sosyal/" + ins.ID + "/master.wav",
		Stems:     []StemRecord{{ID: "s1", Instrument: "drums", Duration: 184, Key: "sosyal/" + ins.ID + "/stems/s1.mp3"}},
		Waveform:  waveform,
	}
	if err := store.Publish(ctx, ins.ID, result); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	rec, _ := store.GetPublication(ctx, ins.ID, owner)
	if rec.Status != StatusPublished || len(rec.Waveform) != 64 || rec.Waveform[3] != 1 || rec.MasterKey != result.MasterKey {
		t.Fatalf("yayinlanmis = durum %s dalga %d master %q", rec.Status, len(rec.Waveform), rec.MasterKey)
	}
	var stems []StemRecord
	var preview *string
	if err := pool.QueryRow(ctx, `SELECT stem_listesi, onizleme_anahtari FROM sosyal.yayin WHERE id = $1`, ins.ID).Scan(&stems, &preview); err != nil {
		t.Fatalf("stem_listesi: %v", err)
	}
	if len(stems) != 1 || stems[0].Key != result.Stems[0].Key || preview != nil {
		t.Fatalf("stemler = %+v onizleme = %v", stems, preview)
	}
	// MarkFailed does nothing while the publication is published.
	if err := store.MarkFailed(ctx, ins.ID); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if rec, _ := store.GetPublication(ctx, ins.ID, owner); rec.Status != StatusPublished {
		t.Fatalf("yayinda yayin hataya dusmemeli: %s", rec.Status)
	}
	if _, err := store.GetPreparation(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan hazirlik: %v", err)
	}
}

func TestPostgresStore_MarkFailedAndDeletedNotPublished(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	failed := testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusPreparing})
	if err := store.MarkFailed(ctx, failed); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if rec, _ := store.GetPublication(ctx, failed, owner); rec.Status != StatusFailed {
		t.Fatalf("durum = %s", rec.Status)
	}
	softDeleted := testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusPreparing, Deleted: true})
	if err := store.Publish(ctx, softDeleted, PreparationResult{MasterKey: "m"}); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	if rec, _ := store.GetPublication(ctx, softDeleted, owner); rec.Status != StatusPreparing {
		t.Fatalf("silinmis yayin yayinlanmamali: %s", rec.Status)
	}
}

// Even when Stems is nil, the record is inserted and the detail is read.
func TestPostgresStore_InsertAndGetWithNilStems(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	ins := samplePublicationInsert(owner)
	ins.Preparation.Stems = nil
	if err := store.InsertPublication(ctx, ins); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	rec, err := store.GetPublication(ctx, ins.ID, owner)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if rec.HasStems {
		t.Fatal("stem yokken StemVar false olmali")
	}
	var kind string
	if err := pool.QueryRow(ctx, `SELECT jsonb_typeof(hazirlik->'stems') FROM sosyal.yayin WHERE id = $1`, ins.ID).Scan(&kind); err != nil || kind != "array" {
		t.Fatalf("stems dizi olarak saklanmali: %q, %v", kind, err)
	}
}

// A row with raw {"stems":null} and null values is also readable in the detail.
func TestPostgresStore_NullStemsAndLinesKeepDetailValid(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	id := testInsertPublication(t, pool, testPublication{OwnerID: owner, Preparation: `{"masterKey":"masters/u/p/m.wav","stems":null}`})
	if _, err := pool.Exec(ctx, `UPDATE sosyal.yayin SET sozler = '{"bolumler":[{"bolumId":"b1","tur":"verse","satirlar":null}]}' WHERE id = $1`, id); err != nil {
		t.Fatalf("sozler: %v", err)
	}
	rec, err := store.GetPublication(ctx, id, owner)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if rec.HasStems {
		t.Fatal("null stems StemVar false olmali")
	}
	if len(rec.Lyrics.Sections) != 1 || rec.Lyrics.Sections[0].Lines == nil {
		t.Fatalf("satirlar bos dizi olmali: %+v", rec.Lyrics)
	}
}

// MarkFailed does not touch a deleted publication; RemovePublication does not touch one that is not preparing.
func TestPostgresStore_MarkFailedAndRemoveGuards(t *testing.T) {
	pool := testPool(t)
	store := NewPostgresStore(pool)
	ctx := context.Background()
	owner := uuid.NewString()
	testInsertProfile(t, store, owner, "sahip")
	softDeleted := testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusPreparing, Deleted: true})
	if err := store.MarkFailed(ctx, softDeleted); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if rec, _ := store.GetPublication(ctx, softDeleted, owner); rec.Status != StatusPreparing {
		t.Fatalf("silinmis yayin hataya alinmamali: %s", rec.Status)
	}
	published := testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusPublished})
	if err := store.RemovePublication(ctx, published); err != nil {
		t.Fatalf("YayinKaldir: %v", err)
	}
	if _, err := store.GetPublication(ctx, published, owner); err != nil {
		t.Fatalf("yayinda yayin kaldirilmamali: %v", err)
	}
	ready := testInsertPublication(t, pool, testPublication{OwnerID: owner, Status: StatusPreparing})
	if err := store.RemovePublication(ctx, ready); err != nil {
		t.Fatalf("YayinKaldir: %v", err)
	}
	if _, err := store.GetPublication(ctx, ready, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hazirlaniyor yayin kaldirilmali: %v", err)
	}
}
