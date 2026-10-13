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
		t.Errorf("expires must be zero for a public key, got: %v", expires)
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
		t.Errorf("expected a presigned URL: %q", url)
	}
	// The host is verified EXACTLY: a "contains" check matched both the
	// "bucket.r2..." and "bucket.account.r2..." forms and never tested which
	// form KeyFromURL expects.
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatalf("could not parse the generated URL: %v", err)
	}
	if u.Host != "makeasinger-private.hesap123.r2.cloudflarestorage.com" {
		t.Errorf("host = %q, want makeasinger-private.hesap123.r2.cloudflarestorage.com", u.Host)
	}
	diff := expires.Sub(before)
	if diff < 59*time.Minute || diff > 61*time.Minute {
		t.Errorf("expires should be ~1 hour, diff: %v", diff)
	}
}

func TestNewR2Client_IncompleteConfig(t *testing.T) {
	cfg := testR2Cfg()
	cfg.AccessKeyID = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("expected an error for incomplete configuration")
	}
}

func TestNewR2Client_RequiresBucketNames(t *testing.T) {
	cfg := testR2Cfg()
	cfg.PrivateBucket = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("expected an error when the private bucket name is empty")
	}
}

// TestNewR2Client_RequiresPublicURL: an empty R2_PUBLIC_URL produces neither a
// valid CDN URL nor a recognized host. Like the bucket names, it must fail at
// startup.
func TestNewR2Client_RequiresPublicURL(t *testing.T) {
	cfg := testR2Cfg()
	cfg.PublicURL = ""
	if _, err := NewR2Client(cfg); err == nil {
		t.Error("expected an error when R2_PUBLIC_URL is empty")
	}
}
