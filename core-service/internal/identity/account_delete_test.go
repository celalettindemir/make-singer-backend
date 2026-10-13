package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// When a user is deleted, the federated links and refresh records must go
// away through FK ON DELETE CASCADE (live Postgres; skipped if absent).
func TestServer_DeleteAccountCascades(t *testing.T) {
	userStore := testPostgresUserStore(t)
	pool := userStore.pool
	tokens := NewPostgresTokenStore(pool, nil)
	federated := NewPostgresFederatedStore(pool)
	ctx := context.Background()

	k, err := userStore.Create(ctx, "sil-"+uuid.NewString()+"@ornek.com", "Ad", "uzun-sifre-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, k.ID)
	})
	if err := federated.Link(ctx, "google", "sub-"+uuid.NewString(), k.ID); err != nil {
		t.Fatalf("Bagla: %v", err)
	}
	if _, err := tokens.CreateRefresh(ctx, newRefresh(k.ID)); err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}

	if err := userStore.Delete(ctx, k.ID); err != nil {
		t.Fatalf("Sil: %v", err)
	}

	var federatedCount, refreshCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM federe_kimlikler WHERE user_id = $1`, k.ID).Scan(&federatedCount)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, k.ID).Scan(&refreshCount)
	if federatedCount != 0 || refreshCount != 0 {
		t.Fatalf("cascade calismadi: federe=%d refresh=%d", federatedCount, refreshCount)
	}
	if err := userStore.Delete(ctx, k.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("ikinci Sil = %v, beklenen ErrKullaniciYok", err)
	}
}
