package client

import (
	"context"
	neturl "net/url"
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

func TestURLFor_PublicKeyPermanentCDNURL(t *testing.T) {
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

func TestURLFor_PrivateKeyPresignedOneHour(t *testing.T) {
	c, err := NewR2Client(testR2Cfg())
	if err != nil {
		t.Fatalf("NewR2Client: %v", err)
	}

	before := time.Now()
	url, expires, err := c.URLFor(context.Background(), "vocals/p1/s1/t1.wav")
	if err != nil {
		t.Fatalf("URLFor: %v", err)
	}
	if !strings.Contains(url, "X-Amz-Signature") {
		t.Errorf("presigned URL bekleniyordu: %q", url)
	}
	// The host is verified EXACTLY: a "contains" check matched both the
	// "bucket.r2..." and "bucket.account.r2..." forms and never tested which
	// form KeyFromURL expects.
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("uretilen URL cozulemedi: %v", err)
	}
	if u.Host != "makeasinger-private.hesap123.r2.cloudflarestorage.com" {
		t.Errorf("konak = %q, beklenen makeasinger-private.hesap123.r2.cloudflarestorage.com", u.Host)
	}
	diff := expires.Sub(before)
	if diff < 59*time.Minute || diff > 61*time.Minute {
		t.Errorf("expires ~1 saat olmali, fark: %v", diff)
	}
}

func TestNewR2Client_IncompleteConfig(t *testing.T) {
	cfg := testR2Cfg()
	cfg.AccessKeyID = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("eksik yapilandirma icin hata bekleniyordu")
	}
}

func TestNewR2Client_RequiresBucketNames(t *testing.T) {
	cfg := testR2Cfg()
	cfg.PrivateBucket = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("private bucket adi bos iken hata bekleniyordu")
	}
}

// TestNewR2Client_RequiresPublicURL: an empty R2_PUBLIC_URL produces neither a
// valid CDN URL nor a recognized host. Like the bucket names, it must fail at
// startup.
func TestNewR2Client_RequiresPublicURL(t *testing.T) {
	cfg := testR2Cfg()
	cfg.PublicURL = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("R2_PUBLIC_URL bos iken hata bekleniyordu")
	}
}
