package lyricsprovider

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// settingKey is the kredi.ayar row that holds the choice.
const settingKey = "lyrics_provider"

// PostgresStore keeps the choice in kredi.ayar (credit migration 004).
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates the store.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Get returns the stored choice; ok is false when nothing is stored.
func (s *PostgresStore) Get(ctx context.Context) (Name, bool, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT deger FROM kredi.ayar WHERE anahtar = $1`, settingKey).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return Name(value), true, nil
}

// Set writes the choice and who made it.
func (s *PostgresStore) Set(ctx context.Context, name Name, by string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO kredi.ayar (anahtar, deger, degistiren, degisti)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (anahtar) DO UPDATE
		SET deger = EXCLUDED.deger, degistiren = EXCLUDED.degistiren, degisti = now()`,
		settingKey, string(name), by)
	return err
}
