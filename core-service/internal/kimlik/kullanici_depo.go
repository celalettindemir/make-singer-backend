package kimlik

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserStore interface {
	Create(ctx context.Context, email, name, password string) (*User, error)
	ByEmail(ctx context.Context, email string) (*User, error)
	ByID(ctx context.Context, id string) (*User, error)
}

type PostgresUserStore struct {
	havuz *pgxpool.Pool
}

func NewPostgresUserStore(havuz *pgxpool.Pool) *PostgresUserStore {
	return &PostgresUserStore{havuz: havuz}
}

func (s *PostgresUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	hash, err := sifreHashle(password)
	if err != nil {
		return nil, err
	}
	k := &User{
		ID:           uuid.NewString(),
		Email:        strings.TrimSpace(email),
		Name:         strings.TrimSpace(name),
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	_, err = s.havuz.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at)
		 VALUES ($1, $2, false, $3, $4, $5)`,
		k.ID, k.Email, k.Name, k.PasswordHash, k.CreatedAt)
	if err != nil {
		// 23505 = unique_violation. Tek unique kisit lower(email)
		// uzerindedir, yani bu hata her zaman "eposta kullanimda" demektir.
		var pgHata *pgconn.PgError
		if errors.As(err, &pgHata) && pgHata.Code == "23505" {
			return nil, ErrEpostaKullanimda
		}
		return nil, fmt.Errorf("kullanici olusturulamadi: %w", err)
	}
	return k, nil
}

func (s *PostgresUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	return s.tekil(ctx,
		`SELECT id, email, email_verified, name, coalesce(password_hash, ''), created_at
		 FROM users WHERE lower(email) = lower($1)`, email)
}

func (s *PostgresUserStore) ByID(ctx context.Context, id string) (*User, error) {
	return s.tekil(ctx,
		`SELECT id, email, email_verified, name, coalesce(password_hash, ''), created_at
		 FROM users WHERE id = $1`, id)
}

func (s *PostgresUserStore) tekil(ctx context.Context, sorgu string, arg any) (*User, error) {
	var k User
	err := s.havuz.QueryRow(ctx, sorgu, arg).Scan(
		&k.ID, &k.Email, &k.EmailVerified, &k.Name, &k.PasswordHash, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrKullaniciYok
	}
	if err != nil {
		return nil, fmt.Errorf("kullanici okunamadi: %w", err)
	}
	return &k, nil
}

// Iki uygulama da arayuzden sapmasin: biri degisirse derleme kirilir.
var (
	_ UserStore = (*PostgresUserStore)(nil)
	_ UserStore = (*SahteUserStore)(nil)
)
