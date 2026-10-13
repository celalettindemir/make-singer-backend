package social

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func ozetler(n int) []PublicationSummary {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	o := make([]PublicationSummary, n)
	for i := range o {
		o[i].ID = fmt.Sprintf("%08d-1b2c-4d3e-8f9a-0b1c2d3e4f5a", i)
		o[i].CreatedAt = t0.Add(-time.Duration(i) * time.Minute)
	}
	return o
}

func TestServis_AkisYeniSayfalari(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Feed(ctx, kullaniciA, TabNew, "", 2)
	if err != nil || len(sayfa.Items) != 2 || sayfa.NextCursor == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.tur != "yeni" || d.sonAkis.limit != 2 || d.sonAkis.zaman != nil || d.sonAkis.izleyici != kullaniciA {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}
	if _, err := s.Feed(ctx, kullaniciA, TabNew, *sayfa.NextCursor, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if d.sonAkis.zaman == nil || d.sonAkis.zaman.ID != d.akisOgeleri[1].ID || !d.sonAkis.zaman.T.Equal(d.akisOgeleri[1].CreatedAt) {
		t.Fatalf("imlec son ogeyi gostermeli: %+v", d.sonAkis.zaman)
	}

	d.akisOgeleri = ozetler(2)
	son, err := s.Feed(ctx, kullaniciA, TabFollowing, "", 2)
	if err != nil || len(son.Items) != 2 || son.NextCursor != nil || d.sonAkis.tur != "takip" {
		t.Fatalf("son sayfa = %+v, %v", son, err)
	}
	d.akisOgeleri = nil
	bos, err := s.Feed(ctx, kullaniciA, TabNew, "", 20)
	if err != nil || bos.Items == nil || len(bos.Items) != 0 {
		t.Fatalf("bos sayfa ogeler [] olmali: %+v, %v", bos, err)
	}
}

func TestServis_AkisSeninIcinReferansSabit(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	an := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return an }
	ctx := context.Background()

	sayfa, err := s.Feed(ctx, kullaniciA, TabForYou, "", 2)
	if err != nil || len(sayfa.Items) != 2 || sayfa.NextCursor == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	ref := an.Add(referenceSkew)
	if !d.sonAkis.referans.Equal(ref) || d.sonAkis.puan != nil {
		t.Fatalf("ilk sayfa referansi simdi arti pay olmali: %+v", d.sonAkis)
	}
	s.now = func() time.Time { return an.Add(time.Hour) }
	if _, err := s.Feed(ctx, kullaniciA, TabForYou, *sayfa.NextCursor, 2); err != nil {
		t.Fatalf("ikinci sayfa: %v", err)
	}
	if !d.sonAkis.referans.Equal(ref) {
		t.Fatalf("sonraki sayfa ayni referansi kullanmali: %v", d.sonAkis.referans)
	}
	if d.sonAkis.puan == nil || d.sonAkis.puan.Score != 99 || d.sonAkis.puan.ID != d.akisOgeleri[1].ID {
		t.Fatalf("puan imleci = %+v", d.sonAkis.puan)
	}
}

// Gelecekteki bir referans zamani simdi arti paya kirpilir; istemci puanlamayi
// kaydiramaz ve sonraki imlec kirpilmis zamani tasir.
func TestServis_AkisSeninIcinGelecekReferansKirpilir(t *testing.T) {
	d := yeniSahteDepo()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	an := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return an }
	gelecek := encodeCursor(ScoreCursor{Score: 5, ID: d.akisOgeleri[0].ID, RefMs: an.Add(24 * time.Hour).UnixMilli()})
	sayfa, err := s.Feed(context.Background(), kullaniciA, TabForYou, *gelecek, 2)
	if err != nil {
		t.Fatalf("gelecek imlec: %v", err)
	}
	ref := an.Add(referenceSkew)
	if !d.sonAkis.referans.Equal(ref) {
		t.Fatalf("referans simdi arti paya kirpilmali: %v", d.sonAkis.referans)
	}
	sonraki, err := decodeScoreCursor(*sayfa.NextCursor)
	if err != nil || sonraki.RefMs != ref.UnixMilli() {
		t.Fatalf("sonraki imlec kirpilmis zamani tasimali: %+v, %v", sonraki, err)
	}
}

func TestServis_AkisHatalari(t *testing.T) {
	s, _ := testServisi(yeniSahteDepo())
	ctx := context.Background()
	if _, err := s.Feed(ctx, kullaniciA, "populer", "", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("gecersiz sekme: %v", err)
	}
	if _, err := s.Feed(ctx, kullaniciA, TabNew, "bozuk!", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("bozuk imlec: %v", err)
	}
	if _, err := s.Feed(ctx, kullaniciA, TabForYou, "bozuk!", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("bozuk puan imleci: %v", err)
	}
}

func TestServis_ProfilYayinlari(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciB] = &ProfileRecord{UserID: kullaniciB, Username: "veli"}
	d.akisOgeleri = ozetler(1)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.ProfilePublications(ctx, kullaniciA, "veli", "coverlar", "", 20)
	if err != nil || len(sayfa.Items) != 1 || sayfa.NextCursor != nil {
		t.Fatalf("sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.sahip != kullaniciB || d.sonAkis.izleyici != kullaniciA || !d.sonAkis.coverlar {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}
	if _, err := s.ProfilePublications(ctx, kullaniciA, "veli", "", "", 20); err != nil || d.sonAkis.coverlar {
		t.Fatalf("bos tur sarkilar olmali: %v %+v", err, d.sonAkis)
	}
	if _, err := s.ProfilePublications(ctx, kullaniciA, "veli", "videolar", "", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("gecersiz tur: %v", err)
	}
	if _, err := s.ProfilePublications(ctx, kullaniciA, "yok", "sarkilar", "", 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan profil: %v", err)
	}
}

func TestServis_Begeni(t *testing.T) {
	d := yeniSahteDepo()
	d.yayinlar[yayinX] = sahteYayin(yayinX, kullaniciB, VisibilityEveryone, StatusPublished)
	d.yayinlar[yayinY] = sahteYayin(yayinY, kullaniciB, VisibilityFollowers, StatusPublished)
	s, _ := testServisi(d)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		b, err := s.SetLike(ctx, kullaniciA, yayinX, true)
		if err != nil || !b.LikedByMe || b.LikeCount != 1 {
			t.Fatalf("begen %d = %+v, %v", i, b, err)
		}
	}
	b, err := s.SetLike(ctx, kullaniciA, yayinX, false)
	if err != nil || b.LikedByMe || b.LikeCount != 0 {
		t.Fatalf("kaldir = %+v, %v", b, err)
	}
	if _, err := s.SetLike(ctx, kullaniciA, yayinY, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gorulmeyen yayin begenilemez: %v", err)
	}
}
