package identity

import (
	"context"
	"errors"
	"testing"
)

func TestSifreHashlenirVeDogrulanir(t *testing.T) {
	depo := NewFakeUserStore()
	k, err := depo.Create(context.Background(), "ali@ornek.com", "Ali", "cokGizli123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if k.PasswordHash == "" {
		t.Fatal("PasswordHash bos")
	}
	if k.PasswordHash == "cokGizli123" {
		t.Fatal("sifre duz metin saklanmis")
	}
	if !k.PasswordMatches("cokGizli123") {
		t.Error("dogru sifre reddedildi")
	}
	if k.PasswordMatches("yanlis") {
		t.Error("yanlis sifre kabul edildi")
	}
}

// Sifresi olmayan kullanici (Faz 2'de yalnizca federe giris) hicbir
// sifreyi kabul etmemeli. Bos hash ile bos sifre eslesirse, federe
// hesaplara sifresiz girilir.
func TestSifresizKullaniciHicbirSifreyiKabulEtmez(t *testing.T) {
	k := &User{ID: "1", Email: "f@ornek.com", PasswordHash: ""}
	if k.PasswordMatches("") {
		t.Error("bos sifre kabul edildi")
	}
	if k.PasswordMatches("herhangi") {
		t.Error("rastgele sifre kabul edildi")
	}
}

func TestEpostaBuyukKucukDuyarsiz(t *testing.T) {
	depo := NewFakeUserStore()
	ctx := context.Background()
	if _, err := depo.Create(ctx, "Ali@Ornek.com", "Ali", "sifre12345"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := depo.Create(ctx, "ali@ornek.COM", "Ali2", "sifre12345"); !errors.Is(err, ErrEmailInUse) {
		t.Errorf("ikinci kayit hatasi = %v, beklenen ErrEpostaKullanimda", err)
	}
	k, err := depo.ByEmail(ctx, "ALI@ORNEK.COM")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if k.Name != "Ali" {
		t.Errorf("Name = %q, beklenen %q", k.Name, "Ali")
	}
}

func TestOlmayanKullanici(t *testing.T) {
	depo := NewFakeUserStore()
	if _, err := depo.ByEmail(context.Background(), "yok@ornek.com"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("hata = %v, beklenen ErrKullaniciYok", err)
	}
}

func TestKisaSifreReddedilir(t *testing.T) {
	depo := NewFakeUserStore()
	if _, err := depo.Create(context.Background(), "a@ornek.com", "A", "kisa"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("hata = %v, beklenen ErrSifreKisa", err)
	}
}
