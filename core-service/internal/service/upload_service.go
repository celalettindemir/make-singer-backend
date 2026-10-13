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
	UploadVocal(ctx context.Context, userID, projectID, sectionID, takeName string, file io.Reader, fileSize int64) (*model.UploadVocalResponse, error)
	DeleteVocal(ctx context.Context, userID, projectID, sectionID, takeID string) error
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
func (s *UploadService) UploadVocal(ctx context.Context, userID, projectID, sectionID, takeName string, file io.Reader, fileSize int64) (*model.UploadVocalResponse, error) {
	takeID := uuid.New().String()

	// The key is prefixed with the user; client ids go through segment
	// validation. The mock path does the same validation.
	key, err := client.UserKey("vocals", userID, projectID, sectionID, takeID+".wav")
	if err != nil {
		return nil, err
	}

	// Use mock response if client is not configured
	if s.r2Client == nil {
		return s.uploadMock(takeID, key)
	}

	// Upload to R2. Upload also returns the URL; calling URLFor separately
	// would have meant a second (wasted) signing.
	fileURL, expiresAt, err := s.r2Client.Upload(ctx, key, file, "audio/wav")
	if err != nil {
		return nil, fmt.Errorf("could not upload the vocal: %w", err)
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

// DeleteVocal deletes the vocal by its deterministic key. The key used to be
// the literal glob "vocals/*/<id>.wav", which matched no object.
func (s *UploadService) DeleteVocal(ctx context.Context, userID, projectID, sectionID, takeID string) error {
	key, err := client.UserKey("vocals", userID, projectID, sectionID, takeID+".wav")
	if err != nil {
		return err
	}
	if s.r2Client == nil {
		return nil // Mock: no-op
	}
	return s.r2Client.Delete(ctx, key)
}

// GetSignedURL generates a presigned URL for temporary access to a file
func (s *UploadService) GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if s.r2Client == nil {
		// Mock: the URL is produced with UnsignedURL so that KeyFromURL can
		// resolve it back to a key on a later request (e.g. in a master job).
		// The fixed cdn.makeasinger.com is an unknown host.
		return client.UnsignedURL(key, s.r2Cfg), nil
	}

	return s.r2Client.GetSignedURL(ctx, key, expiry)
}

// Mock implementation for development/testing
func (s *UploadService) uploadMock(takeID, key string) (*model.UploadVocalResponse, error) {
	return &model.UploadVocalResponse{
		ID: takeID,
		// UnsignedURL is used so that the mock output can also be resolved with
		// KeyFromURL (see the export/master/render mocks).
		FileURL:    client.UnsignedURL(key, s.r2Cfg),
		Duration:   32.5,
		SampleRate: 44100,
		Channels:   1,
		CreatedAt:  time.Now(),
	}, nil
}
