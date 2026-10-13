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
		t.Fatal("PasswordHash is empty")
	}
	if k.PasswordHash == "cokGizli123" {
		t.Fatal("the password was stored in plain text")
	}
	if !k.PasswordMatches("cokGizli123") {
		t.Error("the correct password was rejected")
	}
	if k.PasswordMatches("yanlis") {
		t.Error("the wrong password was accepted")
	}
}

// A user without a password (federated login only since Phase 2) must
// accept no password at all. If an empty password matched an empty hash,
// federated accounts could be entered without a password.
func TestUser_PasswordlessAcceptsNoPassword(t *testing.T) {
	k := &User{ID: "1", Email: "f@ornek.com", PasswordHash: ""}
	if k.PasswordMatches("") {
		t.Error("an empty password was accepted")
	}
	if k.PasswordMatches("herhangi") {
		t.Error("a random password was accepted")
	}
}

func TestUserStore_EmailCaseInsensitive(t *testing.T) {
	store := NewFakeUserStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, "Ali@Ornek.com", "Ali", "sifre12345"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(ctx, "ali@ornek.COM", "Ali2", "sifre12345"); !errors.Is(err, ErrEmailInUse) {
		t.Errorf("second registration error = %v, want ErrEmailInUse", err)
	}
	k, err := store.ByEmail(ctx, "ALI@ORNEK.COM")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if k.Name != "Ali" {
		t.Errorf("Name = %q, want %q", k.Name, "Ali")
	}
}

func TestUserStore_MissingUser(t *testing.T) {
	store := NewFakeUserStore()
	if _, err := store.ByEmail(context.Background(), "yok@ornek.com"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("error = %v, want ErrUserNotFound", err)
	}
}

func TestUserStore_ShortPasswordRejected(t *testing.T) {
	store := NewFakeUserStore()
	if _, err := store.Create(context.Background(), "a@ornek.com", "A", "kisa"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("error = %v, want ErrPasswordTooShort", err)
	}
}
