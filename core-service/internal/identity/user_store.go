package identity

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

// normalizeEmail brings an email address to ONE single form for STORAGE
// and LOOKUP.
//
// WHY ONE SINGLE HELPER: Create used to apply "strings.TrimSpace(email)"
// while ByEmail applied NO trim at all to its parameter
// (WHERE lower(email) = lower($1)), and the page layer passed the raw form
// value. The result was a REAL production bug: a user who typed the email
// with leading or trailing spaces signed up with " x@y.com ", which was
// stored as "x@y.com", and then could not be found when trying to sign in
// with the SAME input and got "E-posta veya sifre hatali" — the account
// existed but could never be entered with that input. Now Create, ByEmail
// and the login/signup pages all go through the SAME path.
//
// Letter case is DELIBERATELY not changed: uniqueness and lookup are done
// on the Postgres side through lower(email) (unique index + ByEmail
// query), so the form the user typed is kept for display and "E@t" and
// "e@t" are still the same account.
func normalizeEmail(email string) string {
	return strings.TrimSpace(email)
}

type UserStore interface {
	Create(ctx context.Context, email, name, password string) (*User, error)
	ByEmail(ctx context.Context, email string) (*User, error)
	ByID(ctx context.Context, id string) (*User, error)
	// Delete permanently deletes the user. refresh_tokens and
	// federe_kimlikler go with it through the FK's ON DELETE CASCADE.
	Delete(ctx context.Context, id string) error
}

type PostgresUserStore struct {
	pool *pgxpool.Pool
}

func NewPostgresUserStore(pool *pgxpool.Pool) *PostgresUserStore {
	return &PostgresUserStore{pool: pool}
}

func (s *PostgresUserStore) Create(ctx context.Context, email, name, password string) (*User, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &User{
		ID:           uuid.NewString(),
		Email:        normalizeEmail(email),
		Name:         strings.TrimSpace(name),
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at)
		 VALUES ($1, $2, false, $3, $4, $5)`,
		u.ID, u.Email, u.Name, u.PasswordHash, u.CreatedAt)
	if err != nil {
		// 23505 = unique_violation. The only unique constraint is on
		// lower(email), so this error always means "email in use".
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrEmailInUse
		}
		return nil, fmt.Errorf("could not create the user: %w", err)
	}
	return u, nil
}

func (s *PostgresUserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	return s.queryOne(ctx,
		`SELECT id, email, email_verified, name, coalesce(password_hash, ''), created_at
		 FROM users WHERE lower(email) = lower($1)`, normalizeEmail(email))
}

func (s *PostgresUserStore) ByID(ctx context.Context, id string) (*User, error) {
	return s.queryOne(ctx,
		`SELECT id, email, email_verified, name, coalesce(password_hash, ''), created_at
		 FROM users WHERE id = $1`, id)
}

func (s *PostgresUserStore) Delete(ctx context.Context, id string) error {
	// An id that is not a uuid gives a type error in Postgres; for the same
	// result as the fake store we return "not found" (ByID returns a
	// generic error in this case, but "not found" is safer for deletion and
	// consistent with the fake store).
	if _, err := uuid.Parse(id); err != nil {
		return ErrUserNotFound
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("could not delete the user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *PostgresUserStore) queryOne(ctx context.Context, query string, arg any) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, query, arg).Scan(
		&u.ID, &u.Email, &u.EmailVerified, &u.Name, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the user: %w", err)
	}
	return &u, nil
}

// Neither implementation may drift from the interface: if one changes,
// the build breaks.
var (
	_ UserStore = (*PostgresUserStore)(nil)
	_ UserStore = (*FakeUserStore)(nil)
)
