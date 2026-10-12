package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// publicPrefix: only objects under this prefix go to the public bucket. If the
// list grows, this is the single point of change (the same rule lives in
// Python's bucket_for).
const publicPrefix = "exports/"

// IsPublicKey reports whether the key belongs to the public bucket.
func IsPublicKey(key string) bool {
	return strings.HasPrefix(key, publicPrefix)
}

// BucketFor returns the name of the bucket the key is written to or read from.
// An unknown prefix falls to PRIVATE: a file on the wrong side should cause
// only unreachability, not a leak.
func BucketFor(key string, cfg *config.R2Config) string {
	if IsPublicKey(key) {
		return cfg.PublicBucket
	}
	return cfg.PrivateBucket
}

// r2Host is the common domain suffix of the R2 S3 API.
const r2Host = "r2.cloudflarestorage.com"

// knownHosts produces the FULL list of hosts KeyFromURL accepts. Comparison is
// always by exact equality: there is no suffix/substring matching, because a
// host like "evil-makeasinger-private.example.com" could be accidentally
// accepted by a suffix match.
//
// Accepted forms:
//   - <R2_PUBLIC_URL> host (CDN)
//   - <bucket>.<accountID>.r2.cloudflarestorage.com  (the real form the AWS SDK
//     presigner produces through the account endpoint)
//   - <bucket>.r2.cloudflarestorage.com              (plain virtual-host)
func knownHosts(cfg *config.R2Config) []string {
	if cfg == nil {
		return nil
	}

	hosts := make([]string, 0, 5)
	if cfg.PublicURL != "" {
		if pu, err := url.Parse(cfg.PublicURL); err == nil && pu.Host != "" {
			hosts = append(hosts, pu.Host)
		}
	}
	for _, b := range []string{cfg.PublicBucket, cfg.PrivateBucket} {
		if b == "" {
			continue
		}
		if cfg.AccountID != "" {
			hosts = append(hosts, fmt.Sprintf("%s.%s.%s", b, cfg.AccountID, r2Host))
		}
		hosts = append(hosts, fmt.Sprintf("%s.%s", b, r2Host))
	}
	return hosts
}

// UnsignedURL produces an unsigned URL for a key that KeyFromURL can still
// resolve back. It is only for mock/dev paths: real paths use R2Client.URLFor.
func UnsignedURL(key string, cfg *config.R2Config) string {
	if cfg != nil && IsPublicKey(key) && cfg.PublicURL != "" {
		return fmt.Sprintf("%s/%s", cfg.PublicURL, key)
	}

	bucket := "makeasinger-private"
	if cfg != nil {
		if b := BucketFor(key, cfg); b != "" {
			bucket = b
		}
	}
	if cfg != nil && cfg.AccountID != "" {
		return fmt.Sprintf("https://%s.%s.%s/%s", bucket, cfg.AccountID, r2Host, key)
	}
	return fmt.Sprintf("https://%s.%s/%s", bucket, r2Host, key)
}

// ResolveURL produces the URL to give the client from an object key. If a
// storage client exists, the real URL is produced (public: permanent CDN,
// private: freshly signed URL). Without a client (mock/dev) it returns an
// unsigned URL that resolves under the same rules.
func ResolveURL(ctx context.Context, sc StorageClient, cfg *config.R2Config, key string) (string, time.Time, error) {
	if sc != nil {
		return sc.URLFor(ctx, key)
	}
	return UnsignedURL(key, cfg), time.Time{}, nil
}

// KeyFromURL extracts the object key from a URL coming from a client.
// Presigned forms (with a query string) are accepted too; the query string is
// ignored. An unknown host returns an error; nothing is guessed.
//
// The FULL URL never appears in error messages: these messages land in the
// Redis job record and the WebSocket broadcast, and a signed URL is a secret.
func KeyFromURL(rawURL string, cfg *config.R2Config) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("%w: bos URL", ErrInvalidURL)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("%w: URL cozulemedi", ErrInvalidURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: URL'de konak yok", ErrInvalidURL)
	}

	known := false
	for _, k := range knownHosts(cfg) {
		if u.Host == k {
			known = true
			break
		}
	}
	if !known {
		return "", fmt.Errorf("%w: taninmayan konak: %s", ErrInvalidURL, u.Host)
	}

	key := strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return "", fmt.Errorf("%w: URL'de nesne anahtari yok (konak: %s)", ErrInvalidURL, u.Host)
	}
	return key, nil
}
