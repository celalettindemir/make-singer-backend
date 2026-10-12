package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type sahteSosyalTemizleyici struct {
	idler     []string
	listeHata error
	silinen   []string
}

func (s *sahteSosyalTemizleyici) YayinlariListele(context.Context, string) ([]string, error) {
	return s.idler, s.listeHata
}

func (s *sahteSosyalTemizleyici) KullaniciVerisiniSil(_ context.Context, userID string) error {
	s.silinen = append(s.silinen, userID)
	return nil
}

// seciciSilici, yalnizca verilen onekte hata doner.
type seciciSilici struct {
	onekler  []string
	hataliOn string
}

func (s *seciciSilici) DeletePrefix(_ context.Context, prefix string) (int, error) {
	s.onekler = append(s.onekler, prefix)
	if s.hataliOn != "" && strings.HasPrefix(prefix, s.hataliOn) {
		return 0, errors.New("r2 kapali")
	}
	return 1, nil
}

const (
	sosyalY1 = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	sosyalY2 = "9b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

func TestHesapSil_SosyalOnekleriVeSatirlariSiler(t *testing.T) {
	sil := &seciciSilici{}
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1, sosyalY2}}
	svc := NewHesapService(&sahteKimlik{}, sil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Sil(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(sil.onekler) != 7 {
		t.Fatalf("5 kullanici + 2 sosyal onek bekleniyordu: %v", sil.onekler)
	}
	if !slices.Equal(sil.onekler[5:], []string{"sosyal/" + sosyalY1 + "/", "sosyal/" + sosyalY2 + "/"}) {
		t.Fatalf("sosyal onekler = %v", sil.onekler[5:])
	}
	if !slices.Equal(sos.silinen, []string{hesapID}) {
		t.Fatalf("sosyal satirlar silinmeli: %v", sos.silinen)
	}
}

// R2 hatasinda satirlar kalir: gecikmeli gorev kimlikleri yeniden bulur.
func TestHesapTemizle_SosyalR2HatasindaSatirlarKalir(t *testing.T) {
	sil := &seciciSilici{hataliOn: "sosyal/" + sosyalY2}
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1, sosyalY2}}
	svc := NewHesapService(&sahteKimlik{}, sil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); err == nil {
		t.Fatal("R2 hatasi donmeli")
	}
	if len(sos.silinen) != 0 {
		t.Fatal("R2 temizligi eksikken satirlar silinmemeli")
	}
	if len(sil.onekler) != 7 {
		t.Fatalf("hata olsa da tum onekler denenmeli: %v", sil.onekler)
	}
}

func TestHesapTemizle_SosyalListeHatasi(t *testing.T) {
	sos := &sahteSosyalTemizleyici{listeHata: errors.New("pg kapali")}
	svc := NewHesapService(&sahteKimlik{}, &seciciSilici{}, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); err == nil {
		t.Fatal("liste hatasi donmeli")
	}
	if len(sos.silinen) != 0 {
		t.Fatal("liste okunamadan satir silinmemeli")
	}
}

// R2 yoksa dosya yoktur; satirlar yine silinir.
func TestHesapTemizle_R2YokkenSosyalSatirlarSilinir(t *testing.T) {
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1}}
	svc := NewHesapService(&sahteKimlik{}, nil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if !slices.Equal(sos.silinen, []string{hesapID}) {
		t.Fatalf("satirlar silinmeli: %v", sos.silinen)
	}
}
