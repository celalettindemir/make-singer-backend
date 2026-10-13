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

	izHata         error
	izlerSilinen   []string
	sil            *seciciSilici // dolu ise iz silme anindaki onek sayisi kaydedilir
	izdekiOnekSayi int
}

func (s *sahteSosyalTemizleyici) DeleteUserTraces(_ context.Context, userID string) error {
	s.izlerSilinen = append(s.izlerSilinen, userID)
	if s.sil != nil {
		s.izdekiOnekSayi = len(s.sil.onekler)
	}
	return s.izHata
}

func (s *sahteSosyalTemizleyici) ListPublicationIDs(context.Context, string) ([]string, error) {
	return s.idler, s.listeHata
}

func (s *sahteSosyalTemizleyici) DeleteUserData(_ context.Context, userID string) error {
	s.silinen = append(s.silinen, userID)
	s.idler = nil // yayin satirlari da gider
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
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1, sosyalY2}, sil: sil}
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
	if !slices.Equal(sos.izlerSilinen, []string{hesapID}) || sos.izdekiOnekSayi != 5 {
		t.Fatalf("izler sosyal onek dongusunden once silinmeli: %v, onek %d", sos.izlerSilinen, sos.izdekiOnekSayi)
	}
}

// R2 kapaliyken de profil (ve izler) hemen silinir ki kullanici gorunmez
// olsun; yayin satirlari kalir ve tekrar ayni kimlikleri listeler.
func TestHesapTemizle_R2HatasindaProfilYineSilinir(t *testing.T) {
	sil := &seciciSilici{hataliOn: "sosyal/"}
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1, sosyalY2}, sil: sil}
	svc := NewHesapService(&sahteKimlik{}, sil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); err == nil {
		t.Fatal("R2 hatasi donmeli")
	}
	if !slices.Equal(sos.izlerSilinen, []string{hesapID}) || sos.izdekiOnekSayi != 5 {
		t.Fatalf("profil R2'den once silinmeli: %v, onek %d", sos.izlerSilinen, sos.izdekiOnekSayi)
	}
	if len(sos.silinen) != 0 {
		t.Fatal("R2 temizligi eksikken yayin satirlari silinmemeli")
	}

	// Gecikmeli tekrar: R2 duzeldi, ayni kimlikler yeniden listelenir.
	sil.hataliOn = ""
	if err := svc.Temizle(context.Background(), hesapID); err != nil {
		t.Fatalf("tekrar: %v", err)
	}
	if !slices.Equal(sil.onekler[12:], []string{"sosyal/" + sosyalY1 + "/", "sosyal/" + sosyalY2 + "/"}) {
		t.Fatalf("tekrar ayni sosyal onekleri silmeli: %v", sil.onekler)
	}
	if !slices.Equal(sos.silinen, []string{hesapID}) {
		t.Fatalf("tekrar sonunda satirlar silinmeli: %v", sos.silinen)
	}
}

// Iz silme hatasi R2 dongusunu durdurmaz; hata birlesik hataya eklenir.
func TestHesapTemizle_IzSilmeHatasiR2yiDurdurmaz(t *testing.T) {
	sil := &seciciSilici{}
	izHata := errors.New("pg kapali")
	sos := &sahteSosyalTemizleyici{idler: []string{sosyalY1, sosyalY2}, izHata: izHata}
	svc := NewHesapService(&sahteKimlik{}, sil, ulasilamazRedis(), &sahteKuyruk{}, sos)
	if err := svc.Temizle(context.Background(), hesapID); !errors.Is(err, izHata) {
		t.Fatalf("iz silme hatasi donmeli: %v", err)
	}
	if len(sil.onekler) != 7 {
		t.Fatalf("iz hatasina ragmen tum onekler silinmeli: %v", sil.onekler)
	}
	if !slices.Equal(sos.silinen, []string{hesapID}) {
		t.Fatalf("R2 basariliyken satirlar silinmeli: %v", sos.silinen)
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
