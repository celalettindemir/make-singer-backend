package kimlik

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost 12: 2026 icin makul bir denge. Dusurmek sifre kirmayi
// kolaylastirir, yukseltmek giris gecikmesini hissedilir yapar.
const bcryptCost = 12

var (
	ErrKullaniciYok     = errors.New("kullanici bulunamadi")
	ErrEpostaKullanimda = errors.New("eposta kullanimda")
	ErrSifreKisa        = errors.New("sifre en az 10 karakter olmali")
)

type User struct {
	ID            string
	Email         string
	EmailVerified bool
	Name          string
	PasswordHash  string
	CreatedAt     time.Time
}

// SifreDogru, verilen sifrenin hash ile uyustugunu soyler. Hash bos ise
// HER ZAMAN false doner: sifresiz (yalnizca federe) hesaplara sifreyle
// girilemez.
func (u *User) SifreDogru(sifre string) bool {
	if u == nil || u.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(sifre)) == nil
}

func sifreHashle(sifre string) (string, error) {
	if len([]rune(sifre)) < 10 {
		return "", ErrSifreKisa
	}
	b, err := bcrypt.GenerateFromPassword([]byte(sifre), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
