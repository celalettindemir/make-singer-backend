package service

import (
	"context"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// TestMasterService_Preview_ExpiresAtConfigTTL verifies that
// MasterPreviewResponse.ExpiresAt comes from r2Cfg.PresignTTL. r2Cfg.PresignTTL
// is given a value (2 hours) DIFFERENT from the known 1-hour default: if the
// code reverts to the fixed "1 hour" value this test breaks.
func TestMasterService_Preview_ExpiresAtConfigTTL(t *testing.T) {
	r2Cfg := &config.R2Config{PresignTTL: 2 * time.Hour}
	s := NewMasterService(nil, nil, nil, r2Cfg)

	before := time.Now()
	resp, err := s.Preview(context.Background(), "kullanici-a", &model.MasterPreviewRequest{})
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}

	if resp.ExpiresAt == nil {
		t.Fatal("ExpiresAt is nil, it must be set for a timed link")
	}

	diff := resp.ExpiresAt.Sub(before)
	// If it reverted to the fixed 1-hour value the difference would be ~1h; it
	// must be close to the 2h in the config (with some tolerance).
	if diff < 90*time.Minute || diff > 130*time.Minute {
		t.Errorf("ExpiresAt difference = %v, it must be close to the 2h PresignTTL in the config (a fixed 1h may have been used)", diff)
	}
}
