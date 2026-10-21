package credit

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/identity"
)

// testPool connects to the local Postgres, applies the credit migrations
// and empties the credit tables. The test is skipped when Postgres is absent
// (same pattern and SAME default DSN as the social tests). TRUNCATE touches
// only the kredi.* tables.
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
		`TRUNCATE kredi.hesap, kredi.hareket, kredi.eylem, kredi.abonelik, kredi.satin_alma,
			kredi.dilim, kredi.dilim_hareket, kredi.kampanya, kredi.kampanya_katilim`); err != nil {
		t.Fatalf("could not truncate the credit tables: %v", err)
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
	content, err := migrationFS.ReadFile("migrations/001_kredi.sql")
	if err != nil {
		t.Fatalf("could not read 001_kredi.sql: %v", err)
	}
	for _, table := range []string{"kredi.hesap", "kredi.hareket", "kredi.eylem", "kredi.abonelik", "kredi.satin_alma"} {
		if !strings.Contains(string(content), "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("migration does not create %s", table)
		}
	}
	if strings.Contains(string(content), "REFERENCES users") || strings.Contains(string(content), "public.users") {
		t.Error("credit tables must not have an FK to public.users")
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
		WHERE table_schema = 'kredi'
		  AND table_name IN ('hesap','hareket','eylem','abonelik','satin_alma','schema_migrations')`,
	).Scan(&count); err != nil {
		t.Fatalf("could not count the tables: %v", err)
	}
	if count != 6 {
		t.Fatalf("expected 6 credit tables, got %d", count)
	}
	var idx int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = 'kredi'
		  AND indexname IN ('abonelik_kullanici_idx','satin_alma_kullanici_idx','eylem_olusturuldu_idx')`,
	).Scan(&idx); err != nil {
		t.Fatalf("could not count the indexes: %v", err)
	}
	if idx != 3 {
		t.Fatalf("expected 3 indexes from migration 002, got %d", idx)
	}
}

func TestCosts_Of(t *testing.T) {
	c := Costs{RenderMix: 23, RenderFocus: 43, RenderStems: 63, LyricsGenerate: 2, LyricsRewrite: 1}
	cases := map[Operation]int{
		OpRenderMix: 23, OpRenderFocus: 43, OpRenderStems: 63,
		OpLyricsGenerate: 2, OpLyricsRewrite: 1, Operation("bogus"): 0,
	}
	for op, want := range cases {
		if got := c.Of(op); got != want {
			t.Errorf("Of(%q) = %d, want %d", op, got, want)
		}
	}
}

// Migration 003 creates the lot and campaign tables, replaces the buckets
// with debt and widens the movement kinds.
func TestMigrations_003CreatesLotTables(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	var tables int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'kredi'
		  AND table_name IN ('dilim','dilim_hareket','kampanya','kampanya_katilim')`,
	).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 4 {
		t.Fatalf("expected 4 new tables, got %d", tables)
	}
	column := func(table, name string) bool {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'kredi' AND table_name = $1 AND column_name = $2`, table, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	for _, c := range [][2]string{
		{"hesap", "borc"}, {"hareket", "tutar"}, {"hareket", "yapan"}, {"hareket", "notu"},
		{"hareket", "kampanya_id"}, {"hareket", "proje_id"}, {"hareket", "kademe"}, {"dilim", "iade"}, {"abonelik", "sonraki_urun"}, {"dilim", "sure_islendi"},
	} {
		if !column(c[0], c[1]) {
			t.Errorf("missing column kredi.%s.%s", c[0], c[1])
		}
	}
	for _, c := range [][2]string{
		{"hesap", "plan_bakiye"}, {"hesap", "kalici_bakiye"}, {"hesap", "plan_donem_sonu"},
		{"hesap", "hediye_alindi"}, {"hareket", "plan_delta"}, {"hareket", "kalici_delta"},
	} {
		if column(c[0], c[1]) {
			t.Errorf("v1 column kredi.%s.%s still exists", c[0], c[1])
		}
	}
	var idx int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes WHERE schemaname = 'kredi'
		  AND indexname IN ('dilim_kullanici_bitis_idx','hareket_olusturuldu_idx','kampanya_tek_aktif_idx')`,
	).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	if idx != 3 {
		t.Fatalf("expected 3 indexes from migration 003, got %d", idx)
	}
	u := "00000000-0000-0000-0000-000000000003"
	if _, err := pool.Exec(ctx, `
		INSERT INTO kredi.hareket (kullanici_id, tur, ref, tutar) VALUES ($1, 'plan_sure', 'm003', -5)`, u); err != nil {
		t.Fatalf("plan_sure must be accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kredi.hareket (kullanici_id, tur, ref, tutar) VALUES ($1, 'hediye', 'm003', 5)`, u); err == nil {
		t.Error("hediye must be rejected")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kredi.hareket (kullanici_id, tur, ref, tutar, kademe) VALUES ($1, 'plan_dolum', 'm003k', 5, 'gold')`, u); err == nil {
		t.Error("an unknown tier must be rejected")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kredi.hareket (kullanici_id, tur, ref, tutar, kademe, proje_id)
		VALUES ($1, 'plan_dolum', 'm003t', 5, 'pro', gen_random_uuid())`, u); err != nil {
		t.Errorf("tier pro with a project id must be accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kredi.dilim (kullanici_id, kaynak, ref, ilk_miktar, kalan) VALUES ($1, 'iade', 'm003', 5, 5)`, u); err == nil {
		t.Error("lot source iade must be rejected")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kredi.dilim (kullanici_id, kaynak, ref, ilk_miktar, kalan) VALUES ($1, 'paket', 'm003', 5, 6)`, u); err == nil {
		t.Error("kalan above ilk_miktar must be rejected")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO kredi.abonelik (magaza, orijinal_islem_id, urun, durum, ortam)
		VALUES ('apple', 'm003', 'plan.monthly', 'aktif', 'Production')`); err == nil {
		t.Error("unknown subscription product must be rejected")
	}
	ins := `INSERT INTO kredi.kampanya (ad, kota, kisi_basi, gecerlilik_gun, durum, baslangic, olusturan)
		VALUES ('k', 10, 24, 30, 'aktif', now(), 'a@b.c')`
	if _, err := pool.Exec(ctx, ins); err != nil {
		t.Fatalf("first active campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, ins); err == nil {
		t.Error("a second active campaign must be rejected")
	}
}
