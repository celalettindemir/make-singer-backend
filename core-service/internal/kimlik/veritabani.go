package kimlik

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Baglan, Postgres havuzunu kurar ve baglantinin gercekten kuruldugunu
// dogrular. DSN bos ise hata doner: kimlik saglayicisi veritabani olmadan
// calisamaz, sessizce devam etmek kullanici hesaplarini kaybettirir.
func Baglan(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("auth veritabani DSN'i bos")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN cozulemedi: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour

	havuz, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("havuz kurulamadi: %w", err)
	}
	pingCtx, iptal := context.WithTimeout(ctx, 10*time.Second)
	defer iptal()
	if err := havuz.Ping(pingCtx); err != nil {
		havuz.Close()
		return nil, fmt.Errorf("veritabanina ulasilamadi: %w", err)
	}
	return havuz, nil
}

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

// Migrate, uygulanmamis migration'lari sirayla calistirir. Her migration
// kendi islemi icinde kosar ve schema_migrations'a yazilir; yarida kalan
// bir migration kismi sema birakmaz.
func Migrate(ctx context.Context, havuz *pgxpool.Pool) error {
	_, err := havuz.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("schema_migrations olusturulamadi: %w", err)
	}

	adlar, err := migrationAdlari()
	if err != nil {
		return err
	}
	for _, ad := range adlar {
		var varMi bool
		err := havuz.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, ad,
		).Scan(&varMi)
		if err != nil {
			return fmt.Errorf("%s kontrol edilemedi: %w", ad, err)
		}
		if varMi {
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + ad)
		if err != nil {
			return fmt.Errorf("%s okunamadi: %w", ad, err)
		}
		islem, err := havuz.Begin(ctx)
		if err != nil {
			return fmt.Errorf("%s icin islem baslatilamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx, string(sql)); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s uygulanamadi: %w", ad, err)
		}
		if _, err := islem.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, ad); err != nil {
			_ = islem.Rollback(ctx)
			return fmt.Errorf("%s kaydedilemedi: %w", ad, err)
		}
		if err := islem.Commit(ctx); err != nil {
			return fmt.Errorf("%s commit edilemedi: %w", ad, err)
		}
	}
	return nil
}
