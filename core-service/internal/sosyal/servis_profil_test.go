package sosyal

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
	if _, err := s.ProfilBen(context.Background(), kullaniciA); !errors.Is(err, ErrProfilYok) {
		t.Fatalf("ErrProfilYok bekleniyordu: %v", err)
	}
}

func TestServis_GecersizKimlik(t *testing.T) {
	s, _ := testServisi(yeniSahteDepo())
	if _, err := s.ProfilBen(context.Background(), "kullanici-a"); !errors.Is(err, ErrKimlik) {
		t.Fatalf("ErrKimlik bekleniyordu: %v", err)
	}
}

func TestServis_ProfilKaydetVeBen(t *testing.T) {
	d := yeniSahteDepo()
	d.sayilar = ProfilSayilari{Istatistik: Istatistik{Sarki: 2, Takipci: 5}}
	s, _ := testServisi(d)
	p, err := s.ProfilKaydet(context.Background(), kullaniciA, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: " Ali ", Bio: "selam"})
	if err != nil {
		t.Fatalf("kaydet: %v", err)
	}
	if p.KullaniciAdi != "ali" || p.GorunenAd != "Ali" || !p.BenMiyim || p.TakipEdiyorMuyum || p.Istatistik.Sarki != 2 {
		t.Fatalf("profil = %+v", p)
	}
	if p.AvatarRengi != AvatarRengi(kullaniciA) {
		t.Fatalf("avatar rengi = %d", p.AvatarRengi)
	}
	ben, err := s.ProfilBen(context.Background(), kullaniciA)
	if err != nil || ben.KullaniciAdi != "ali" {
		t.Fatalf("ben = %+v, %v", ben, err)
	}
}

func TestServis_ProfilKaydetHatalari(t *testing.T) {
	d := yeniSahteDepo()
	s, _ := testServisi(d)
	ctx := context.Background()
	if _, err := s.ProfilKaydet(ctx, kullaniciA, ProfilGirdisi{KullaniciAdi: ".ali", GorunenAd: "A"}); !errors.Is(err, ErrGecersizKullaniciAdi) {
		t.Fatalf("gecersiz ad: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, kullaniciA, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: ""}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("bos gorunen ad: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, kullaniciA, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "A"}); err != nil {
		t.Fatalf("ilk kayit: %v", err)
	}
	if _, err := s.ProfilKaydet(ctx, kullaniciB, ProfilGirdisi{KullaniciAdi: "ali", GorunenAd: "B"}); !errors.Is(err, ErrKullaniciAdiAlinmis) {
		t.Fatalf("alinmis ad: %v", err)
	}
}

func TestServis_AdUygunMu(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali"}
	d.profiller[kullaniciB] = &ProfilKaydi{KullaniciID: kullaniciB, KullaniciAdi: "ali2"}
	s, _ := testServisi(d)
	ctx := context.Background()

	bos, err := s.AdUygunMu(ctx, kullaniciB, "veli")
	if err != nil || !bos.Uygun || bos.Oneri != nil {
		t.Fatalf("bos ad = %+v, %v", bos, err)
	}
	kendi, err := s.AdUygunMu(ctx, kullaniciA, "ali")
	if err != nil || !kendi.Uygun {
		t.Fatalf("kendi adi uygun olmali: %+v, %v", kendi, err)
	}
	// ali (A) ve ali2 (B) alinmis: C icin oneri ali3.
	alinmis, err := s.AdUygunMu(ctx, "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", "ali")
	if err != nil || alinmis.Uygun || alinmis.Oneri == nil || *alinmis.Oneri != "ali3" {
		t.Fatalf("alinmis ad = %+v, %v", alinmis, err)
	}
	// B icin ali2 kendi adi oldugundan oneri ali2'dir.
	bIcin, err := s.AdUygunMu(ctx, kullaniciB, "ali")
	if err != nil || bIcin.Oneri == nil || *bIcin.Oneri != "ali2" {
		t.Fatalf("B icin oneri = %+v, %v", bIcin, err)
	}
	if _, err := s.AdUygunMu(ctx, kullaniciA, "A!"); !errors.Is(err, ErrGecersizKullaniciAdi) {
		t.Fatalf("gecersiz ad: %v", err)
	}
}

func TestServis_ProfilGoster(t *testing.T) {
	d := yeniSahteDepo()
	d.profiller[kullaniciA] = &ProfilKaydi{KullaniciID: kullaniciA, KullaniciAdi: "ali", GorunenAd: "Ali"}
	d.sayilar = ProfilSayilari{TakipEdiyorMuyum: true}
	s, _ := testServisi(d)
	ctx := context.Background()
	p, err := s.ProfilGoster(ctx, kullaniciB, "ali")
	if err != nil || p.BenMiyim || !p.TakipEdiyorMuyum {
		t.Fatalf("profil = %+v, %v", p, err)
	}
	if _, err := s.ProfilGoster(ctx, kullaniciB, "yok"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan profil: %v", err)
	}
	if _, err := s.ProfilGoster(ctx, kullaniciB, "X"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("gecersiz bicimli ad: %v", err)
	}
}
