package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

func ticketService(t *testing.T) *WSTicketService {
	t.Helper()
	rdb := liveRedis(t)
	repo := jobs.NewMemoryStore(nil)
	storeJob(t, repo, &model.Job{ID: testJobID, OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	storeJob(t, repo, &model.Job{ID: testJobID2, OwnerID: "kullanici-a", Status: model.JobStatusRunning})
	return NewWSTicketService(rdb, repo)
}

func TestWSTicketService_SingleUse(t *testing.T) {
	s := ticketService(t)
	issued, err := s.Issue(context.Background(), "kullanici-a", testJobID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(issued.Ticket) != 43 || time.Until(issued.ExpiresAt) > 31*time.Second {
		t.Errorf("ticket = %q, TTL %v", issued.Ticket, issued.ExpiresAt)
	}
	if err := s.Redeem(context.Background(), issued.Ticket, testJobID); err != nil {
		t.Fatalf("first use was rejected: %v", err)
	}
	if err := s.Redeem(context.Background(), issued.Ticket, testJobID); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("want ErrInvalidTicket on the second use, got: %v", err)
	}
}

func TestWSTicketService_NotIssuedForOtherUsersJob(t *testing.T) {
	s := ticketService(t)
	if _, err := s.Issue(context.Background(), "kullanici-b", testJobID); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("want ErrJobNotFound, got: %v", err)
	}
}

func TestWSTicketService_NotUsableForOtherJob(t *testing.T) {
	s := ticketService(t)
	issued, err := s.Issue(context.Background(), "kullanici-a", testJobID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Redeem(context.Background(), issued.Ticket, testJobID2); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("a ticket for is1 must give ErrInvalidTicket for is2, got: %v", err)
	}
}

func TestWSTicketService_MalformedTicketRejectedWithoutRedis(t *testing.T) {
	s := NewWSTicketService(nil, nil) // nil Redis: panics if touched
	for _, ticket := range []string{"", "kisa", "wsbilet:*", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"} {
		if err := s.Redeem(context.Background(), ticket, testJobID); !errors.Is(err, ErrInvalidTicket) {
			t.Errorf("want ErrInvalidTicket for %q, got: %v", ticket, err)
		}
	}
}

func TestWSTicket_UserTicketRejectedForJobRoute(t *testing.T) {
	s := ticketService(t)
	issued, err := s.IssueUser(context.Background(), "kullanici-a")
	if err != nil {
		t.Fatalf("IssueUser: %v", err)
	}
	if err := s.Redeem(context.Background(), issued.Ticket, testJobID); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("a user ticket on the job route must give ErrInvalidTicket, got: %v", err)
	}
	// Even a job id that looks like a user binding is rejected.
	issued, _ = s.IssueUser(context.Background(), "kullanici-a")
	if err := s.Redeem(context.Background(), issued.Ticket, "user:kullanici-a"); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("crafted job id must be rejected, got: %v", err)
	}
}

func TestWSTicket_JobTicketRejectedForUserRoute(t *testing.T) {
	s := ticketService(t)
	issued, _ := s.Issue(context.Background(), "kullanici-a", testJobID)
	if _, err := s.RedeemUser(context.Background(), issued.Ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("a job ticket on the user route must give ErrInvalidTicket, got: %v", err)
	}
}

func TestWSTicket_UserTicketSingleUse(t *testing.T) {
	s := ticketService(t)
	issued, _ := s.IssueUser(context.Background(), "kullanici-a")
	uid, err := s.RedeemUser(context.Background(), issued.Ticket)
	if err != nil || uid != "kullanici-a" {
		t.Fatalf("RedeemUser = %q, %v", uid, err)
	}
	if _, err := s.RedeemUser(context.Background(), issued.Ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Errorf("second use: %v", err)
	}
}
