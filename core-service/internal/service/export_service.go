package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// FileExporter defines the interface for file export operations
type FileExporter interface {
	ExportMP3(ctx context.Context, userID string, req *model.ExportMP3Request) (*model.ExportMP3Response, error)
	ExportWAV(ctx context.Context, userID string, req *model.ExportWAVRequest) (*model.ExportWAVResponse, error)
	ExportStems(ctx context.Context, userID string, req *model.ExportStemsRequest) (*model.ExportStemsResponse, error)
}

// ExportService handles file exports using the audio processing service
type ExportService struct {
	r2Client    client.StorageClient
	audioClient client.AudioProcessor
	r2Cfg       *config.R2Config
}

// NewExportService creates a new export service
func NewExportService(r2Client client.StorageClient, audioClient client.AudioProcessor, r2Cfg *config.R2Config) *ExportService {
	return &ExportService{
		r2Client:    r2Client,
		audioClient: audioClient,
		r2Cfg:       r2Cfg,
	}
}

// ExportMP3 exports master to MP3 format
func (s *ExportService) ExportMP3(ctx context.Context, userID string, req *model.ExportMP3Request) (*model.ExportMP3Response, error) {
	quality := 320
	if req.Quality != nil {
		quality = *req.Quality
	}

	// Sahiplik denetimi mock dalindan once: dev ve prod ayni davranir.
	inputKey, err := client.ResolveOwnedKey(req.MasterFileURL, userID, s.r2Cfg)
	if err != nil {
		return nil, fmt.Errorf("master URL cozulemedi: %w", err)
	}

	// Use mock response if audio client is not configured
	if s.audioClient == nil {
		return s.exportMP3Mock(userID, quality)
	}

	exportID := uuid.New().String()
	outputKey, err := client.UserKey("exports", userID, exportID+".mp3")
	if err != nil {
		return nil, err
	}

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
		InputKey:  inputKey,
		Format:    "mp3",
		Quality:   quality,
		OutputKey: outputKey,
		Metadata:  metadata,
	}

	resp, err := s.audioClient.Encode(ctx, encodeReq)
	if err != nil {
		return nil, fmt.Errorf("MP3 encoding failed: %w", err)
	}

	// outputKey public (exports/) oldugu icin URLFor kalici CDN adresi
	// dondurur; audio-service'ten gelen resp.OutputURL imzasiz oldugundan
	// dogrudan istemciye verilmez.
	fileURL, _, err := s.r2Client.URLFor(ctx, outputKey)
	if err != nil {
		return nil, fmt.Errorf("export URL uretilemedi: %w", err)
	}

	return &model.ExportMP3Response{
		FileURL:   fileURL,
		Size:      resp.Size,
		Format:    "mp3",
		Quality:   quality,
		ExpiresAt: nil,
	}, nil
}

// ExportWAV exports master to WAV format
func (s *ExportService) ExportWAV(ctx context.Context, userID string, req *model.ExportWAVRequest) (*model.ExportWAVResponse, error) {
	bitDepth := 24
	sampleRate := 48000

	if req.BitDepth != nil {
		bitDepth = *req.BitDepth
	}
	if req.SampleRate != nil {
		sampleRate = *req.SampleRate
	}

	// Sahiplik denetimi mock dalindan once: dev ve prod ayni davranir.
	inputKey, err := client.ResolveOwnedKey(req.MasterFileURL, userID, s.r2Cfg)
	if err != nil {
		return nil, fmt.Errorf("master URL cozulemedi: %w", err)
	}

	// Use mock response if audio client is not configured
	if s.audioClient == nil {
		return s.exportWAVMock(userID, bitDepth, sampleRate)
	}

	exportID := uuid.New().String()
	outputKey, err := client.UserKey("exports", userID, exportID+".wav")
	if err != nil {
		return nil, err
	}

	encodeReq := &client.EncodeRequest{
		InputKey:   inputKey,
		Format:     "wav",
		BitDepth:   bitDepth,
		SampleRate: sampleRate,
		OutputKey:  outputKey,
	}

	resp, err := s.audioClient.Encode(ctx, encodeReq)
	if err != nil {
		return nil, fmt.Errorf("WAV encoding failed: %w", err)
	}

	// outputKey public (exports/) oldugu icin URLFor kalici CDN adresi
	// dondurur; audio-service'ten gelen resp.OutputURL imzasiz oldugundan
	// dogrudan istemciye verilmez.
	fileURL, _, err := s.r2Client.URLFor(ctx, outputKey)
	if err != nil {
		return nil, fmt.Errorf("export URL uretilemedi: %w", err)
	}

	return &model.ExportWAVResponse{
		FileURL:    fileURL,
		Size:       resp.Size,
		Format:     "wav",
		BitDepth:   bitDepth,
		SampleRate: sampleRate,
		ExpiresAt:  nil,
	}, nil
}

// ExportStems exports stems as ZIP
func (s *ExportService) ExportStems(ctx context.Context, userID string, req *model.ExportStemsRequest) (*model.ExportStemsResponse, error) {
	// Sahiplik denetimi mock dalindan once: dev ve prod ayni davranir.
	// Build file list for ZIP
	files := make([]client.ZipFileEntry, 0)

	// Add stems
	for i, url := range req.StemURLs {
		key, err := client.ResolveOwnedKey(url, userID, s.r2Cfg)
		if err != nil {
			return nil, fmt.Errorf("zip girdisi cozulemedi: %w", err)
		}
		files = append(files, client.ZipFileEntry{
			Key:      key,
			Filename: fmt.Sprintf("stems/stem_%d.wav", i+1),
		})
	}

	// Add vocals if requested
	if req.IncludeVocals && len(req.VocalURLs) > 0 {
		for i, url := range req.VocalURLs {
			key, err := client.ResolveOwnedKey(url, userID, s.r2Cfg)
			if err != nil {
				return nil, fmt.Errorf("zip girdisi cozulemedi: %w", err)
			}
			files = append(files, client.ZipFileEntry{
				Key:      key,
				Filename: fmt.Sprintf("vocals/vocal_%d.wav", i+1),
			})
		}
	}

	// Add master if requested
	if req.IncludeMaster && req.MasterURL != "" {
		key, err := client.ResolveOwnedKey(req.MasterURL, userID, s.r2Cfg)
		if err != nil {
			return nil, fmt.Errorf("zip girdisi cozulemedi: %w", err)
		}
		files = append(files, client.ZipFileEntry{
			Key:      key,
			Filename: "master.wav",
		})
	}

	// Use mock response if audio client is not configured
	if s.audioClient == nil {
		return s.exportStemsMock(userID, req)
	}

	exportID := uuid.New().String()
	outputKey, err := client.UserKey("exports", userID, exportID+".zip")
	if err != nil {
		return nil, err
	}

	zipReq := &client.ZipRequest{
		Files:     files,
		OutputKey: outputKey,
	}

	resp, err := s.audioClient.CreateZip(ctx, zipReq)
	if err != nil {
		return nil, fmt.Errorf("ZIP creation failed: %w", err)
	}

	// outputKey public (exports/) oldugu icin URLFor kalici CDN adresi
	// dondurur; audio-service'ten gelen resp.OutputURL imzasiz oldugundan
	// dogrudan istemciye verilmez.
	fileURL, _, err := s.r2Client.URLFor(ctx, outputKey)
	if err != nil {
		return nil, fmt.Errorf("export URL uretilemedi: %w", err)
	}

	return &model.ExportStemsResponse{
		FileURL:   fileURL,
		Size:      resp.Size,
		FileCount: resp.FileCount,
		ExpiresAt: nil,
	}, nil
}

// Mock implementations for development/testing
func (s *ExportService) exportMP3Mock(userID string, quality int) (*model.ExportMP3Response, error) {
	key, err := client.UserKey("exports", userID, uuid.New().String()+".mp3")
	if err != nil {
		return nil, err
	}
	return &model.ExportMP3Response{
		FileURL:   client.UnsignedURL(key, s.r2Cfg),
		Size:      5242880, // ~5MB
		Format:    "mp3",
		Quality:   quality,
		ExpiresAt: nil, // mock export de public/kalicidir, suresi yok
	}, nil
}

func (s *ExportService) exportWAVMock(userID string, bitDepth, sampleRate int) (*model.ExportWAVResponse, error) {
	key, err := client.UserKey("exports", userID, uuid.New().String()+".wav")
	if err != nil {
		return nil, err
	}
	return &model.ExportWAVResponse{
		FileURL:    client.UnsignedURL(key, s.r2Cfg),
		Size:       31457280, // ~30MB
		Format:     "wav",
		BitDepth:   bitDepth,
		SampleRate: sampleRate,
		ExpiresAt:  nil, // mock export de public/kalicidir, suresi yok
	}, nil
}

func (s *ExportService) exportStemsMock(userID string, req *model.ExportStemsRequest) (*model.ExportStemsResponse, error) {
	key, err := client.UserKey("exports", userID, uuid.New().String()+".zip")
	if err != nil {
		return nil, err
	}
	fileCount := len(req.StemURLs)

	if req.IncludeVocals && len(req.VocalURLs) > 0 {
		fileCount += len(req.VocalURLs)
	}
	if req.IncludeMaster && req.MasterURL != "" {
		fileCount++
	}

	return &model.ExportStemsResponse{
		FileURL:   client.UnsignedURL(key, s.r2Cfg),
		Size:      52428800, // ~50MB
		FileCount: fileCount,
		ExpiresAt: nil, // mock export de public/kalicidir, suresi yok
	}, nil
}
