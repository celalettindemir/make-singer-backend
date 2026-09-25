package service

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// FileUploader defines the interface for file upload operations
type FileUploader interface {
	UploadVocal(ctx context.Context, projectID, sectionID, takeName string, file io.Reader, fileSize int64) (*model.UploadVocalResponse, error)
	DeleteVocal(ctx context.Context, takeID string) error
}

// UploadService handles file uploads to R2 storage
type UploadService struct {
	r2Client client.StorageClient
	r2Cfg    *config.R2Config
}

// NewUploadService creates a new upload service with R2 client
func NewUploadService(r2Client client.StorageClient, r2Cfg *config.R2Config) *UploadService {
	return &UploadService{
		r2Client: r2Client,
		r2Cfg:    r2Cfg,
	}
}

// UploadVocal uploads a vocal recording to R2 storage
func (s *UploadService) UploadVocal(ctx context.Context, projectID, sectionID, takeName string, file io.Reader, fileSize int64) (*model.UploadVocalResponse, error) {
	takeID := uuid.New().String()

	// Generate storage key
	key := fmt.Sprintf("vocals/%s/%s/%s.wav", projectID, sectionID, takeID)

	// Use mock response if client is not configured
	if s.r2Client == nil {
		return s.uploadMock(takeID, projectID)
	}

	// Upload to R2. Upload adresi de dondurur; ayrica URLFor cagirmak
	// ikinci (bosa giden) bir imzalama demekti.
	fileURL, expiresAt, err := s.r2Client.Upload(ctx, key, file, "audio/wav")
	if err != nil {
		return nil, fmt.Errorf("vokal yuklenemedi: %w", err)
	}

	return &model.UploadVocalResponse{
		ID:         takeID,
		FileURL:    fileURL,
		Duration:   0, // Would need audio analysis to get actual duration
		SampleRate: 44100,
		Channels:   1,
		CreatedAt:  time.Now(),
		ExpiresAt:  model.ExpiresPtr(expiresAt),
	}, nil
}

// DeleteVocal deletes a vocal recording from R2 storage
func (s *UploadService) DeleteVocal(ctx context.Context, takeID string) error {
	if s.r2Client == nil {
		return nil // Mock: no-op
	}

	// Note: In a real implementation, we'd need to look up the full key
	// from a database or construct it from the takeID
	key := fmt.Sprintf("vocals/*/%s.wav", takeID)

	return s.r2Client.Delete(ctx, key)
}

// DeleteVocalByKey deletes a vocal recording by its full storage key
func (s *UploadService) DeleteVocalByKey(ctx context.Context, key string) error {
	if s.r2Client == nil {
		return nil
	}

	return s.r2Client.Delete(ctx, key)
}

// GetSignedURL generates a presigned URL for temporary access to a file
func (s *UploadService) GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if s.r2Client == nil {
		// Mock: adres UnsignedURL ile uretilir ki KeyFromURL sonraki
		// istekte (ornegin master isinde) bu adresi geri anahtara
		// cozebilsin. Sabit cdn.makeasinger.com bilinmeyen bir konaktir.
		return client.UnsignedURL(key, s.r2Cfg), nil
	}

	return s.r2Client.GetSignedURL(ctx, key, expiry)
}

// Mock implementation for development/testing
func (s *UploadService) uploadMock(takeID, projectID string) (*model.UploadVocalResponse, error) {
	key := fmt.Sprintf("vocals/%s/%s.wav", projectID, takeID)
	return &model.UploadVocalResponse{
		ID: takeID,
		// UnsignedURL kullanilir: mock ciktisi da KeyFromURL ile
		// cozulebilsin diye (bkz. export/master/render mock'lari).
		FileURL:    client.UnsignedURL(key, s.r2Cfg),
		Duration:   32.5,
		SampleRate: 44100,
		Channels:   1,
		CreatedAt:  time.Now(),
	}, nil
}
