package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

func testR2Cfg() *config.R2Config {
	return &config.R2Config{
		AccountID:       "hesap123",
		AccessKeyID:     "anahtar",
		SecretAccessKey: "gizli",
		PublicBucket:    "makeasinger-public",
		PrivateBucket:   "makeasinger-private",
		PublicURL:       "https://makesinger-cdn.celalettindemir.dev",
		PresignTTL:      time.Hour,
	}
}

func TestURLFor_PublicAnahtarKaliciCDNAdresi(t *testing.T) {
	c, err := NewR2Client(testR2Cfg())
	if err != nil {
		t.Fatalf("NewR2Client: %v", err)
	}

	url, expires, err := c.URLFor(context.Background(), "exports/abc.mp3")
	if err != nil {
		t.Fatalf("URLFor: %v", err)
	}
	if url != "https://makesinger-cdn.celalettindemir.dev/exports/abc.mp3" {
		t.Errorf("public URL = %q", url)
	}
	if !expires.IsZero() {
		t.Errorf("public anahtar icin expires sifir olmali, geldi: %v", expires)
	}
}

func TestURLFor_PrivateAnahtarPresignedVeBirSaat(t *testing.T) {
	c, err := NewR2Client(testR2Cfg())
	if err != nil {
		t.Fatalf("NewR2Client: %v", err)
	}

	once := time.Now()
	url, expires, err := c.URLFor(context.Background(), "vocals/p1/s1/t1.wav")
	if err != nil {
		t.Fatalf("URLFor: %v", err)
	}
	if !strings.Contains(url, "X-Amz-Signature") {
		t.Errorf("presigned URL bekleniyordu: %q", url)
	}
	if !strings.Contains(url, "makeasinger-private") {
		t.Errorf("private bucket bekleniyordu: %q", url)
	}
	fark := expires.Sub(once)
	if fark < 59*time.Minute || fark > 61*time.Minute {
		t.Errorf("expires ~1 saat olmali, fark: %v", fark)
	}
}

func TestNewR2Client_EksikYapilandirma(t *testing.T) {
	cfg := testR2Cfg()
	cfg.AccessKeyID = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("eksik yapilandirma icin hata bekleniyordu")
	}
}

func TestNewR2Client_BucketAdlariZorunlu(t *testing.T) {
	cfg := testR2Cfg()
	cfg.PrivateBucket = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("private bucket adi bos iken hata bekleniyordu")
	}
}
