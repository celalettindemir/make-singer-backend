package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// fakeAudio records the audio-service calls.
type fakeAudio struct {
	encode *client.EncodeRequest
	zip    *client.ZipRequest
}

func (s *fakeAudio) Master(context.Context, *client.MasterRequest) (*client.MasterResponse, error) {
	return &client.MasterResponse{}, nil
}
func (s *fakeAudio) Encode(_ context.Context, r *client.EncodeRequest) (*client.EncodeResponse, error) {
	s.encode = r
	return &client.EncodeResponse{}, nil
}
func (s *fakeAudio) CreateZip(_ context.Context, r *client.ZipRequest) (*client.ZipResponse, error) {
	s.zip = r
	return &client.ZipResponse{}, nil
}
func (s *fakeAudio) HealthCheck(context.Context) error { return nil }

func TestUploadService_KeyHasUserPrefix(t *testing.T) {
	storage := &fakeStorage{}
	s := NewUploadService(storage, testR2Cfg())

	resp, err := s.UploadVocal(context.Background(), "kullanici-a", "p1", "s1", "take1", nil, 0)
	if err != nil {
		t.Fatalf("UploadVocal: %v", err)
	}
	want := "vocals/kullanici-a/p1/s1/" + resp.ID + ".wav"
	if len(storage.uploaded) != 1 || storage.uploaded[0] != want {
		t.Errorf("yuklenen = %v, beklenen %q", storage.uploaded, want)
	}
}

func TestUploadService_InvalidSegmentNotUploaded(t *testing.T) {
	storage := &fakeStorage{}
	s := NewUploadService(storage, testR2Cfg())

	_, err := s.UploadVocal(context.Background(), "kullanici-a", "../kullanici-b", "s1", "take1", nil, 0)
	if !errors.Is(err, client.ErrInvalidSegment) {
		t.Fatalf("ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
	if len(storage.uploaded) != 0 {
		t.Errorf("gecersiz istekte yukleme yapildi: %v", storage.uploaded)
	}
}

// deletingStorage records the key passed to Delete.
type deletingStorage struct {
	fakeStorage
	deleted []string
}

func (d *deletingStorage) Delete(_ context.Context, key string) error {
	d.deleted = append(d.deleted, key)
	return nil
}

func TestUploadService_DeleteUsesDeterministicKey(t *testing.T) {
	storage := &deletingStorage{}
	s := NewUploadService(storage, testR2Cfg())

	if err := s.DeleteVocal(context.Background(), "kullanici-a", "p1", "s1", "t1"); err != nil {
		t.Fatalf("DeleteVocal: %v", err)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != "vocals/kullanici-a/p1/s1/t1.wav" {
		t.Errorf("silinen = %v", storage.deleted)
	}
	if err := s.DeleteVocal(context.Background(), "kullanici-a", "p1", "s1", "*"); !errors.Is(err, client.ErrInvalidSegment) {
		t.Errorf("glob takeId icin ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
}

func TestExportService_RejectsOtherUsersMaster(t *testing.T) {
	cfg := testR2Cfg()
	audio := &fakeAudio{}
	s := NewExportService(&fakeStorage{}, audio, cfg)

	foreign := client.UnsignedURL("masters/kullanici-b/p1/m1.wav", cfg)
	_, err := s.ExportMP3(context.Background(), "kullanici-a", &model.ExportMP3Request{MasterFileURL: foreign})
	if !errors.Is(err, client.ErrKeyNotOwned) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if audio.encode != nil {
		t.Error("yabanci girdiyle audio-service cagrildi")
	}
}

func TestExportService_OutputHasUserPrefix(t *testing.T) {
	cfg := testR2Cfg()
	audio := &fakeAudio{}
	s := NewExportService(&fakeStorage{}, audio, cfg)

	own := client.UnsignedURL("masters/kullanici-a/p1/m1.wav", cfg)
	if _, err := s.ExportWAV(context.Background(), "kullanici-a", &model.ExportWAVRequest{MasterFileURL: own}); err != nil {
		t.Fatalf("ExportWAV: %v", err)
	}
	if audio.encode.InputKey != "masters/kullanici-a/p1/m1.wav" || !strings.HasPrefix(audio.encode.OutputKey, "exports/kullanici-a/") {
		t.Errorf("encode istegi = %+v", audio.encode)
	}
}

func TestExportService_RejectsForeignStemInZip(t *testing.T) {
	cfg := testR2Cfg()
	audio := &fakeAudio{}
	s := NewExportService(&fakeStorage{}, audio, cfg)

	req := &model.ExportStemsRequest{StemURLs: []string{
		client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg),
		client.UnsignedURL("stems/kullanici-b/p1/s2.mp3", cfg),
	}}
	if _, err := s.ExportStems(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrKeyNotOwned) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if audio.zip != nil {
		t.Error("yabanci stem ile zip olusturuldu")
	}
}

func TestMasterService_StartFinalRejectsForeignStem(t *testing.T) {
	cfg := testR2Cfg()
	s := NewMasterService(nil, nil, nil, cfg)

	req := &model.MasterFinalRequest{StemURLs: []string{client.UnsignedURL("stems/kullanici-b/p1/s1.mp3", cfg)}}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrKeyNotOwned) {
		t.Errorf("stem: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}

	req = &model.MasterFinalRequest{
		StemURLs:   []string{client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg)},
		VocalTakes: []model.VocalTake{{FileURL: client.UnsignedURL("vocals/kullanici-b/p1/s1/t1.wav", cfg)}},
	}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrKeyNotOwned) {
		t.Errorf("vokal: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
}

func TestExportService_MockBranchChecksOwnership(t *testing.T) {
	cfg := testR2Cfg()
	s := NewExportService(nil, nil, cfg) // audioClient nil: mock branch

	foreign := client.UnsignedURL("masters/kullanici-b/p1/m1.wav", cfg)
	if _, err := s.ExportMP3(context.Background(), "kullanici-a", &model.ExportMP3Request{MasterFileURL: foreign}); !errors.Is(err, client.ErrKeyNotOwned) {
		t.Errorf("mp3: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if _, err := s.ExportWAV(context.Background(), "kullanici-a", &model.ExportWAVRequest{MasterFileURL: foreign}); !errors.Is(err, client.ErrKeyNotOwned) {
		t.Errorf("wav: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	stems := &model.ExportStemsRequest{StemURLs: []string{client.UnsignedURL("stems/kullanici-b/p1/s1.mp3", cfg)}}
	if _, err := s.ExportStems(context.Background(), "kullanici-a", stems); !errors.Is(err, client.ErrKeyNotOwned) {
		t.Errorf("stems: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}

	own := client.UnsignedURL("masters/kullanici-a/p1/m1.wav", cfg)
	if _, err := s.ExportMP3(context.Background(), "kullanici-a", &model.ExportMP3Request{MasterFileURL: own}); err != nil {
		t.Errorf("kendi master mock: %v", err)
	}
}

func TestService_UnknownHostIsInvalidURL(t *testing.T) {
	cfg := testR2Cfg()
	bad := "https://cdn.example.com/masters/kullanici-a/p1/m1.wav"

	ex := NewExportService(nil, nil, cfg)
	_, err := ex.ExportMP3(context.Background(), "kullanici-a", &model.ExportMP3Request{MasterFileURL: bad})
	if !errors.Is(err, client.ErrInvalidURL) || errors.Is(err, client.ErrKeyNotOwned) {
		t.Errorf("ExportMP3: ErrGecersizURL bekleniyordu, geldi: %v", err)
	}

	ms := NewMasterService(nil, nil, nil, cfg)
	_, err = ms.StartFinal(context.Background(), "kullanici-a", &model.MasterFinalRequest{StemURLs: []string{bad}})
	if !errors.Is(err, client.ErrInvalidURL) || errors.Is(err, client.ErrKeyNotOwned) {
		t.Errorf("StartFinal: ErrGecersizURL bekleniyordu, geldi: %v", err)
	}
}
