package social

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/identity"
)

// testPool connects to the local Postgres, applies the social migrations
// and empties the social tables. The test is skipped when Postgres is absent
// (same pattern and SAME default DSN as the identity tests). TRUNCATE touches
// only the social.* tables; it does not affect the public tables of the
// identity tests.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE sosyal.sikayet, sosyal.yorum, sosyal.begeni, sosyal.takip, sosyal.yayin, sosyal.profil`); err != nil {
		t.Fatalf("could not truncate the social tables: %v", err)
	}
	return pool
}

func TestMigrations_EmbeddedAndSorted(t *testing.T) {
	names, err := migrationNames()
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations: %v %v", names, err)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("migrations are not sorted: %q, %q", names[i-1], names[i])
		}
	}
	content, err := migrationFS.ReadFile("migrations/001_sosyal.sql")
	if err != nil {
		t.Fatalf("could not read 001_sosyal.sql: %v", err)
	}
	for _, table := range []string{"sosyal.profil", "sosyal.yayin", "sosyal.begeni", "sosyal.takip", "sosyal.yorum", "sosyal.sikayet"} {
		if !strings.Contains(string(content), "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("migration %s does not create the table", table)
		}
	}
	if strings.Contains(string(content), "REFERENCES users") || strings.Contains(string(content), "public.users") {
		t.Error("social tables must not have an FK to public.users")
	}
}

// Migrate must not fail when it runs twice (replicas start first).
func TestMigrate_RunsTwiceAndCreatesTables(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'sosyal'
		  AND table_name IN ('profil','yayin','begeni','takip','yorum','sikayet','schema_migrations')`,
	).Scan(&count); err != nil {
		t.Fatalf("could not count the tables: %v", err)
	}
	if count != 7 {
		t.Fatalf("expected 7 social tables, got %d", count)
	}
}

// Deleting a publication also deletes its likes, comments and reports (in-schema cascade).
func TestMigrate_DeletingPublicationCascades(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pubID := "7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	person := "8b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	if _, err := pool.Exec(ctx, `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, tur, bpm, ton_tonik, ton_mod, sure_sn,
			gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik, durum)
		VALUES ($1, 'Abcd1234', $2, 'b', 'pop', 120, 'A', 'minor', 60, 'herkes', false, true,
			'{}', '[]', '{}', 'yayinda')`, pubID, person); err != nil {
		t.Fatalf("could not add the publication: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)`, person, pubID); err != nil {
		t.Fatalf("like: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), $1, $2, 'm')`, pubID, person); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), $1, 'yayin', $2, $2, 'spam')`, person, pubID); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1`, pubID); err != nil {
		t.Fatalf("could not delete the publication: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM sosyal.begeni) + (SELECT count(*) FROM sosyal.yorum) + (SELECT count(*) FROM sosyal.sikayet)`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("cascade did not work, %d rows left", remaining)
	}
}
