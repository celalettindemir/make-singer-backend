package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// FileExporter defines the interface for file export operations
type FileExporter interface {
	ExportMP3(ctx context.Context, req *model.ExportMP3Request) (*model.ExportMP3Response, error)
	ExportWAV(ctx context.Context, req *model.ExportWAVRequest) (*model.ExportWAVResponse, error)
	ExportStems(ctx context.Context, req *model.ExportStemsRequest) (*model.ExportStemsResponse, error)
}

// ExportService handles file exports using the audio processing service
type ExportService struct {
	r2Client    client.StorageClient
	audioClient client.AudioProcessor
}

// NewExportService creates a new export service
func NewExportService(r2Client client.StorageClient, audioClient client.AudioProcessor) *ExportService {
	return &ExportService{
		r2Client:    r2Client,
		audioClient: audioClient,
	}
}

// ExportMP3 exports master to MP3 format
func (s *ExportService) ExportMP3(ctx context.Context, req *model.ExportMP3Request) (*model.ExportMP3Response, error) {
	quality := 320
	if req.Quality != nil {
		quality = *req.Quality
	}

	// Use mock response if audio client is not configured
	if s.audioClient == nil {
		return s.exportMP3Mock(quality)
	}

	exportID := uuid.New().String()
	outputKey := fmt.Sprintf("exports/%s.mp3", exportID)

	// Build metadata map if provided
	var metadata map[string]string
	if req.Metadata != nil {
		metadata = make(map[string]string)
		if req.Metadata.Title != "" {
			metadata["title"] = req.Metadata.Title
		}
		if req.Metadata.Artist != "" {
			metadata["artist"] = req.Metadata.Artist
		}
		if req.Metadata.Album != "" {
			metadata["album"] = req.Metadata.Album
		}
		if req.Metadata.Year != nil {
			metadata["year"] = fmt.Sprintf("%d", *req.Metadata.Year)
		}
	}

	encodeReq := &client.EncodeRequest{
		InputURL:  req.MasterFileURL,
		Format:    "mp3",
		Quality:   quality,
		OutputKey: outputKey,
		Metadata:  metadata,
	}

	resp, err := s.audioClient.Encode(ctx, encodeReq)
	if err != nil {
		return nil, fmt.Errorf("MP3 encoding failed: %w", err)
	}

	return &model.ExportMP3Response{
		FileURL:   resp.OutputURL,
		Size:      resp.Size,
		Format:    "mp3",
		Quality:   quality,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

// ExportWAV exports master to WAV format
func (s *ExportService) ExportWAV(ctx context.Context, req *model.ExportWAVRequest) (*model.ExportWAVResponse, error) {
	bitDepth := 24
	sampleRate := 48000

	if req.BitDepth != nil {
		bitDepth = *req.BitDepth
	}
	if req.SampleRate != nil {
		sampleRate = *req.SampleRate
	}

	// Use mock response if audio client is not configured
	if s.audioClient == nil {
		return s.exportWAVMock(bitDepth, sampleRate)
	}

	exportID := uuid.New().String()
	outputKey := fmt.Sprintf("exports/%s.wav", exportID)

	encodeReq := &client.EncodeRequest{
		InputURL:   req.MasterFileURL,
		Format:     "wav",
		BitDepth:   bitDepth,
		SampleRate: sampleRate,
		OutputKey:  outputKey,
	}

	resp, err := s.audioClient.Encode(ctx, encodeReq)
	if err != nil {
		return nil, fmt.Errorf("WAV encoding failed: %w", err)
	}

	return &model.ExportWAVResponse{
		FileURL:    resp.OutputURL,
		Size:       resp.Size,
		Format:     "wav",
		BitDepth:   bitDepth,
		SampleRate: sampleRate,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}, nil
}

// ExportStems exports stems as ZIP
func (s *ExportService) ExportStems(ctx context.Context, req *model.ExportStemsRequest) (*model.ExportStemsResponse, error) {
	// Use mock response if audio client is not configured
	if s.audioClient == nil {
		return s.exportStemsMock(req)
	}

	exportID := uuid.New().String()
	outputKey := fmt.Sprintf("exports/%s.zip", exportID)

	// Build file list for ZIP
	files := make([]client.ZipFileEntry, 0)

	// Add stems
	for i, url := range req.StemURLs {
		files = append(files, client.ZipFileEntry{
			URL:      url,
			Filename: fmt.Sprintf("stems/stem_%d.wav", i+1),
		})
	}

	// Add vocals if requested
	if req.IncludeVocals && len(req.VocalURLs) > 0 {
		for i, url := range req.VocalURLs {
			files = append(files, client.ZipFileEntry{
				URL:      url,
				Filename: fmt.Sprintf("vocals/vocal_%d.wav", i+1),
			})
		}
	}

	// Add master if requested
	if req.IncludeMaster && req.MasterURL != "" {
		files = append(files, client.ZipFileEntry{
			URL:      req.MasterURL,
			Filename: "master.wav",
		})
	}

	zipReq := &client.ZipRequest{
		Files:     files,
		OutputKey: outputKey,
	}

	resp, err := s.audioClient.CreateZip(ctx, zipReq)
	if err != nil {
		return nil, fmt.Errorf("ZIP creation failed: %w", err)
	}

	return &model.ExportStemsResponse{
		FileURL:   resp.OutputURL,
		Size:      resp.Size,
		FileCount: resp.FileCount,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

// Mock implementations for development/testing
func (s *ExportService) exportMP3Mock(quality int) (*model.ExportMP3Response, error) {
	exportID := uuid.New().String()

	return &model.ExportMP3Response{
		FileURL:   fmt.Sprintf("https://cdn.makeasinger.com/exports/%s.mp3", exportID),
		Size:      5242880, // ~5MB
		Format:    "mp3",
		Quality:   quality,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

func (s *ExportService) exportWAVMock(bitDepth, sampleRate int) (*model.ExportWAVResponse, error) {
	exportID := uuid.New().String()

	return &model.ExportWAVResponse{
		FileURL:    fmt.Sprintf("https://cdn.makeasinger.com/exports/%s.wav", exportID),
		Size:       31457280, // ~30MB
		Format:     "wav",
		BitDepth:   bitDepth,
		SampleRate: sampleRate,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}, nil
}

func (s *ExportService) exportStemsMock(req *model.ExportStemsRequest) (*model.ExportStemsResponse, error) {
	exportID := uuid.New().String()
	fileCount := len(req.StemURLs)

	if req.IncludeVocals && len(req.VocalURLs) > 0 {
		fileCount += len(req.VocalURLs)
	}
	if req.IncludeMaster && req.MasterURL != "" {
		fileCount++
	}

	return &model.ExportStemsResponse{
		FileURL:   fmt.Sprintf("https://cdn.makeasinger.com/exports/%s.zip", exportID),
		Size:      52428800, // ~50MB
		FileCount: fileCount,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}
