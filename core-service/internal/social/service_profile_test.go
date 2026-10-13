package social

import (
	"context"
	"errors"
	"testing"
)

const (
	kullaniciA = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	kullaniciB = "7f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func TestServis_ProfilBenYoksaProfilYok(t *testing.T) {
	s, _ := testServisi(yeniSahteDepo())
	if _, err := s.MyProfile(context.Background(), kullaniciA); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("ErrProfilYok bekleniyordu: %v", err)
	}
}

func TestServis_GecersizKimlik(t *testing.T) {
	s, _ := testServisi(yeniSahteDepo())
	if _, err := s.MyProfile(context.Background(), "kullanici-a"); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("ErrKimlik bekleniyordu: %v", err)
	}
}

func TestServis_ProfilKaydetVeBen(t *testing.T) {
	d := yeniSahteDepo()
	d.sayilar = ProfileCounts{Stats: Stats{Songs: 2, Followers: 5}}
	s, _ := testServisi(d)
	p, err := s.SaveProfile(context.Background(), kullaniciA, ProfileInput{Username: "ali", DisplayName: " Ali ", Bio: "selam"})
	if err != nil {
		t.Fatalf("kaydet: %v", err)
	}
	if p.Username != "ali" || p.DisplayName != "Ali" || !p.IsMe || p.FollowedByMe || p.Stats.Songs != 2 {
		t.Fatalf("profil = %+v", p)
	}
	if p.AvatarColor != AvatarColor(kullaniciA) {
		t.Fatalf("avatar rengi = %d", p.AvatarColor)
	}
	ben, err := s.MyProfile(context.Background(), kullaniciA)
	if err != nil || ben.Username != "ali" {
		t.Fatalf("ben = %+v, %v", ben, err)
	}
}

func TestServis_ProfilKaydetHatalari(t *testing.T) {
	d := yeniSahteDepo()
	s, _ := testServisi(d)
	ctx := context.Background()
	if _, err := s.SaveProfile(ctx, kullaniciA, ProfileInput{Username: ".ali", DisplayName: "A"}); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("gecersiz ad: %v", err)
	}
	if _, err := s.SaveProfile(ctx, kullaniciA, ProfileInput{Username: "ali", DisplayName: ""}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bos gorunen ad: %v", err)
	}
	if _, err := s.SaveProfile(ctx, kullaniciA, ProfileInput{Username: "ali", DisplayName: "A"}); err != nil {
		t.Fatalf("ilk kayit: %v", err)
	}
	if _, err := s.SaveProfile(ctx, kullaniciB, ProfileInput{Username: "ali", DisplayName: "B"}); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("alinmis ad: %v", err)
	}
}

func TestServis_AdUygunMu(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfileRecord{UserID: kullaniciA, Username: "ali"}
	d.profiller[kullaniciB] = &ProfileRecord{UserID: kullaniciB, Username: "ali2"}
	s, _ := testServisi(d)
	ctx := context.Background()

	bos, err := s.CheckUsername(ctx, kullaniciB, "veli")
	if err != nil || !bos.Available || bos.Suggestion != nil {
		t.Fatalf("bos ad = %+v, %v", bos, err)
	}
	kendi, err := s.CheckUsername(ctx, kullaniciA, "ali")
	if err != nil || !kendi.Available {
		t.Fatalf("kendi adi uygun olmali: %+v, %v", kendi, err)
	}
	// ali (A) ve ali2 (B) alinmis: C icin oneri ali3.
	alinmis, err := s.CheckUsername(ctx, "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", "ali")
	if err != nil || alinmis.Available || alinmis.Suggestion == nil || *alinmis.Suggestion != "ali3" {
		t.Fatalf("alinmis ad = %+v, %v", alinmis, err)
	}
	// B icin ali2 kendi adi oldugundan oneri ali2'dir.
	bIcin, err := s.CheckUsername(ctx, kullaniciB, "ali")
	if err != nil || bIcin.Suggestion == nil || *bIcin.Suggestion != "ali2" {
		t.Fatalf("B icin oneri = %+v, %v", bIcin, err)
	}
	if _, err := s.CheckUsername(ctx, kullaniciA, "A!"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("gecersiz ad: %v", err)
	}
}

func TestServis_ProfilGoster(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfileRecord{UserID: kullaniciA, Username: "ali", DisplayName: "Ali"}
	d.sayilar = ProfileCounts{FollowedByMe: true}
	s, _ := testServisi(d)
	ctx := context.Background()
	p, err := s.Profile(ctx, kullaniciB, "ali")
	if err != nil || p.IsMe || !p.FollowedByMe {
		t.Fatalf("profil = %+v, %v", p, err)
	}
	if _, err := s.Profile(ctx, kullaniciB, "yok"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("olmayan profil: %v", err)
	}
	if _, err := s.Profile(ctx, kullaniciB, "X"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gecersiz bicimli ad: %v", err)
	}
}
