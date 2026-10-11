package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

// sahteSes, audio-service cagrilarini kaydeder.
type sahteSes struct {
	encode *client.EncodeRequest
	zip    *client.ZipRequest
}

func (s *sahteSes) Master(context.Context, *client.MasterRequest) (*client.MasterResponse, error) {
	return &client.MasterResponse{}, nil
}
func (s *sahteSes) Encode(_ context.Context, r *client.EncodeRequest) (*client.EncodeResponse, error) {
	s.encode = r
	return &client.EncodeResponse{}, nil
}
func (s *sahteSes) CreateZip(_ context.Context, r *client.ZipRequest) (*client.ZipResponse, error) {
	s.zip = r
	return &client.ZipResponse{}, nil
}
func (s *sahteSes) HealthCheck(context.Context) error { return nil }

func TestUploadService_AnahtarKullaniciOnekli(t *testing.T) {
	depo := &sahteDepo{}
	s := NewUploadService(depo, testR2Cfg())

	resp, err := s.UploadVocal(context.Background(), "kullanici-a", "p1", "s1", "take1", nil, 0)
	if err != nil {
		t.Fatalf("UploadVocal: %v", err)
	}
	beklenen := "vocals/kullanici-a/p1/s1/" + resp.ID + ".wav"
	if len(depo.yuklenen) != 1 || depo.yuklenen[0] != beklenen {
		t.Errorf("yuklenen = %v, beklenen %q", depo.yuklenen, beklenen)
	}
}

func TestUploadService_GecersizSegmentYuklenmez(t *testing.T) {
	depo := &sahteDepo{}
	s := NewUploadService(depo, testR2Cfg())

	_, err := s.UploadVocal(context.Background(), "kullanici-a", "../kullanici-b", "s1", "take1", nil, 0)
	if !errors.Is(err, client.ErrGecersizSegment) {
		t.Fatalf("ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
	if len(depo.yuklenen) != 0 {
		t.Errorf("gecersiz istekte yukleme yapildi: %v", depo.yuklenen)
	}
}

// silenDepo, Delete'e giden anahtari kaydeder.
type silenDepo struct {
	sahteDepo
	silinen []string
}

func (d *silenDepo) Delete(_ context.Context, key string) error {
	d.silinen = append(d.silinen, key)
	return nil
}

func TestUploadService_SilmeDeterministikAnahtar(t *testing.T) {
	depo := &silenDepo{}
	s := NewUploadService(depo, testR2Cfg())

	if err := s.DeleteVocal(context.Background(), "kullanici-a", "p1", "s1", "t1"); err != nil {
		t.Fatalf("DeleteVocal: %v", err)
	}
	if len(depo.silinen) != 1 || depo.silinen[0] != "vocals/kullanici-a/p1/s1/t1.wav" {
		t.Errorf("silinen = %v", depo.silinen)
	}
	if err := s.DeleteVocal(context.Background(), "kullanici-a", "p1", "s1", "*"); !errors.Is(err, client.ErrGecersizSegment) {
		t.Errorf("glob takeId icin ErrGecersizSegment bekleniyordu, geldi: %v", err)
	}
}

func TestExportService_BaskasininMasteriReddedilir(t *testing.T) {
	cfg := testR2Cfg()
	ses := &sahteSes{}
	s := NewExportService(&sahteDepo{}, ses, cfg)

	yabanci := client.UnsignedURL("masters/kullanici-b/p1/m1.wav", cfg)
	_, err := s.ExportMP3(context.Background(), "kullanici-a", &model.ExportMP3Request{MasterFileURL: yabanci})
	if !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if ses.encode != nil {
		t.Error("yabanci girdiyle audio-service cagrildi")
	}
}

func TestExportService_CiktiKullaniciOnekli(t *testing.T) {
	cfg := testR2Cfg()
	ses := &sahteSes{}
	s := NewExportService(&sahteDepo{}, ses, cfg)

	kendi := client.UnsignedURL("masters/kullanici-a/p1/m1.wav", cfg)
	if _, err := s.ExportWAV(context.Background(), "kullanici-a", &model.ExportWAVRequest{MasterFileURL: kendi}); err != nil {
		t.Fatalf("ExportWAV: %v", err)
	}
	if ses.encode.InputKey != "masters/kullanici-a/p1/m1.wav" || !strings.HasPrefix(ses.encode.OutputKey, "exports/kullanici-a/") {
		t.Errorf("encode istegi = %+v", ses.encode)
	}
}

func TestExportService_ZipteYabanciStemReddedilir(t *testing.T) {
	cfg := testR2Cfg()
	ses := &sahteSes{}
	s := NewExportService(&sahteDepo{}, ses, cfg)

	req := &model.ExportStemsRequest{StemURLs: []string{
		client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg),
		client.UnsignedURL("stems/kullanici-b/p1/s2.mp3", cfg),
	}}
	if _, err := s.ExportStems(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Fatalf("ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
	if ses.zip != nil {
		t.Error("yabanci stem ile zip olusturuldu")
	}
}

func TestMasterService_StartFinalYabanciStemReddedilir(t *testing.T) {
	cfg := testR2Cfg()
	s := NewMasterService(nil, nil, nil, cfg)

	req := &model.MasterFinalRequest{StemURLs: []string{client.UnsignedURL("stems/kullanici-b/p1/s1.mp3", cfg)}}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Errorf("stem: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}

	req = &model.MasterFinalRequest{
		StemURLs:   []string{client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg)},
		VocalTakes: []model.VocalTake{{FileURL: client.UnsignedURL("vocals/kullanici-b/p1/s1/t1.wav", cfg)}},
	}
	if _, err := s.StartFinal(context.Background(), "kullanici-a", req); !errors.Is(err, client.ErrYabanciAnahtar) {
		t.Errorf("vokal: ErrYabanciAnahtar bekleniyordu, geldi: %v", err)
	}
}
