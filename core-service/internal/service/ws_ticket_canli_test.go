package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/model"
)

func biletServisi(t *testing.T) *WSTicketService {
	t.Helper()
	rdb := canliRedis(t)
	isKaydet(t, rdb, &model.Job{ID: "is1", OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	isKaydet(t, rdb, &model.Job{ID: "is2", OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	return NewWSTicketService(rdb)
}

func TestWSBilet_TekKullanimlik(t *testing.T) {
	s := biletServisi(t)
	b, err := s.Issue(context.Background(), "kullanici-a", "is1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(b.Ticket) != 43 || time.Until(b.ExpiresAt) > 31*time.Second {
		t.Errorf("bilet = %q, son %v", b.Ticket, b.ExpiresAt)
	}
	if err := s.Redeem(context.Background(), b.Ticket, "is1"); err != nil {
		t.Fatalf("ilk kullanim reddedildi: %v", err)
	}
	if err := s.Redeem(context.Background(), b.Ticket, "is1"); !errors.Is(err, ErrBiletGecersiz) {
		t.Errorf("ikinci kullanimda ErrBiletGecersiz bekleniyordu, geldi: %v", err)
	}
}

func TestWSBilet_BaskasininIsineVerilmez(t *testing.T) {
	s := biletServisi(t)
	if _, err := s.Issue(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestWSBilet_BaskaIseKullanilamaz(t *testing.T) {
	s := biletServisi(t)
	b, err := s.Issue(context.Background(), "kullanici-a", "is1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Redeem(context.Background(), b.Ticket, "is2"); !errors.Is(err, ErrBiletGecersiz) {
		t.Errorf("is1 bileti is2'de ErrBiletGecersiz vermeli, geldi: %v", err)
	}
}

func TestWSBilet_BicimsizBiletRedisiSormadanReddedilir(t *testing.T) {
	s := NewWSTicketService(nil) // nil Redis: dokunulursa panik
	for _, b := range []string{"", "kisa", "wsbilet:*", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"} {
		if err := s.Redeem(context.Background(), b, "is1"); !errors.Is(err, ErrBiletGecersiz) {
			t.Errorf("%q icin ErrBiletGecersiz bekleniyordu, geldi: %v", b, err)
		}
	}
}
