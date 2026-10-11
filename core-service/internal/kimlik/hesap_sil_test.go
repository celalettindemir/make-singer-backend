package kimlik

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Kullanici silinince federe eslesmeler ve refresh kayitlari FK
// ON DELETE CASCADE ile gitmeli (canli Postgres; yoksa atlanir).
func TestHesapSilCascade(t *testing.T) {
	kullanicilar := testPostgresUserStore(t)
	havuz := kullanicilar.havuz
	jetonlar := NewPostgresTokenStore(havuz, nil)
	federe := NewPostgresFedereDepo(havuz)
	ctx := context.Background()

	k, err := kullanicilar.Create(ctx, "sil-"+uuid.NewString()+"@ornek.com", "Ad", "uzun-sifre-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = havuz.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, k.ID)
	})
	if err := federe.Bagla(ctx, "google", "sub-"+uuid.NewString(), k.ID); err != nil {
		t.Fatalf("Bagla: %v", err)
	}
	if _, err := jetonlar.RefreshOlustur(ctx, yeniRefresh(k.ID)); err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}

	if err := kullanicilar.Sil(ctx, k.ID); err != nil {
		t.Fatalf("Sil: %v", err)
	}

	var federeSayi, refreshSayi int
	_ = havuz.QueryRow(ctx, `SELECT count(*) FROM federe_kimlikler WHERE user_id = $1`, k.ID).Scan(&federeSayi)
	_ = havuz.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, k.ID).Scan(&refreshSayi)
	if federeSayi != 0 || refreshSayi != 0 {
		t.Fatalf("cascade calismadi: federe=%d refresh=%d", federeSayi, refreshSayi)
	}
	if err := kullanicilar.Sil(ctx, k.ID); !errors.Is(err, ErrKullaniciYok) {
		t.Errorf("ikinci Sil = %v, beklenen ErrKullaniciYok", err)
	}
}
