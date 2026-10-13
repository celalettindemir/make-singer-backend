package identity

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// testPostgres wants a local Postgres. If there is none the test is
// skipped: unit tests have to pass without a network (see testRedis,
// request_test.go). The DSN comes from the environment variable; the
// default value is for a throwaway, TEST-LOCAL container (it is NOT a
// production secret, and no production secret is ever written into source).
func testPostgres(t *testing.T) *PostgresTokenStore {
	t.Helper()
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Skip("yerel Postgres yok, atlaniyor")
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	// rdb is nil: these tests only measure the refresh path, the access
	// records (Redis) are never used.
	return NewPostgresTokenStore(pool, nil)
}

// pgTestUser opens a real user row because refresh_tokens.user_id has an
// FK to users, and deletes it at the end of the test (refresh records go
// via ON DELETE CASCADE, so tests do not pollute each other).
func pgTestUser(t *testing.T, s *PostgresTokenStore) string {
	t.Helper()
	id := uuid.NewString()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		id, id+"@test.invalid", "test-yerel-hash"); err != nil {
		t.Fatalf("test kullanicisi eklenemedi: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// LIVE POSTGRES test for the finding (Critical). The Postgres branch of
// this code path had never been tested until now; that is why the
// finding surfaced late. What is measured: on a real database, rotation
// -> presenting the old token again through the method the LIBRARY calls
// (TokenRequestByRefreshToken) -> the WHOLE family gets revoked_at.
func TestPostgresTokenStore_ReuseRevokesFamily(t *testing.T) {
	s := testPostgres(t)
	ctx := context.Background()
	userID := pgTestUser(t, s)

	key, err := LoadSigningKey(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	d := NewStorage(testAuthCfg(), NewFakeUserStore(), s, nil, key)

	record := newRefresh(userID)
	old, err := s.CreateRefresh(ctx, record)
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	fresh, err := s.RotateRefresh(ctx, old, newRefresh(userID))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	if _, err := s.GetRefresh(ctx, fresh); err != nil {
		t.Fatalf("rotasyon sonrasi yeni jeton gecersiz: %v", err)
	}

	// The used token must be rejected with the error the library sees...
	if _, err := d.TokenRequestByRefreshToken(ctx, old); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
	// ...and the WHOLE family must be dead. Verify with SQL: how many rows
	// are in the same family and how many are still alive.
	familyID, err := s.RefreshFamilyID(ctx, old)
	if err != nil {
		t.Fatalf("RefreshAileID: %v", err)
	}
	var total, alive int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE revoked_at IS NULL)
		   FROM refresh_tokens WHERE family_id = $1`, familyID).
		Scan(&total, &alive); err != nil {
		t.Fatalf("aile sorgulanamadi: %v", err)
	}
	if total != 2 {
		t.Errorf("ailede %d satir var, beklenen 2", total)
	}
	if alive != 0 {
		t.Errorf("ailede %d satir hala iptal edilmemis, beklenen 0", alive)
	}
	// The current token in the legitimate client's hands must not work any more either.
	if _, err := d.TokenRequestByRefreshToken(ctx, fresh); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("guncel jeton hala kabul ediliyor: %v", err)
	}
}

// The Postgres branch's GetRefresh order must be EXACTLY the same as the
// fake store's: used -> ErrTokenReused, revoked -> ErrTokenNotFound.
func TestPostgresTokenStore_GetRefreshErrorOrder(t *testing.T) {
	s := testPostgres(t)
	ctx := context.Background()
	userID := pgTestUser(t, s)

	old, err := s.CreateRefresh(ctx, newRefresh(userID))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := s.RotateRefresh(ctx, old, newRefresh(userID)); err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	// used_at set, revoked_at empty: reuse.
	if _, err := s.GetRefresh(ctx, old); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("hata = %v, beklenen ErrJetonTekrar", err)
	}
	// After the family is revoked the same token is now ErrTokenNotFound: the
	// alarm does not ring again.
	familyID, err := s.RefreshFamilyID(ctx, old)
	if err != nil {
		t.Fatalf("RefreshAileID: %v", err)
	}
	if err := s.RevokeFamily(ctx, familyID); err != nil {
		t.Fatalf("AileIptal: %v", err)
	}
	if _, err := s.GetRefresh(ctx, old); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
	// Unknown token: neither a record nor a family.
	if _, err := s.GetRefresh(ctx, "uydurma"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
	if _, err := s.RefreshFamilyID(ctx, "uydurma"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}
