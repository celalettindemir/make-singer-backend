package service

import (
	"context"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// TestMasterService_Preview_ExpiresAtConfigTTL, MasterPreviewResponse.ExpiresAt
// degerinin r2Cfg.PresignTTL'den geldigini dogrular. r2Cfg.PresignTTL burada
// bilinen 1 saatlik varsayilandan FARKLI (2 saat) verilir: eger kod tekrar
// sabit "1 saat" degerine donerse bu test kirilir.
func TestMasterService_Preview_ExpiresAtConfigTTL(t *testing.T) {
	r2Cfg := &config.R2Config{PresignTTL: 2 * time.Hour}
	s := NewMasterService(nil, nil, nil, r2Cfg)

	before := time.Now()
	resp, err := s.Preview(context.Background(), &model.MasterPreviewRequest{})
	if err != nil {
		t.Fatalf("Preview hata verdi: %v", err)
	}

	if resp.ExpiresAt == nil {
		t.Fatal("ExpiresAt nil, sureli bir link icin dolu olmali")
	}

	fark := resp.ExpiresAt.Sub(before)
	// 1 saatlik sabit degere donulmusse fark ~1h olur; config'teki 2h'e
	// yakin olmasi gerekir (bir miktar tolerans ile).
	if fark < 90*time.Minute || fark > 130*time.Minute {
		t.Errorf("ExpiresAt farki = %v, config'teki 2h PresignTTL'e yakin olmali (sabit 1h kullanilmis olabilir)", fark)
	}
}
