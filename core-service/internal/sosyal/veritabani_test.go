package sosyal

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makeasinger/api/internal/identity"
)

// testHavuz, yerel Postgres'e baglanir, sosyal migration'larini uygular
// ve sosyal tablolarini bosaltir. Postgres yoksa test atlanir (kimlik
// testleriyle AYNI kalip ve AYNI varsayilan DSN). TRUNCATE yalnizca
// sosyal.* tablolarina dokunur; kimlik testlerinin public tablolarini
// etkilemez.
func testHavuz(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, iptal := context.WithTimeout(context.Background(), 3*time.Second)
	defer iptal()
	havuz, err := identity.Baglan(ctx, dsn)
	if err != nil {
		t.Skip("yerel Postgres yok, atlaniyor")
	}
	t.Cleanup(havuz.Close)
	if err := Migrate(context.Background(), havuz); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := havuz.Exec(context.Background(),
		`TRUNCATE sosyal.sikayet, sosyal.yorum, sosyal.begeni, sosyal.takip, sosyal.yayin, sosyal.profil`); err != nil {
		t.Fatalf("sosyal tablolari bosaltilamadi: %v", err)
	}
	return havuz
}

func TestMigrationlarGomuluVeSirali(t *testing.T) {
	adlar, err := migrationAdlari()
	if err != nil || len(adlar) == 0 {
		t.Fatalf("migration yok: %v %v", adlar, err)
	}
	for i := 1; i < len(adlar); i++ {
		if adlar[i-1] >= adlar[i] {
			t.Errorf("migrationlar sirali degil: %q, %q", adlar[i-1], adlar[i])
		}
	}
	icerik, err := migrationFS.ReadFile("migrations/001_sosyal.sql")
	if err != nil {
		t.Fatalf("001_sosyal.sql okunamadi: %v", err)
	}
	for _, tablo := range []string{"sosyal.profil", "sosyal.yayin", "sosyal.begeni", "sosyal.takip", "sosyal.yorum", "sosyal.sikayet"} {
		if !strings.Contains(string(icerik), "CREATE TABLE IF NOT EXISTS "+tablo) {
			t.Errorf("migration %s tablosunu kurmuyor", tablo)
		}
	}
	if strings.Contains(string(icerik), "REFERENCES users") || strings.Contains(string(icerik), "public.users") {
		t.Error("sosyal tablolari public.users'a FK kurmamali")
	}
}

// Migrate iki kez kosunca da hata vermemeli (replikalar start-first doner).
func TestMigrate_IkiKezKosarVeTablolariKurar(t *testing.T) {
	havuz := testHavuz(t)
	ctx := context.Background()
	if err := Migrate(ctx, havuz); err != nil {
		t.Fatalf("ikinci Migrate: %v", err)
	}
	var sayi int
	if err := havuz.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'sosyal'
		  AND table_name IN ('profil','yayin','begeni','takip','yorum','sikayet','schema_migrations')`,
	).Scan(&sayi); err != nil {
		t.Fatalf("tablolar sayilamadi: %v", err)
	}
	if sayi != 7 {
		t.Fatalf("7 sosyal tablosu bekleniyordu, %d var", sayi)
	}
}

// yayin silinince begeni, yorum ve sikayet de gider (sema ici cascade).
func TestMigrate_YayinSilinceBagliKayitlarGider(t *testing.T) {
	havuz := testHavuz(t)
	ctx := context.Background()
	yayinID := "7a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	kisi := "8b1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	if _, err := havuz.Exec(ctx, `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, tur, bpm, ton_tonik, ton_mod, sure_sn,
			gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik, durum)
		VALUES ($1, 'Abcd1234', $2, 'b', 'pop', 120, 'A', 'minor', 60, 'herkes', false, true,
			'{}', '[]', '{}', 'yayinda')`, yayinID, kisi); err != nil {
		t.Fatalf("yayin eklenemedi: %v", err)
	}
	if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)`, kisi, yayinID); err != nil {
		t.Fatalf("begeni: %v", err)
	}
	if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), $1, $2, 'm')`, yayinID, kisi); err != nil {
		t.Fatalf("yorum: %v", err)
	}
	if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), $1, 'yayin', $2, $2, 'spam')`, kisi, yayinID); err != nil {
		t.Fatalf("sikayet: %v", err)
	}
	if _, err := havuz.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1`, yayinID); err != nil {
		t.Fatalf("yayin silinemedi: %v", err)
	}
	var kalan int
	if err := havuz.QueryRow(ctx, `SELECT (SELECT count(*) FROM sosyal.begeni) + (SELECT count(*) FROM sosyal.yorum) + (SELECT count(*) FROM sosyal.sikayet)`).Scan(&kalan); err != nil {
		t.Fatalf("sayim: %v", err)
	}
	if kalan != 0 {
		t.Fatalf("cascade calismadi, %d kayit kaldi", kalan)
	}
}
