package service

import (
	"context"
	"fmt"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// FileService refreshes the file URLs the client has stored. The client keeps
// a URL only as an object reference; since the signature expires in 1 hour it
// gets a fresh URL from here before playing.
type FileService struct {
	storage client.StorageClient
	r2Cfg   *config.R2Config
}

func NewFileService(storage client.StorageClient, r2Cfg *config.R2Config) *FileService {
	return &FileService{storage: storage, r2Cfg: r2Cfg}
}

// Sign refreshes the URLs in the same order as the input. ALL of them are
// resolved and ownership-checked first: if there is a single foreign key no
// signature is produced (a partial response would leak the existence of the
// foreign key).
func (s *FileService) Sign(ctx context.Context, userID string, urls []string) (*model.FileSignResponse, error) {
	keys := make([]string, len(urls))
	for i, u := range urls {
		key, err := client.ResolveOwnedKey(u, userID, s.r2Cfg)
		if err != nil {
			return nil, err
		}
		keys[i] = key
	}
	files := make([]model.SignedFile, len(keys))
	for i, key := range keys {
		signedURL, expiresAt, err := client.ResolveURL(ctx, s.storage, s.r2Cfg, key)
		if err != nil {
			return nil, fmt.Errorf("adres uretilemedi: %w", err)
		}
		files[i] = model.SignedFile{URL: signedURL, ExpiresAt: model.ExpiresPtr(expiresAt)}
	}
	return &model.FileSignResponse{Files: files}, nil
}
