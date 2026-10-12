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

var (
	ErrIdentityNotFound = errors.New("federe kimlik bulunamadi")
	ErrIdentityInUse    = errors.New("federe kimlik baska hesaba bagli")
)

// FederatedStore manages the mapping between (provider, sub) and a user.
// The Phase 2 federation flows (Apple/Google) use this store to find or
// create our User.
type FederatedStore interface {
	// UserByIdentity returns the User linked to the (provider, sub) pair.
	// If there is no mapping it returns ErrIdentityNotFound, not a nil User.
	UserByIdentity(ctx context.Context, provider, sub string) (*User, error)

	// Link attaches a new federated identity to an EXISTING user. It returns
	// ErrIdentityInUse if (provider, sub) is already linked to another account
	// or if this user is already linked to this provider.
	Link(ctx context.Context, provider, sub, userID string) error

	// CreateFederatedUser creates a new user WITHOUT a password and links it
	// with (provider, sub) in the same transaction.
	CreateFederatedUser(ctx context.Context, provider, sub, email, name string, emailVerified bool) (*User, error)

	// ProvidersByUserID returns the names of all providers a user is linked
	// to (for example, to feed the text of K1's 409 page).
	ProvidersByUserID(ctx context.Context, userID string) ([]string, error)
}

type PostgresFederatedStore struct {
	pool *pgxpool.Pool
}

func NewPostgresFederatedStore(pool *pgxpool.Pool) *PostgresFederatedStore {
	return &PostgresFederatedStore{pool: pool}
}

func (d *PostgresFederatedStore) UserByIdentity(ctx context.Context, provider, sub string) (*User, error) {
	var k User
	err := d.pool.QueryRow(ctx,
		`SELECT u.id, u.email, u.email_verified, u.name, coalesce(u.password_hash, ''), u.created_at
		 FROM federe_kimlikler f
		 JOIN users u ON u.id = f.user_id
		 WHERE f.saglayici = $1 AND f.sub = $2`,
		provider, sub,
	).Scan(&k.ID, &k.Email, &k.EmailVerified, &k.Name, &k.PasswordHash, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrIdentityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("federe kimlik okunamadi: %w", err)
	}
	return &k, nil
}

func (d *PostgresFederatedStore) Link(ctx context.Context, provider, sub, userID string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO federe_kimlikler (saglayici, sub, user_id) VALUES ($1, $2, $3)`,
		provider, sub, userID)
	if err != nil {
		// 23505 = unique_violation. Both the (provider, sub) primary key and the
		// (user_id, provider) uniqueness raise this code; the meaning is the same
		// in both cases: this identity conflicts with another mapping.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrIdentityInUse
		}
		return fmt.Errorf("federe kimlik baglanamadi: %w", err)
	}
	return nil
}

// CreateFederatedUser writes a user without a password and its federated
// identity in ONE TRANSACTION. The transaction is required: if an error
// happened between two separate Execs, we would be left with an account
// that has neither a password nor a federated identity, so it could NEVER
// log in -- and since email is unique, the user could not sign up again.
func (d *PostgresFederatedStore) CreateFederatedUser(
	ctx context.Context, provider, sub, email, name string, emailVerified bool,
) (*User, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("islem acilamadi: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	k := &User{
		ID:            uuid.NewString(),
		Email:         normalizeEmail(email),
		EmailVerified: emailVerified,
		Name:          strings.TrimSpace(name),
		CreatedAt:     time.Now().UTC(),
	}
	// password_hash is EXPLICITLY NULL: the password check always returns
	// false on an empty hash, so this account cannot log in with a password.
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at)
		 VALUES ($1, $2, $3, $4, NULL, $5)`,
		k.ID, k.Email, k.EmailVerified, k.Name, k.CreatedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrEmailInUse
		}
		return nil, fmt.Errorf("federe kullanici olusturulamadi: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO federe_kimlikler (saglayici, sub, user_id) VALUES ($1, $2, $3)`,
		provider, sub, k.ID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrIdentityInUse
		}
		return nil, fmt.Errorf("federe kimlik yazilamadi: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("islem tamamlanamadi: %w", err)
	}
	return k, nil
}

func (d *PostgresFederatedStore) ProvidersByUserID(ctx context.Context, userID string) ([]string, error) {
	lines, err := d.pool.Query(ctx,
		`SELECT saglayici FROM federe_kimlikler WHERE user_id = $1 ORDER BY saglayici`, userID)
	if err != nil {
		return nil, fmt.Errorf("saglayicilar okunamadi: %w", err)
	}
	defer lines.Close()

	var providers []string
	for lines.Next() {
		var s string
		if err := lines.Scan(&s); err != nil {
			return nil, fmt.Errorf("saglayicilar okunamadi: %w", err)
		}
		providers = append(providers, s)
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("saglayicilar okunamadi: %w", err)
	}
	return providers, nil
}

// Neither implementation may drift from the interface: if one changes, the build breaks.
var (
	_ FederatedStore = (*PostgresFederatedStore)(nil)
	_ FederatedStore = (*FakeFederatedStore)(nil)
)
