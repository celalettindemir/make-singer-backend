package client

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/makeasinger/api/internal/config"
)

// publicOnek: yalnizca bu onekteki nesneler public bucket'a gider. Liste
// buyurse burasi tek degisim noktasi (ayni kural Python'da bucket_for).
const publicOnek = "exports/"

// IsPublicKey, anahtarin public bucket'a ait olup olmadigini soyler.
func IsPublicKey(key string) bool {
	return strings.HasPrefix(key, publicOnek)
}

// BucketFor, anahtarin yazilacagi/okunacagi bucket adini dondurur.
// Bilinmeyen onek PRIVATE'a duser: yanlis tarafa dusen bir dosya sizinti
// degil, yalnizca erisilemezlik uretsin.
func BucketFor(key string, cfg *config.R2Config) string {
	if IsPublicKey(key) {
		return cfg.PublicBucket
	}
	return cfg.PrivateBucket
}

// KeyFromURL, istemciden gelen bir URL'den nesne anahtarini cikarir.
// Kabul edilen bicimler: <R2_PUBLIC_URL>/<key>, https://<bucket>.r2.
// cloudflarestorage.com/<key> ve bunlarin presigned (sorgu dizeli) hali.
// Taninmayan konak icin hata doner; tahmin edilmez.
func KeyFromURL(rawURL string, cfg *config.R2Config) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("bos URL")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("URL cozulemedi: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL'de konak yok: %s", rawURL)
	}

	tanidik := false
	if cfg.PublicURL != "" {
		if pu, err := url.Parse(cfg.PublicURL); err == nil && pu.Host == u.Host {
			tanidik = true
		}
	}
	for _, b := range []string{cfg.PublicBucket, cfg.PrivateBucket} {
		if b != "" && u.Host == fmt.Sprintf("%s.r2.cloudflarestorage.com", b) {
			tanidik = true
		}
	}
	if !tanidik {
		return "", fmt.Errorf("taninmayan konak: %s", u.Host)
	}

	key := strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return "", fmt.Errorf("URL'de nesne anahtari yok: %s", rawURL)
	}
	return key, nil
}
