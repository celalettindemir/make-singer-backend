package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

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

// r2Konak, R2 S3 API'sinin ortak alan adi sonekidir.
const r2Konak = "r2.cloudflarestorage.com"

// bilinenKonaklar, KeyFromURL'in kabul ettigi konaklarin TAM listesini
// uretir. Karsilastirma her zaman tam esitlikle yapilir: sonek/icerik
// eslesmesi yoktur, cunku "kotu-makeasinger-private.example.com" gibi bir
// konak sonek eslesmesinde kazara tanidik sayilabilirdi.
//
// Kabul edilen bicimler:
//   - <R2_PUBLIC_URL> konagi (CDN)
//   - <bucket>.<accountID>.r2.cloudflarestorage.com  (AWS SDK presigner'in
//     hesap uc noktasi uzerinden urettigi gercek bicim)
//   - <bucket>.r2.cloudflarestorage.com              (sade virtual-host)
func bilinenKonaklar(cfg *config.R2Config) []string {
	if cfg == nil {
		return nil
	}

	konaklar := make([]string, 0, 5)
	if cfg.PublicURL != "" {
		if pu, err := url.Parse(cfg.PublicURL); err == nil && pu.Host != "" {
			konaklar = append(konaklar, pu.Host)
		}
	}
	for _, b := range []string{cfg.PublicBucket, cfg.PrivateBucket} {
		if b == "" {
			continue
		}
		if cfg.AccountID != "" {
			konaklar = append(konaklar, fmt.Sprintf("%s.%s.%s", b, cfg.AccountID, r2Konak))
		}
		konaklar = append(konaklar, fmt.Sprintf("%s.%s", b, r2Konak))
	}
	return konaklar
}

// UnsignedURL, bir anahtar icin imzasiz ama KeyFromURL ile geri
// cozulebilen adres uretir. Yalnizca mock/dev yollari icindir: gercek
// yollar R2Client.URLFor kullanir.
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
		return fmt.Sprintf("https://%s.%s.%s/%s", bucket, cfg.AccountID, r2Konak, key)
	}
	return fmt.Sprintf("https://%s.%s/%s", bucket, r2Konak, key)
}

// ResolveURL, bir nesne anahtarindan istemciye verilecek adresi uretir.
// Depolama istemcisi varsa gercek adres (public: kalici CDN, private:
// taze imzali URL) uretilir. Istemci yoksa (mock/dev) imzasiz ama ayni
// kurallarla cozulebilen bir adres dondurulur.
func ResolveURL(ctx context.Context, sc StorageClient, cfg *config.R2Config, key string) (string, time.Time, error) {
	if sc != nil {
		return sc.URLFor(ctx, key)
	}
	return UnsignedURL(key, cfg), time.Time{}, nil
}

// KeyFromURL, istemciden gelen bir URL'den nesne anahtarini cikarir.
// Presigned (sorgu dizeli) haller de kabul edilir; sorgu dizesi yok sayilir.
// Taninmayan konak icin hata doner; tahmin edilmez.
//
// Hata mesajlarinda TAM URL asla gecmez: bu mesajlar Redis is kaydina ve
// WebSocket yayinina dusuyor, imzali URL de bir sirdir.
func KeyFromURL(rawURL string, cfg *config.R2Config) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("bos URL")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("URL cozulemedi")
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL'de konak yok")
	}

	tanidik := false
	for _, k := range bilinenKonaklar(cfg) {
		if u.Host == k {
			tanidik = true
			break
		}
	}
	if !tanidik {
		return "", fmt.Errorf("taninmayan konak: %s", u.Host)
	}

	key := strings.TrimPrefix(u.Path, "/")
	if key == "" {
		return "", fmt.Errorf("URL'de nesne anahtari yok (konak: %s)", u.Host)
	}
	return key, nil
}
