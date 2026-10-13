package social

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func ornekYeniYayin(sahipID string) PublicationInsert {
	return PublicationInsert{
		ID: uuid.NewString(), Slug: "Ab3dEf9h", OwnerID: sahipID,
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

func TestPostgresDepo_YayinEkleVeGetir(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	if err := d.InsertPublication(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	k, err := d.GetPublication(ctx, y.ID, sahip)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if k.Title != "Gece" || k.Status != StatusPreparing || !k.IsMine || k.Visibility != VisibilityEveryone {
		t.Fatalf("kayit = %+v", k)
	}
	if k.Owner.Username != "sahip" || k.Owner.AvatarColor != AvatarColor(sahip) {
		t.Fatalf("sahip = %+v", k.Owner)
	}
	if k.MusicalKey != (MusicalKey{Tonic: "A", Mode: "minor"}) || k.DurationSec != 184 || k.BPM != 120 {
		t.Fatalf("muzik alanlari = %+v", k.PublicationSummary)
	}
	if k.Waveform == nil || len(k.Waveform) != 0 || k.Source != nil {
		t.Fatalf("dalga formu bos dizi, kaynak nil olmali: %v %v", k.Waveform, k.Source)
	}
	if len(k.Lyrics.Sections) != 1 || k.Lyrics.Sections[0].Lines[1] != "iki" || len(k.Structure) != 1 || k.Structure[0].Bars != 8 {
		t.Fatalf("sozler/yapi = %+v %+v", k.Lyrics, k.Structure)
	}
	if !k.HasStems || k.MasterKey != "" || k.Deleted || k.CreatedAt.Location().String() != "UTC" {
		t.Fatalf("ic alanlar = stem %v master %q silindi %v", k.HasStems, k.MasterKey, k.Deleted)
	}
	if s, err := d.GetPublicationBySlug(ctx, "Ab3dEf9h", uuid.NewString()); err != nil || s.ID != y.ID || s.IsMine {
		t.Fatalf("slug ile = %+v, %v", s, err)
	}
	if _, err := d.GetPublication(ctx, uuid.NewString(), sahip); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan yayin: %v", err)
	}
}

func TestPostgresDepo_SlugCakismasi(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	if err := d.InsertPublication(ctx, ornekYeniYayin(sahip)); err != nil {
		t.Fatalf("ilk: %v", err)
	}
	if err := d.InsertPublication(ctx, ornekYeniYayin(sahip)); !errors.Is(err, errSlugConflict) {
		t.Fatalf("errSlugCakisti bekleniyordu: %v", err)
	}
}

// SQL kosulu ile Go kurali her kombinasyonda ayni karari vermeli.
func TestPostgresDepo_GorunurlukSQLVeGoAyni(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip, takipci, yabanci := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testTakipEkle(t, havuz, takipci, sahip)

	var yayinlar []string
	for _, gr := range []string{VisibilityEveryone, VisibilityFollowers, VisibilityLink} {
		for _, durum := range []string{StatusPreparing, StatusPublished, StatusFailed} {
			for _, silindi := range []bool{false, true} {
				yayinlar = append(yayinlar, testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: gr, Durum: durum, Silindi: silindi}))
			}
		}
	}
	for _, izleyici := range []string{sahip, takipci, yabanci} {
		for _, id := range yayinlar {
			k, err := d.GetPublication(ctx, id, izleyici)
			if err != nil {
				t.Fatalf("YayinGetir: %v", err)
			}
			for _, detay := range []bool{true, false} {
				var sqlKarari bool
				if err := havuz.QueryRow(ctx, fmt.Sprintf(
					`SELECT EXISTS (SELECT 1 FROM sosyal.yayin y WHERE y.id = $2 AND %s)`,
					visibilityCondition("y", "$1", detay)), izleyici, id).Scan(&sqlKarari); err != nil {
					t.Fatalf("kosul: %v", err)
				}
				goKarari := canSee(k.visibilityInput(izleyici), detay)
				if sqlKarari != goKarari {
					t.Errorf("izleyici %s yayin %s/%s silindi=%v detay=%v: SQL %v, Go %v",
						izleyici[:4], k.Visibility, k.Status, k.Deleted, detay, sqlKarari, goKarari)
				}
			}
		}
	}
}

func TestPostgresDepo_SayaclarVeKaynak(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip, coverci, izleyici := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testProfilEkle(t, d, coverci, "coverci")
	orijinal := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Baslik: "Orijinal", AltyapiAcik: true})
	cover := testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Gorunurluk: VisibilityLink})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Durum: StatusFailed})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Silindi: true})
	for _, kisi := range []string{izleyici, coverci} {
		if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)`, kisi, orijinal); err != nil {
			t.Fatalf("begeni: %v", err)
		}
	}
	if _, err := havuz.Exec(ctx, `
		INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, silinme)
		VALUES (gen_random_uuid(), $1, $2, 'a', NULL), (gen_random_uuid(), $1, $2, 'b', now())`, orijinal, izleyici); err != nil {
		t.Fatalf("yorum: %v", err)
	}

	k, err := d.GetPublication(ctx, orijinal, izleyici)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	// Cover sayaci: yayinda ve silinmemis (gorunurluk fark etmez) = 2.
	if k.LikeCount != 2 || k.CommentCount != 1 || k.CoverCount != 2 || !k.LikedByMe {
		t.Fatalf("sayaclar = begeni %d yorum %d cover %d begendim %v", k.LikeCount, k.CommentCount, k.CoverCount, k.LikedByMe)
	}

	c, err := d.GetPublication(ctx, cover, izleyici)
	if err != nil || c.Source == nil {
		t.Fatalf("cover = %+v, %v", c, err)
	}
	if *c.Source != (SourceSummary{PublicationID: orijinal, Title: "Orijinal", OwnerUsername: "sahip", Deleted: false}) {
		t.Fatalf("canli kaynak = %+v", *c.Source)
	}
	if c.SourcePublicationID != orijinal {
		t.Fatalf("KaynakYayinID = %q", c.SourcePublicationID)
	}

	// Yumusak silinen orijinal: son degerler + silindi.
	if err := d.DeletePublication(ctx, orijinal, sahip); err != nil {
		t.Fatalf("YayinSil: %v", err)
	}
	c, _ = d.GetPublication(ctx, cover, izleyici)
	if !c.Source.Deleted || c.Source.Title != "Orijinal" {
		t.Fatalf("silinen kaynak = %+v", *c.Source)
	}
	// Kalici silinen orijinal (hesap silme gibi): cover'daki anlik goruntu.
	// YayinKaldir yalnizca hazirlaniyor satiri sildigi icin ham SQL.
	if _, err := havuz.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1`, orijinal); err != nil {
		t.Fatalf("kalici silme: %v", err)
	}
	c, _ = d.GetPublication(ctx, cover, izleyici)
	if *c.Source != (SourceSummary{PublicationID: orijinal, Title: "Eski baslik", OwnerUsername: "eski_ad", Deleted: true}) {
		t.Fatalf("kalici silinen kaynak = %+v", *c.Source)
	}
}

func TestPostgresDepo_GuncelleSilTekrarDene(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip, baskasi := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	id := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: StatusFailed, AltyapiAcik: true})

	aciklama, kapali := "yeni", false
	if err := d.UpdatePublication(ctx, id, sahip, PublicationPatch{Description: &aciklama, BackingEnabled: &kapali}); err != nil {
		t.Fatalf("guncelle: %v", err)
	}
	k, _ := d.GetPublication(ctx, id, sahip)
	if k.Description != "yeni" || k.BackingEnabled || !k.CommentsEnabled || k.Visibility != VisibilityEveryone {
		t.Fatalf("yalnizca gonderilen alanlar degismeli: %+v", k)
	}
	if err := d.UpdatePublication(ctx, id, baskasi, PublicationPatch{Description: &aciklama}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasinin yamasi: %v", err)
	}

	if err := d.ResetForRetry(ctx, id, baskasi); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi tekrar deneyemez: %v", err)
	}
	if err := d.ResetForRetry(ctx, id, sahip); err != nil {
		t.Fatalf("hata -> hazirlaniyor: %v", err)
	}
	if err := d.ResetForRetry(ctx, id, sahip); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("hazirlaniyor iken ErrDurum bekleniyordu: %v", err)
	}

	if err := d.DeletePublication(ctx, id, baskasi); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baskasi silemez: %v", err)
	}
	if err := d.DeletePublication(ctx, id, sahip); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if err := d.DeletePublication(ctx, id, sahip); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci silme 404 olmali: %v", err)
	}
	if k, err := d.GetPublication(ctx, id, sahip); err != nil || !k.Deleted {
		t.Fatalf("silinmis yayin isaretli donmeli: %+v, %v", k, err)
	}
	if err := d.UpdatePublication(ctx, id, sahip, PublicationPatch{Description: &aciklama}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("silinmis yayin guncellenemez: %v", err)
	}
	if err := d.ResetForRetry(ctx, id, sahip); !errors.Is(err, ErrNotFound) {
		t.Fatalf("silinmis yayin tekrar denenemez: %v", err)
	}
}

func TestPostgresDepo_Hazirlik(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	if err := d.InsertPublication(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	h, err := d.GetPreparation(ctx, y.ID)
	if err != nil || h.OwnerID != sahip || h.Status != StatusPreparing || h.Deleted {
		t.Fatalf("hazirlik = %+v, %v", h, err)
	}
	if h.Preparation.MasterKey != "masters/u/p/m.wav" || len(h.Preparation.Stems) != 1 || h.Preparation.Stems[0].Key != "stems/u/p/s1.mp3" {
		t.Fatalf("hazirlik girdisi = %+v", h.Preparation)
	}
	dalga := make([]float64, 64)
	dalga[3] = 1
	sonuc := PreparationResult{
		MasterKey: "sosyal/" + y.ID + "/master.wav",
		Stems:     []StemRecord{{ID: "s1", Instrument: "drums", Duration: 184, Key: "sosyal/" + y.ID + "/stems/s1.mp3"}},
		Waveform:  dalga,
	}
	if err := d.Publish(ctx, y.ID, sonuc); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	k, _ := d.GetPublication(ctx, y.ID, sahip)
	if k.Status != StatusPublished || len(k.Waveform) != 64 || k.Waveform[3] != 1 || k.MasterKey != sonuc.MasterKey {
		t.Fatalf("yayinlanmis = durum %s dalga %d master %q", k.Status, len(k.Waveform), k.MasterKey)
	}
	var stemler []StemRecord
	var onizleme *string
	if err := havuz.QueryRow(ctx, `SELECT stem_listesi, onizleme_anahtari FROM sosyal.yayin WHERE id = $1`, y.ID).Scan(&stemler, &onizleme); err != nil {
		t.Fatalf("stem_listesi: %v", err)
	}
	if len(stemler) != 1 || stemler[0].Key != sonuc.Stems[0].Key || onizleme != nil {
		t.Fatalf("stemler = %+v onizleme = %v", stemler, onizleme)
	}
	// yayinda iken HataYap bir sey yapmaz.
	if err := d.MarkFailed(ctx, y.ID); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.GetPublication(ctx, y.ID, sahip); k.Status != StatusPublished {
		t.Fatalf("yayinda yayin hataya dusmemeli: %s", k.Status)
	}
	if _, err := d.GetPreparation(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan hazirlik: %v", err)
	}
}

func TestPostgresDepo_HataYapVeSilinmisYayinYayinlanmaz(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	hatali := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: StatusPreparing})
	if err := d.MarkFailed(ctx, hatali); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.GetPublication(ctx, hatali, sahip); k.Status != StatusFailed {
		t.Fatalf("durum = %s", k.Status)
	}
	silinen := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: StatusPreparing, Silindi: true})
	if err := d.Publish(ctx, silinen, PreparationResult{MasterKey: "m"}); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	if k, _ := d.GetPublication(ctx, silinen, sahip); k.Status != StatusPreparing {
		t.Fatalf("silinmis yayin yayinlanmamali: %s", k.Status)
	}
}

// Stems nil olsa bile kayit eklenir ve detay okunur.
func TestPostgresDepo_NilStemlerleEkleVeGetir(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	y.Preparation.Stems = nil
	if err := d.InsertPublication(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	k, err := d.GetPublication(ctx, y.ID, sahip)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if k.HasStems {
		t.Fatal("stem yokken StemVar false olmali")
	}
	var tip string
	if err := havuz.QueryRow(ctx, `SELECT jsonb_typeof(hazirlik->'stems') FROM sosyal.yayin WHERE id = $1`, y.ID).Scan(&tip); err != nil || tip != "array" {
		t.Fatalf("stems dizi olarak saklanmali: %q, %v", tip, err)
	}
}

// Ham {"stems":null} ve null satirlar iceren satir da detayda okunur.
func TestPostgresDepo_NullStemVeSatirlarDetayiBozmaz(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	id := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Hazirlik: `{"masterKey":"masters/u/p/m.wav","stems":null}`})
	if _, err := havuz.Exec(ctx, `UPDATE sosyal.yayin SET sozler = '{"bolumler":[{"bolumId":"b1","tur":"verse","satirlar":null}]}' WHERE id = $1`, id); err != nil {
		t.Fatalf("sozler: %v", err)
	}
	k, err := d.GetPublication(ctx, id, sahip)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if k.HasStems {
		t.Fatal("null stems StemVar false olmali")
	}
	if len(k.Lyrics.Sections) != 1 || k.Lyrics.Sections[0].Lines == nil {
		t.Fatalf("satirlar bos dizi olmali: %+v", k.Lyrics)
	}
}

// HataYap silinmis yayina, YayinKaldir hazirlaniyor disindaki yayina dokunmaz.
func TestPostgresDepo_HataYapVeKaldirKorumalari(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	silinen := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: StatusPreparing, Silindi: true})
	if err := d.MarkFailed(ctx, silinen); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.GetPublication(ctx, silinen, sahip); k.Status != StatusPreparing {
		t.Fatalf("silinmis yayin hataya alinmamali: %s", k.Status)
	}
	yayinda := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: StatusPublished})
	if err := d.RemovePublication(ctx, yayinda); err != nil {
		t.Fatalf("YayinKaldir: %v", err)
	}
	if _, err := d.GetPublication(ctx, yayinda, sahip); err != nil {
		t.Fatalf("yayinda yayin kaldirilmamali: %v", err)
	}
	hazir := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: StatusPreparing})
	if err := d.RemovePublication(ctx, hazir); err != nil {
		t.Fatalf("YayinKaldir: %v", err)
	}
	if _, err := d.GetPublication(ctx, hazir, sahip); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hazirlaniyor yayin kaldirilmali: %v", err)
	}
}
