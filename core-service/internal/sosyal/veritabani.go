// Package sosyal, Kesfet'in (yayin, profil, begeni, akis) veri katmanini ve
// is kurallarini tasir. Kendi Postgres semasinda (sosyal) ve kendi
// migration tablosunda calisir; kimlik modulunun havuzunu paylasmaz.
package sosyal

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

// migrationKilitAnahtari, sosyal migration'larini kumede tek pod'un
// kosmasini saglayan advisory lock anahtaridir. Kimligin anahtarindan
// (0x6B696D6C696B31, "kimlik1") farklidir; iki modul birbirini beklemez.
const migrationKilitAnahtari int64 = 0x736F7379616C31 // "sosyal1"

func migrationAdlari() ([]string, error) {
	girdiler, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var adlar []string
	for _, g := range girdiler {
		if !g.IsDir() {
			adlar = append(adlar, g.Name())
		}
	}
	sort.Strings(adlar)
	return adlar, nil
}

// Migrate, sosyal semasini ve uygulanmamis migration'lari kurar. Butun is
// tek bir baglanti uzerinde, advisory lock altinda kosar: rolling update
// sirasinda iki pod ayni anda baslarsa ikincisi birincinin bitmesini
// bekler. Her migration kendi islemindedir; yarida kalan sema birakmaz.
func Migrate(ctx context.Context, havuz *pgxpool.Pool) error {
	baglanti, err := havuz.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("sosyal migration icin baglanti alinamadi: %w", err)
	}
	defer baglanti.Release()

	if _, err := baglanti.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationKilitAnahtari); err != nil {
		return fmt.Errorf("sosyal migration kilidi alinamadi: %w", err)
	}
	defer func() {
		if _, err := baglanti.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationKilitAnahtari); err != nil {
			log.Printf("sosyal migration kilidi birakilamadi: %v", err)
			// Oturum kilidi baglantiyla yasar: baglanti kapatilir ki havuz
			// onu dusurup kilit serbest kalsin (Release kapali baglantiyi atar).
			if cerr := baglanti.Conn().Close(context.Background()); cerr != nil {
				log.Printf("sosyal migration baglantisi kapatilamadi: %v", cerr)
			}
		}
	}()

	if _, err := baglanti.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS sosyal`); err != nil {
		return fmt.Errorf("sosyal semasi olusturulamadi: %w", err)
	}
	if _, err := baglanti.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sosyal.schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("sosyal.schema_migrations olusturulamadi: %w", err)
	}

	adlar, err := migrationAdlari()
	if err != nil {
		return err
	}
	for _, ad := range adlar {
		var varMi bool
		if err := baglanti.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM sosyal.schema_migrations WHERE version = $1)`, ad,
		).Scan(&varMi); err != nil {
			return fmt.Errorf("%s kontrol edilemedi: %w", ad, err)
		}
		if varMi {
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + ad)
		if err != nil {
			return fmt.Errorf("%s okunamadi: %w", ad, err)
		}
		islem, err := baglanti.Begin(ctx)
		if err != nil {
			return fmt.Errorf("%s icin islem baslatilamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx, string(sql)); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s uygulanamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx,
			`INSERT INTO sosyal.schema_migrations (version) VALUES ($1)`, ad); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s kaydedilemedi: %w", ad, err)
		}
		if err := islem.Commit(ctx); err != nil {
			return fmt.Errorf("%s commit edilemedi: %w", ad, err)
		}
	}
	return nil
}
