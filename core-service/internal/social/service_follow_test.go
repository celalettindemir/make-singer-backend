package social

import (
	"context"
	"errors"
	"testing"
)

func takipDeposu() *sahteDepo {
	d := profilliDepo()
	d.profiller[kullaniciB] = &ProfileRecord{UserID: kullaniciB, Username: "berk", DisplayName: "Berk"}
	return d
}

func TestServis_TakipEtVeBirak(t *testing.T) {
	d := takipDeposu()
	s, _ := testServisi(d)
	ctx := context.Background()

	for i := 0; i < 2; i++ { // tekrari guvenli
		durum, err := s.SetFollow(ctx, kullaniciA, "berk", true)
		if err != nil || durum.Followers != 1 || !durum.FollowedByMe {
			t.Fatalf("takip %d = %+v, %v", i, durum, err)
		}
	}
	if !d.takipler[[2]string{kullaniciA, kullaniciB}] {
		t.Fatal("takip A -> B yazilmali")
	}
	for i := 0; i < 2; i++ {
		durum, err := s.SetFollow(ctx, kullaniciA, "berk", false)
		if err != nil || durum.Followers != 0 || durum.FollowedByMe {
			t.Fatalf("birakma %d = %+v, %v", i, durum, err)
		}
	}
}

func TestServis_TakipHatalari(t *testing.T) {
	d := takipDeposu()
	s, _ := testServisi(d)
	ctx := context.Background()
	durumlar := []struct {
		ad, kullanici string
		takip         bool
		beklenen      error
	}{
		{"ali", kullaniciA, true, ErrSelfFollow},
		{"ali", kullaniciA, false, ErrSelfFollow},
		{"yok_biri", kullaniciA, true, ErrNotFound},
		{".kotu", kullaniciA, true, ErrNotFound},
		{"berk", "uuid-degil", true, ErrInvalidIdentity},
	}
	for _, c := range durumlar {
		if _, err := s.SetFollow(ctx, c.kullanici, c.ad, c.takip); !errors.Is(err, c.beklenen) {
			t.Errorf("%s/%v: %v, beklenen %v", c.ad, c.takip, err, c.beklenen)
		}
	}
	if len(d.takipler) != 0 {
		t.Fatalf("hatali isteklerde takip yazilmamali: %v", d.takipler)
	}
}

func TestServis_OnerilerOzetVeSinir(t *testing.T) {
	d := takipDeposu()
	s, _ := testServisi(d)
	ctx := context.Background()

	bos, err := s.Suggestions(ctx, kullaniciA)
	if err != nil || bos == nil || len(bos) != 0 {
		t.Fatalf("bos oneriler [] olmali: %#v, %v", bos, err)
	}
	if d.sonOneri != [2]any{kullaniciA, 10} {
		t.Fatalf("depo cagrisi = %v, beklenen {A, 10}", d.sonOneri)
	}

	d.oneriKayitlar = []ProfileRecord{*d.profiller[kullaniciB]}
	o, err := s.Suggestions(ctx, kullaniciA)
	if err != nil || len(o) != 1 {
		t.Fatalf("oneriler = %+v, %v", o, err)
	}
	if o[0] != (ProfileSummary{Username: "berk", DisplayName: "Berk", AvatarColor: AvatarColor(kullaniciB)}) {
		t.Fatalf("ozet = %+v", o[0])
	}
}
