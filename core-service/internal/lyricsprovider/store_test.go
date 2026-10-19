package lyricsprovider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/identity"
)

// TestPostgresStore_GetSet runs against the local test Postgres (same DSN as
// the credit tests) and is skipped without it.
func TestPostgresStore_GetSet(t *testing.T) {
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := identity.Connect(ctx, dsn)
	if err != nil {
		t.Skip("no local Postgres, skipping")
	}
	t.Cleanup(pool.Close)
	if err := credit.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM kredi.ayar WHERE anahtar = $1`, settingKey); err != nil {
		t.Fatal(err)
	}
	s := NewPostgresStore(pool)
	if _, ok, err := s.Get(context.Background()); err != nil || ok {
		t.Fatalf("empty Get = %v, %v", ok, err)
	}
	for _, name := range []Name{OpenAI, Anthropic} {
		if err := s.Set(context.Background(), name, "a@b.c"); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := s.Get(context.Background()); err != nil || !ok || got != name {
			t.Fatalf("Get = %v, %v, %v; want %v", got, ok, err, name)
		}
	}
}
