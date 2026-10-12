package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
)

const imzaKullanici = "u1"

func ozelURL(key string) string {
	return "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/" + key + "?X-Amz-Signature=eski"
}

func TestFileSign_SirayiKoruyupTazeAdresUretir(t *testing.T) {
	depo := &sahteDepo{}
	svc := NewFileService(depo, testR2Cfg())
	girdi := []string{ozelURL("stems/u1/p1/a.mp3"), ozelURL("vocals/u1/p1/s1/t1.wav")}
	got, err := svc.Sign(context.Background(), imzaKullanici, girdi)
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(got.Files) != 2 {
		t.Fatalf("2 dosya bekleniyordu: %+v", got.Files)
	}
	for i, key := range []string{"stems/u1/p1/a.mp3", "vocals/u1/p1/s1/t1.wav"} {
		if !strings.Contains(got.Files[i].URL, key) || strings.Contains(got.Files[i].URL, "eski") {
			t.Errorf("dosya %d = %q, taze %s adresi bekleniyordu", i, got.Files[i].URL, key)
		}
		if got.Files[i].ExpiresAt == nil {
			t.Errorf("dosya %d icin expiresAt bekleniyordu", i)
		}
	}
}

func TestFileSign_YabanciAnahtarReddedilir(t *testing.T) {
	_, err := NewFileService(&sahteDepo{}, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{ozelURL("stems/u1/p1/a.mp3"), ozelURL("stems/u2/p1/b.mp3")})
	if !errors.Is(err, client.ErrKeyNotOwned) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, gelen %v", err)
	}
}

func TestFileSign_TaninmayanKonakReddedilir(t *testing.T) {
	_, err := NewFileService(&sahteDepo{}, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{"https://kotu.ornek/stems/u1/p1/a.mp3"})
	if !errors.Is(err, client.ErrInvalidURL) {
		t.Fatalf("ErrGecersizURL bekleniyordu, gelen %v", err)
	}
}

func TestFileSign_YabanciVarsaHicImzalanmaz(t *testing.T) {
	depo := &sahteDepo{}
	_, _ = NewFileService(depo, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{ozelURL("stems/u1/p1/a.mp3"), ozelURL("stems/u2/p1/b.mp3")})
	if depo.urlForCnt != 0 {
		t.Fatalf("yabanci anahtar varken imza uretilmemeli, URLFor %d kez cagrildi", depo.urlForCnt)
	}
}

func TestFileSign_PublicAnahtardaExpiresYok(t *testing.T) {
	// storage nil: ResolveURL imzasiz adres ve sifir zaman doner.
	got, err := NewFileService(nil, testR2Cfg()).Sign(context.Background(), imzaKullanici,
		[]string{"https://makesinger-cdn.celalettindemir.dev/exports/u1/e1.mp3"})
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if got.Files[0].ExpiresAt != nil {
		t.Fatalf("public anahtarda expiresAt olmamali: %v", got.Files[0].ExpiresAt)
	}
}
