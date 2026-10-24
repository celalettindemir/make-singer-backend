package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// wsTicketTTL: long enough between getting a ticket and connecting, short if it leaks.
const wsTicketTTL = 30 * time.Second

// ErrInvalidTicket: the ticket does not exist, has expired, was already used
// or belongs to another job. The cases are deliberately not told apart.
var ErrInvalidTicket = errors.New("invalid websocket ticket")

// ticketPattern: 32 bytes of unpadded base64url = 43 characters. Malformed
// input never reaches Redis.
var ticketPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// WSTicketService issues single-use tickets for WebSocket connections.
// React Native's WebSocket cannot send custom headers on iOS; putting the
// access token in the query string would carry it into logs. The ticket
// lives 30 seconds and is single-use, so it can safely sit in the query string.
type WSTicketService struct {
	redis *redis.Client
	repo  jobs.Repo
}

func NewWSTicketService(rdb *redis.Client, repo jobs.Repo) *WSTicketService {
	return &WSTicketService{redis: rdb, repo: repo}
}

// userTicketPrefix marks a ticket bound to a user stream (/ws/me); job
// tickets hold a bare job id.
const userTicketPrefix = "user:"

func ticketKey(ticket string) string { return "wsbilet:" + ticket }

// Issue gives the job's owner a ticket. Ownership is checked here; the ticket
// is bound to that job.
func (s *WSTicketService) Issue(ctx context.Context, userID, jobID string) (*model.WSTicketResponse, error) {
	if _, err := s.repo.GetOwned(ctx, userID, jobID); err != nil {
		return nil, err
	}
	return s.store(ctx, jobID)
}

// IssueUser gives the user a ticket for /ws/me, bound to the user rather than a job.
func (s *WSTicketService) IssueUser(ctx context.Context, userID string) (*model.WSTicketResponse, error) {
	return s.store(ctx, userTicketPrefix+userID)
}

func (s *WSTicketService) store(ctx context.Context, bound string) (*model.WSTicketResponse, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("could not generate the ticket: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.redis.Set(ctx, ticketKey(ticket), bound, wsTicketTTL).Err(); err != nil {
		return nil, fmt.Errorf("could not save the ticket: %w", err)
	}
	return &model.WSTicketResponse{Ticket: ticket, ExpiresAt: time.Now().Add(wsTicketTTL)}, nil
}

// consume reads and deletes the ticket (GETDEL is atomic: single use).
func (s *WSTicketService) consume(ctx context.Context, ticket string) (string, error) {
	if !ticketPattern.MatchString(ticket) {
		return "", ErrInvalidTicket
	}
	bound, err := s.redis.GetDel(ctx, ticketKey(ticket)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrInvalidTicket
	}
	if err != nil {
		return "", fmt.Errorf("could not read the ticket: %w", err)
	}
	return bound, nil
}

// RedeemUser consumes a user ticket and returns its user id. A job ticket
// presented here is consumed too and rejected.
func (s *WSTicketService) RedeemUser(ctx context.Context, ticket string) (string, error) {
	bound, err := s.consume(ctx, ticket)
	if err != nil {
		return "", err
	}
	userID, ok := strings.CutPrefix(bound, userTicketPrefix)
	if !ok || userID == "" {
		return "", ErrInvalidTicket
	}
	return userID, nil
}

// Redeem consumes the ticket. GETDEL is atomic: the same ticket cannot be
// used on two connections. A ticket presented for the wrong job, or a user
// ticket, is consumed too and rejected.
func (s *WSTicketService) Redeem(ctx context.Context, ticket, jobID string) error {
	bound, err := s.consume(ctx, ticket)
	if err != nil {
		return err
	}
	if strings.HasPrefix(bound, userTicketPrefix) || bound != jobID {
		return ErrInvalidTicket
	}
	return nil
}
