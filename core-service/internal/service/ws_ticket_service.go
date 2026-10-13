package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// wsTicketTTL: long enough between getting a ticket and connecting, short if it leaks.
const wsTicketTTL = 30 * time.Second

// ErrInvalidTicket: the ticket does not exist, has expired, was already used
// or belongs to another job. The cases are deliberately not told apart.
var ErrInvalidTicket = errors.New("websocket bileti gecersiz")

// ticketPattern: 32 bytes of unpadded base64url = 43 characters. Malformed
// input never reaches Redis.
var ticketPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// WSTicketService issues single-use tickets for WebSocket connections.
// React Native's WebSocket cannot send custom headers on iOS; putting the
// access token in the query string would carry it into logs. The ticket
// lives 30 seconds and is single-use, so it can safely sit in the query string.
type WSTicketService struct {
	redis *redis.Client
}

func NewWSTicketService(rdb *redis.Client) *WSTicketService {
	return &WSTicketService{redis: rdb}
}

func ticketKey(ticket string) string { return "wsbilet:" + ticket }

// Issue gives the job's owner a ticket. Ownership is checked here; the ticket
// is bound to that job.
func (s *WSTicketService) Issue(ctx context.Context, userID, jobID string) (*model.WSTicketResponse, error) {
	if _, err := readOwnedJob(ctx, s.redis, userID, jobID); err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("bilet uretilemedi: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.redis.Set(ctx, ticketKey(ticket), jobID, wsTicketTTL).Err(); err != nil {
		return nil, fmt.Errorf("bilet kaydedilemedi: %w", err)
	}
	return &model.WSTicketResponse{Ticket: ticket, ExpiresAt: time.Now().Add(wsTicketTTL)}, nil
}

// Redeem consumes the ticket. GETDEL is atomic: the same ticket cannot be
// used on two connections. A ticket presented for the wrong job is consumed too.
func (s *WSTicketService) Redeem(ctx context.Context, ticket, jobID string) error {
	if !ticketPattern.MatchString(ticket) {
		return ErrInvalidTicket
	}
	boundJobID, err := s.redis.GetDel(ctx, ticketKey(ticket)).Result()
	if errors.Is(err, redis.Nil) {
		return ErrInvalidTicket
	}
	if err != nil {
		return fmt.Errorf("bilet okunamadi: %w", err)
	}
	if boundJobID != jobID {
		return ErrInvalidTicket
	}
	return nil
}
