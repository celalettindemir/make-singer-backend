package identity

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost 12: a reasonable balance for 2026. Lowering it makes
// cracking passwords easier, raising it makes the login delay noticeable.
const bcryptCost = 12

var (
	ErrUserNotFound     = errors.New("user not found")
	ErrEmailInUse       = errors.New("email in use")
	ErrPasswordTooShort = errors.New("sifre en az 10 karakter olmali")
)

type User struct {
	ID            string
	Email         string
	EmailVerified bool
	Name          string
	PasswordHash  string
	CreatedAt     time.Time
}

// PasswordMatches reports whether the given password matches the hash. If
// the hash is empty it ALWAYS returns false: accounts without a password
// (federated only) cannot be signed in to with a password.
func (u *User) PasswordMatches(password string) bool {
	if u == nil || u.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

func hashPassword(password string) (string, error) {
	if len([]rune(password)) < 10 {
		return "", ErrPasswordTooShort
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
