package kimlik

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SahteUserStore, testler icin bellek ici UserStore. Postgres'in
// lower(email) unique index davranisini taklit eder.
type SahteUserStore struct {
	mu          sync.Mutex
	epostayaGor map[string]*User
	idyeGore    map[string]*User
}

func NewSahteUserStore() *SahteUserStore {
	return &SahteUserStore{
		epostayaGor: map[string]*User{},
		idyeGore:    map[string]*User{},
	}
}

func (s *SahteUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	hash, err := sifreHashle(password)
	if err != nil {
		return nil, err
	}
	anahtar := strings.ToLower(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.epostayaGor[anahtar]; ok {
		return nil, ErrEpostaKullanimda
	}
	k := &User{
		ID:           uuid.NewString(),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	s.epostayaGor[anahtar] = k
	s.idyeGore[k.ID] = k
	return k, nil
}

func (s *SahteUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.epostayaGor[strings.ToLower(email)]; ok {
		return k, nil
	}
	return nil, ErrKullaniciYok
}

func (s *SahteUserStore) ByID(ctx context.Context, id string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.idyeGore[id]; ok {
		return k, nil
	}
	return nil, ErrKullaniciYok
}
