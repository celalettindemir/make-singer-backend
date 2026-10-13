package identity

import (
	"context"
	"errors"
	"testing"
)

func TestUser_PasswordHashedAndVerified(t *testing.T) {
	store := NewFakeUserStore()
	k, err := store.Create(context.Background(), "ali@ornek.com", "Ali", "cokGizli123")
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

// A user without a password (federated login only since Phase 2) must
// accept no password at all. If an empty password matched an empty hash,
// federated accounts could be entered without a password.
func TestUser_PasswordlessAcceptsNoPassword(t *testing.T) {
	k := &User{ID: "1", Email: "f@ornek.com", PasswordHash: ""}
	if k.PasswordMatches("") {
		t.Error("bos sifre kabul edildi")
	}
	if k.PasswordMatches("herhangi") {
		t.Error("rastgele sifre kabul edildi")
	}
}

func TestUserStore_EmailCaseInsensitive(t *testing.T) {
	store := NewFakeUserStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, "Ali@Ornek.com", "Ali", "sifre12345"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(ctx, "ali@ornek.COM", "Ali2", "sifre12345"); !errors.Is(err, ErrEmailInUse) {
		t.Errorf("ikinci kayit hatasi = %v, beklenen ErrEpostaKullanimda", err)
	}
	k, err := store.ByEmail(ctx, "ALI@ORNEK.COM")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if k.Name != "Ali" {
		t.Errorf("Name = %q, beklenen %q", k.Name, "Ali")
	}
}

func TestUserStore_MissingUser(t *testing.T) {
	store := NewFakeUserStore()
	if _, err := store.ByEmail(context.Background(), "yok@ornek.com"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("hata = %v, beklenen ErrKullaniciYok", err)
	}
}

func TestUserStore_ShortPasswordRejected(t *testing.T) {
	store := NewFakeUserStore()
	if _, err := store.Create(context.Background(), "a@ornek.com", "A", "kisa"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("hata = %v, beklenen ErrSifreKisa", err)
	}
}
