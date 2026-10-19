// Package lyricsprovider picks the AI provider that writes lyrics (R-LYR-11).
// The admin's choice lives in one Postgres setting; the Selector caches it
// for a short time so every pod follows a change within the TTL.
package lyricsprovider

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/makeasinger/api/internal/client"
)

// Name is a lyrics provider. Its values are part of the wire contract.
type Name string

const (
	OpenAI    Name = "openai"
	Anthropic Name = "anthropic"
	Groq      Name = "groq"
)

// ErrUnavailable means the provider is unknown or has no configured API key.
var ErrUnavailable = errors.New("lyrics provider unavailable")

// ParseName returns the provider named s.
func ParseName(s string) (Name, error) {
	switch n := Name(s); n {
	case OpenAI, Anthropic, Groq:
		return n, nil
	}
	return "", fmt.Errorf("unknown lyrics provider %q", s)
}

// Store keeps the admin's choice.
type Store interface {
	Get(ctx context.Context) (name Name, ok bool, err error)
	Set(ctx context.Context, name Name, by string) error
}

// Selector answers which client writes lyrics now.
type Selector struct {
	clients  map[Name]client.ChatClient
	store    Store
	fallback Name
	ttl      time.Duration
	now      func() time.Time

	mu       sync.Mutex
	cached   Name
	hasCache bool
	readAt   time.Time
	fixed    client.ChatClient
}

// NewSelector creates a selector over the given clients. fallback is used
// while the store has no choice, or when the chosen provider is not configured.
func NewSelector(clients map[Name]client.ChatClient, store Store, fallback Name, ttl time.Duration, now func() time.Time) *Selector {
	return &Selector{clients: clients, store: store, fallback: fallback, ttl: ttl, now: now}
}

// Fixed returns a selector that always answers c (tests, e2e).
func Fixed(c client.ChatClient) *Selector {
	return &Selector{fixed: c}
}

// Active returns the provider to use for one request. A store error keeps
// the last known choice (or the fallback): lyrics never fail on the setting.
func (s *Selector) Active(ctx context.Context) (Name, client.ChatClient) {
	if s.store == nil {
		return "", s.fixed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasCache || s.now().Sub(s.readAt) >= s.ttl {
		name, ok, err := s.store.Get(ctx)
		switch {
		case err != nil:
			log.Printf("lyrics provider: could not read the setting, keeping %q: %v", s.choiceLocked(), err)
		case ok:
			s.cached, s.hasCache = name, true
		default:
			s.cached, s.hasCache = s.fallback, true
		}
		s.readAt = s.now()
	}
	name := s.choiceLocked()
	if !s.available(name) {
		name = s.fallback
	}
	return name, s.clients[name]
}

func (s *Selector) choiceLocked() Name {
	if s.hasCache {
		return s.cached
	}
	return s.fallback
}

func (s *Selector) available(name Name) bool {
	c, ok := s.clients[name]
	return ok && c != nil && c.IsConfigured()
}

// Available returns the configured providers, sorted.
func (s *Selector) Available() []Name {
	var names []Name
	for name := range s.clients {
		if s.available(name) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

// Set stores the admin's choice and applies it at once on this pod.
func (s *Selector) Set(ctx context.Context, name Name, by string) error {
	if _, err := ParseName(string(name)); err != nil || !s.available(name) || s.store == nil {
		return ErrUnavailable
	}
	previous, _ := s.Active(ctx)
	if err := s.store.Set(ctx, name, by); err != nil {
		return fmt.Errorf("could not store the lyrics provider: %w", err)
	}
	s.mu.Lock()
	s.cached, s.hasCache, s.readAt = name, true, s.now()
	s.mu.Unlock()
	log.Printf("lyrics provider %s -> %s by %s", previous, name, by)
	return nil
}
