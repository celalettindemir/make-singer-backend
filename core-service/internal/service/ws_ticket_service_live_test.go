package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/model"
)

func ticketService(t *testing.T) *WSTicketService {
	t.Helper()
	rdb := liveRedis(t)
	storeJob(t, rdb, &model.Job{ID: "is1", OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	storeJob(t, rdb, &model.Job{ID: "is2", OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	return NewWSTicketService(rdb)
}

func TestWSTicketService_SingleUse(t *testing.T) {
	s := ticketService(t)
	issued, err := s.Issue(context.Background(), "kullanici-a", "is1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(issued.Ticket) != 43 || time.Until(issued.ExpiresAt) > 31*time.Second {
		t.Errorf("bilet = %q, son %v", issued.Ticket, issued.ExpiresAt)
	}
	if err := s.Redeem(context.Background(), issued.Ticket, "is1"); err != nil {
		t.Fatalf("ilk kullanim reddedildi: %v", err)
	}
	if err := s.Redeem(context.Background(), issued.Ticket, "is1"); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("ikinci kullanimda ErrBiletGecersiz bekleniyordu, geldi: %v", err)
	}
}

func TestWSTicketService_NotIssuedForOtherUsersJob(t *testing.T) {
	s := ticketService(t)
	if _, err := s.Issue(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestWSTicketService_NotUsableForOtherJob(t *testing.T) {
	s := ticketService(t)
	issued, err := s.Issue(context.Background(), "kullanici-a", "is1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Redeem(context.Background(), issued.Ticket, "is2"); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("is1 bileti is2'de ErrBiletGecersiz vermeli, geldi: %v", err)
	}
}

func TestWSTicketService_MalformedTicketRejectedWithoutRedis(t *testing.T) {
	s := NewWSTicketService(nil) // nil Redis: panics if touched
	for _, ticket := range []string{"", "kisa", "wsbilet:*", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"} {
		if err := s.Redeem(context.Background(), ticket, "is1"); !errors.Is(err, ErrInvalidTicket) {
			t.Errorf("%q icin ErrBiletGecersiz bekleniyordu, geldi: %v", ticket, err)
		}
	}
}
