package kimlik

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// testPostgres, yerel Postgres ister. Yoksa test atlanir: birim testler
// agsiz gecmek zorunda (bkz. testRedis, istek_test.go). DSN ortam
// degiskeninden alinir; varsayilan deger tek kullanimlik, TEST-YEREL bir
// konteyner icindir (uretim sirri DEGILDIR ve kaynak koda uretim sirri
// yazilmaz).
func testPostgres(t *testing.T) *PostgresTokenStore {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, iptal := context.WithTimeout(context.Background(), 3*time.Second)
	defer iptal()
	havuz, err := Baglan(ctx, dsn)
	if err != nil {
		t.Skip("yerel Postgres yok, atlaniyor")
	}
	if err := Migrate(ctx, havuz); err != nil {
		havuz.Close()
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(havuz.Close)
	// rdb nil: bu testler yalnizca refresh yolunu olcer, access kayitlari
	// (Redis) hic kullanilmaz.
	return NewPostgresTokenStore(havuz, nil)
}

// pgTestKullanici, refresh_tokens.user_id'nin users'a FK'si oldugu icin
// gercek bir kullanici satiri acar ve testin sonunda siler (refresh
// kayitlari ON DELETE CASCADE ile gider, testler birbirini kirletmez).
func pgTestKullanici(t *testing.T, s *PostgresTokenStore) string {
	t.Helper()
	id := uuid.NewString()
	ctx := context.Background()
	if _, err := s.havuz.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		id, id+"@test.invalid", "test-yerel-hash"); err != nil {
		t.Fatalf("test kullanicisi eklenemedi: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.havuz.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// BULGU (Critical) icin CANLI POSTGRES testi. Bu kod yolunun Postgres
// kolu bugune kadar hic test edilmedi; bulgunun gec ortaya cikmasinin
// nedeni buydu. Olculen: gercek bir veritabaninda rotasyon -> eski
// jetonu KUTUPHANENIN CAGIRDIGI metotla (TokenRequestByRefreshToken)
// yeniden sunma -> ailenin TAMAMININ revoked_at almasi.
func TestPGYenidenKullanimAileyiIptalEder(t *testing.T) {
	s := testPostgres(t)
	ctx := context.Background()
	userID := pgTestKullanici(t, s)

	anahtar, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	d := NewDepo(testAuthCfg(), NewSahteUserStore(), s, nil, anahtar)

	kayit := yeniRefresh(userID)
	eski, err := s.RefreshOlustur(ctx, kayit)
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	yeni, err := s.RefreshDondur(ctx, eski, yeniRefresh(userID))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	if _, err := s.RefreshOku(ctx, yeni); err != nil {
		t.Fatalf("rotasyon sonrasi yeni jeton gecersiz: %v", err)
	}

	// Kullanilmis jeton kutuphanenin gordugu hatayla reddedilmeli...
	if _, err := d.TokenRequestByRefreshToken(ctx, eski); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
	// ...ve ailenin TAMAMI olmus olmali. SQL ile dogrula: ayni aileden
	// kac satir var ve kaci hala yasiyor.
	familyID, err := s.RefreshAileID(ctx, eski)
	if err != nil {
		t.Fatalf("RefreshAileID: %v", err)
	}
	var toplam, yasayan int
	if err := s.havuz.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE revoked_at IS NULL)
		   FROM refresh_tokens WHERE family_id = $1`, familyID).
		Scan(&toplam, &yasayan); err != nil {
		t.Fatalf("aile sorgulanamadi: %v", err)
	}
	if toplam != 2 {
		t.Errorf("ailede %d satir var, beklenen 2", toplam)
	}
	if yasayan != 0 {
		t.Errorf("ailede %d satir hala iptal edilmemis, beklenen 0", yasayan)
	}
	// Mesru istemcinin elindeki guncel jeton da artik calismamali.
	if _, err := d.TokenRequestByRefreshToken(ctx, yeni); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("guncel jeton hala kabul ediliyor: %v", err)
	}
}

// Postgres kolunun RefreshOku sirasi sahte depoyla BIREBIR ayni olmali:
// kullanilmis -> ErrJetonTekrar, iptal edilmis -> ErrJetonYok.
func TestPGRefreshOkuHataSirasi(t *testing.T) {
	s := testPostgres(t)
	ctx := context.Background()
	userID := pgTestKullanici(t, s)

	eski, err := s.RefreshOlustur(ctx, yeniRefresh(userID))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := s.RefreshDondur(ctx, eski, yeniRefresh(userID)); err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	// used_at dolu, revoked_at bos: yeniden kullanim.
	if _, err := s.RefreshOku(ctx, eski); !errors.Is(err, ErrJetonTekrar) {
		t.Fatalf("hata = %v, beklenen ErrJetonTekrar", err)
	}
	// Aile iptal edildikten sonra ayni jeton artik ErrJetonYok: alarm
	// tekrar calmaz.
	familyID, err := s.RefreshAileID(ctx, eski)
	if err != nil {
		t.Fatalf("RefreshAileID: %v", err)
	}
	if err := s.AileIptal(ctx, familyID); err != nil {
		t.Fatalf("AileIptal: %v", err)
	}
	if _, err := s.RefreshOku(ctx, eski); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
	// Bilinmeyen jeton: ne kayit ne aile.
	if _, err := s.RefreshOku(ctx, "uydurma"); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
	if _, err := s.RefreshAileID(ctx, "uydurma"); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}
