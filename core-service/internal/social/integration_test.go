package social

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// jsonAlanlari, degeri JSON'a cevirip ust duzey alanlarini doner.
func jsonAlanlari(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json: %v", err)
	}
	return m
}

// Gercek Servis + PostgresDepo ile yayin yasam dongusu: profil, yayinla,
// hazirlik sonucu, detay, akis, begeni, yama, silme. Is (worker) yerine
// depo.Yayinla dogrudan cagrilir.
func TestButunlesik_YayinYasamDongusu(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	q := &sahteKuyruk{}
	s := NewService(d, q, nil, testR2())
	ctx := context.Background()
	sahip, dinleyici := uuid.NewString(), uuid.NewString()

	if _, err := s.CreatePublication(ctx, sahip, PublicationRequest{}); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("profilsiz yayin: %v", err)
	}
	if _, err := s.SaveProfile(ctx, sahip, ProfileInput{Username: "sahip", DisplayName: "Sahip"}); err != nil {
		t.Fatalf("profil: %v", err)
	}
	if _, err := s.SaveProfile(ctx, dinleyici, ProfileInput{Username: "dinleyici", DisplayName: "D"}); err != nil {
		t.Fatalf("profil: %v", err)
	}

	ist := gecerliIstek()
	ist.MasterURL = "masters/" + sahip + "/p1/m1.wav"
	ist.Stems[0].URL = "stems/" + sahip + "/p1/drums.mp3"
	yanit, err := s.CreatePublication(ctx, sahip, ist)
	if err != nil {
		t.Fatalf("YayinOlustur: %v", err)
	}
	if len(q.gorevler) != 1 {
		t.Fatalf("hazirla isi kurulmali")
	}

	// Hazirlanirken yalnizca sahibine gorunur.
	if y, err := s.Publication(ctx, sahip, yanit.ID); err != nil || y.Status != StatusPreparing {
		t.Fatalf("sahip hazirlanani gormeli: %+v, %v", y, err)
	}
	if _, err := s.Publication(ctx, dinleyici, yanit.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dinleyici hazirlanani gormemeli: %v", err)
	}

	h, err := d.GetPreparation(ctx, yanit.ID)
	if err != nil || h.Preparation.MasterKey != ist.MasterURL {
		t.Fatalf("hazirlik = %+v, %v", h, err)
	}
	master, _ := MasterDestKey(yanit.ID, h.Preparation.MasterKey)
	dalga := make([]float64, 64)
	if err := d.Publish(ctx, yanit.ID, PreparationResult{MasterKey: master, Waveform: dalga}); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}

	detay, err := s.PublicationBySlug(ctx, dinleyici, yanit.Slug)
	if err != nil {
		t.Fatalf("SlugGoster: %v", err)
	}
	m := jsonAlanlari(t, detay)
	for _, alan := range []string{"id", "slug", "baslik", "sahip", "tur", "bpm", "ton", "sureSn", "dalgaFormu",
		"begeniSayisi", "yorumSayisi", "coverSayisi", "begendimMi", "altyapiAcik", "kaynak", "olusturma",
		"aciklama", "yorumAcik", "sozler", "yapi", "durum", "benimMi", "gorunurluk"} {
		if _, ok := m[alan]; !ok {
			t.Errorf("YayinDetayi.%s yok", alan)
		}
	}
	if m["kaynak"] != nil || m["durum"] != "yayinda" || m["benimMi"] != false || len(m["dalgaFormu"].([]any)) != 64 {
		t.Fatalf("detay = %v", m)
	}
	if sahipJSON := m["sahip"].(map[string]any); sahipJSON["kullaniciAdi"] != "sahip" {
		t.Fatalf("sahip = %v", sahipJSON)
	}

	akis, err := s.Feed(ctx, dinleyici, TabForYou, "", 20)
	if err != nil || len(akis.Items) != 1 || akis.NextCursor != nil {
		t.Fatalf("senin icin = %+v, %v", akis, err)
	}
	if kendi, _ := s.Feed(ctx, sahip, TabForYou, "", 20); len(kendi.Items) != 0 {
		t.Fatalf("kendi yayinim senin icin'de olmamali: %+v", kendi.Items)
	}
	if b, err := s.SetLike(ctx, dinleyici, yanit.ID, true); err != nil || b.LikeCount != 1 || !b.LikedByMe {
		t.Fatalf("begeni = %+v, %v", b, err)
	}

	dosya, err := s.Files(ctx, dinleyici, yanit.ID)
	if err != nil || dosya.Master.URL == "" {
		t.Fatalf("dosyalar = %+v, %v", dosya, err)
	}

	takipcilere := VisibilityFollowers
	if _, err := s.UpdatePublication(ctx, sahip, yanit.ID, PublicationPatch{Visibility: &takipcilere}); err != nil {
		t.Fatalf("yama: %v", err)
	}
	if _, err := s.Publication(ctx, dinleyici, yanit.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("takipciler yayini takip etmeyene gorunmemeli: %v", err)
	}
	p, err := s.Profile(ctx, dinleyici, "sahip")
	if err != nil || p.Stats.Songs != 0 {
		t.Fatalf("takip etmeyen icin sarki sayaci 0 olmali: %+v, %v", p, err)
	}
	ben, _ := s.MyProfile(ctx, sahip)
	if ben.Stats.Songs != 1 || !ben.IsMe {
		t.Fatalf("sahibin sayaci = %+v", ben)
	}

	if err := s.DeletePublication(ctx, sahip, yanit.ID); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if err := s.DeletePublication(ctx, sahip, yanit.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci silme: %v", err)
	}
	if len(q.gorevler) != 2 || q.gorevler[1].Type() != TaskTypeDeleteFiles {
		t.Fatalf("dosya silme isi kurulmali: %d", len(q.gorevler))
	}
}

func TestButunlesik_AdUygunlugu(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	s := NewService(d, &sahteKuyruk{}, nil, testR2())
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	for i, ad := range []string{"ali", "ali2"} {
		id := []string{a, b}[i]
		if _, err := s.SaveProfile(ctx, id, ProfileInput{Username: ad, DisplayName: "x"}); err != nil {
			t.Fatalf("profil: %v", err)
		}
	}
	u, err := s.CheckUsername(ctx, uuid.NewString(), "ali")
	if err != nil || u.Available || u.Suggestion == nil || *u.Suggestion != "ali3" {
		t.Fatalf("uygunluk = %+v, %v", u, err)
	}
	if _, err := s.SaveProfile(ctx, b, ProfileInput{Username: "ali", DisplayName: "x"}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("alinmis ad: %v", err)
	}
}
