package service

import (
	"context"
	"fmt"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// FileService, istemcinin sakladigi dosya adreslerini tazeler. Istemci
// adresi yalnizca nesne referansi olarak tutar; imza 1 saatte doldugu
// icin calmadan once buradan yeni adres alir.
type FileService struct {
	storage client.StorageClient
	r2Cfg   *config.R2Config
}

func NewFileService(storage client.StorageClient, r2Cfg *config.R2Config) *FileService {
	return &FileService{storage: storage, r2Cfg: r2Cfg}
}

// Sign, adresleri girdiyle ayni sirada tazeler. Once HEPSI cozulup
// sahiplik denetlenir: tek bir yabanci anahtar varsa hicbir imza
// uretilmez (kismi yanit, yabanci anahtarin varligini sizdirmaz).
func (s *FileService) Sign(ctx context.Context, userID string, urls []string) (*model.FileSignResponse, error) {
	anahtarlar := make([]string, len(urls))
	for i, u := range urls {
		key, err := client.SahipliAnahtarCoz(u, userID, s.r2Cfg)
		if err != nil {
			return nil, err
		}
		anahtarlar[i] = key
	}
	dosyalar := make([]model.SignedFile, len(anahtarlar))
	for i, key := range anahtarlar {
		adres, son, err := client.ResolveURL(ctx, s.storage, s.r2Cfg, key)
		if err != nil {
			return nil, fmt.Errorf("adres uretilemedi: %w", err)
		}
		dosyalar[i] = model.SignedFile{URL: adres, ExpiresAt: model.ExpiresPtr(son)}
	}
	return &model.FileSignResponse{Files: dosyalar}, nil
}
