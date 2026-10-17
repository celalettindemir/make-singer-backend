// Package credit holds the data layer and business rules of the credit
// system (balances, ledger, store purchases, subscriptions). It runs in its
// own Postgres schema (kredi) and its own migration table; it does not share
// the identity module's pool.
package credit

import (
	"context"
	"embed"
	"fmt"
	"log"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationLockKey is the advisory lock key that lets only one pod in the
// cluster run the credit migrations. It differs from the identity and social
// modules' keys, so the modules never wait for each other.
const migrationLockKey int64 = 0x6B726564693031 // "kredi01"

func migrationNames() ([]string, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Migrate creates the credit schema and applies pending migrations. All the
// work runs on a single connection under an advisory lock: if two pods start
// at the same time during a rolling update, the second waits for the first
// to finish. Each migration runs in its own transaction, so a failure never
// leaves a half-applied schema.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("could not get a connection for the credit migration: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("could not acquire the credit migration lock: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey); err != nil {
			log.Printf("could not release the credit migration lock: %v", err)
			// The session lock lives as long as the connection: close it so the
			// pool drops it and the lock is released (Release discards a closed
			// connection).
			if closeErr := conn.Conn().Close(context.Background()); closeErr != nil {
				log.Printf("could not close the credit migration connection: %v", closeErr)
			}
		}
	}()

	if _, err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS kredi`); err != nil {
		return fmt.Errorf("could not create the credit schema: %w", err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS kredi.schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("could not create kredi.schema_migrations: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM kredi.schema_migrations WHERE version = $1)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("could not check %s: %w", name, err)
		}
		if exists {
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", name, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("could not start a transaction for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("could not apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO kredi.schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("could not record %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("could not commit %s: %w", name, err)
		}
	}
	return nil
}
