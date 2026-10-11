package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

func isKaydet(t *testing.T, rdb *redis.Client, job *model.Job) {
	t.Helper()
	if err := jobYaz(context.Background(), rdb, job); err != nil {
		t.Fatalf("jobYaz: %v", err)
	}
}

func TestRenderService_BaskasininIsi404(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	isKaydet(t, rdb, &model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued})

	if _, err := s.GetStatus(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("baska kullanici icin ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	if _, err := s.GetStatus(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Errorf("sahibi kendi isini okuyamadi: %v", err)
	}
}

func TestRenderService_BaskasiIptalEdemez(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	isKaydet(t, rdb, &model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued})

	if _, err := s.CancelRender(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("baska kullanici icin ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	job, err := jobOku(context.Background(), rdb, "is1")
	if err != nil {
		t.Fatalf("jobOku: %v", err)
	}
	if job.Status != model.JobStatusQueued {
		t.Errorf("baskasinin iptali isi degistirdi: %s", job.Status)
	}
}

func TestRenderService_BaskasininSonucu404(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, &sahteDepo{}, testR2Cfg())
	sonuc, _ := json.Marshal(model.RenderResultRecord{ID: "r1", Stems: []model.StemResultRecord{{ID: "s1", FileKey: "stems/kullanici-a/p1/s1.mp3"}}})
	isKaydet(t, rdb, &model.Job{ID: "is1", OwnerID: "kullanici-a", Status: model.JobStatusSucceeded, Result: sonuc})

	if _, err := s.GetResult(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestRenderService_SahipsizKayitKimseyeAcilmaz(t *testing.T) {
	rdb := canliRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	isKaydet(t, rdb, &model.Job{ID: "eski", Status: model.JobStatusQueued})

	if _, err := s.GetStatus(context.Background(), "kullanici-a", "eski"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("OwnerID'siz kayit icin ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestMasterService_BaskasininIsi404(t *testing.T) {
	rdb := canliRedis(t)
	s := NewMasterService(rdb, nil, &sahteDepo{}, testR2Cfg())
	sonuc, _ := json.Marshal(model.MasterResultRecord{FileKey: "masters/kullanici-a/p1/m1.wav"})
	isKaydet(t, rdb, &model.Job{ID: "m1", Type: model.JobTypeMaster, OwnerID: "kullanici-a", Status: model.JobStatusSucceeded, Result: sonuc})

	if _, err := s.GetStatus(context.Background(), "kullanici-b", "m1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("status: ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	if _, err := s.GetResult(context.Background(), "kullanici-b", "m1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("result: ErrJobNotFound bekleniyordu, geldi: %v", err)
	}
	if _, err := s.GetResult(context.Background(), "kullanici-a", "m1"); err != nil {
		t.Errorf("sahibi sonucu okuyamadi: %v", err)
	}
}

// bekleyenGorevPayload, kuyruktaki tek gorevin is payload'ini cozer.
func bekleyenGorevPayload(t *testing.T, kuyruk string, hedef any) {
	t.Helper()
	insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer insp.Close()
	gorevler, err := insp.ListPendingTasks(kuyruk)
	if err != nil || len(gorevler) != 1 {
		t.Fatalf("bekleyen gorev = %d, hata %v", len(gorevler), err)
	}
	var zarf struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(gorevler[0].Payload, &zarf); err != nil {
		t.Fatalf("gorev zarfi: %v", err)
	}
	if err := json.Unmarshal(zarf.Payload, hedef); err != nil {
		t.Fatalf("gorev payload: %v", err)
	}
}

func TestRenderService_StartRenderSahibiYazar(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	resp, err := s.StartRender(context.Background(), "kullanici-a", &model.RenderStartRequest{ProjectID: uuid.New().String()})
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	job, err := jobOku(context.Background(), rdb, resp.JobID)
	if err != nil {
		t.Fatalf("jobOku: %v", err)
	}
	if job.OwnerID != "kullanici-a" {
		t.Fatalf("is kaydi sahibi = %q", job.OwnerID)
	}
	var p model.RenderJobPayload
	bekleyenGorevPayload(t, "render", &p)
	if p.OwnerID != "kullanici-a" {
		t.Errorf("gorev payload sahibi = %q", p.OwnerID)
	}
}

func TestMasterService_StartFinalSahibiYazar(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	cfg := testR2Cfg()
	s := NewMasterService(rdb, ac, nil, cfg)

	req := &model.MasterFinalRequest{
		ProjectID: uuid.New().String(),
		StemURLs:  []string{client.UnsignedURL("stems/kullanici-a/p1/s1.mp3", cfg)},
	}
	resp, err := s.StartFinal(context.Background(), "kullanici-a", req)
	if err != nil {
		t.Fatalf("StartFinal: %v", err)
	}
	job, err := jobOku(context.Background(), rdb, resp.JobID)
	if err != nil {
		t.Fatalf("jobOku: %v", err)
	}
	if job.OwnerID != "kullanici-a" {
		t.Fatalf("is kaydi sahibi = %q", job.OwnerID)
	}
	var p model.MasterJobPayload
	bekleyenGorevPayload(t, "master", &p)
	if p.OwnerID != "kullanici-a" {
		t.Errorf("gorev payload sahibi = %q", p.OwnerID)
	}
}

func TestRenderService_StartRenderStemModunuYazar(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	davul := model.InstrumentDrums
	req := &model.RenderStartRequest{
		ProjectID:       uuid.New().String(),
		Arrangement:     model.Arrangement{Instruments: []model.Instrument{model.InstrumentDrums}},
		StemMode:        model.StemModeFocus,
		FocusInstrument: &davul,
	}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	var p model.RenderJobPayload
	bekleyenGorevPayload(t, "render", &p)
	if p.StemMode != model.StemModeFocus || p.FocusInstrument == nil || *p.FocusInstrument != model.InstrumentDrums {
		t.Errorf("payload stem ayari = %q %v", p.StemMode, p.FocusInstrument)
	}
}

func TestRenderService_StartRenderVarsayilanMix(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	req := &model.RenderStartRequest{ProjectID: uuid.New().String()}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	var p model.RenderJobPayload
	bekleyenGorevPayload(t, "render", &p)
	if p.StemMode != model.StemModeMix {
		t.Errorf("varsayilan mod = %q, mix bekleniyordu", p.StemMode)
	}
}

func TestRenderService_GecersizStemAyariIsYaratmaz(t *testing.T) {
	// Redis nil: dogrulama is kaydindan ONCE olmali, dokunulursa panic.
	s := NewRenderService(nil, nil, nil, testR2Cfg())
	req := &model.RenderStartRequest{ProjectID: uuid.New().String(), StemMode: model.StemModeFocus}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); !errors.Is(err, model.ErrGecersizStemAyari) {
		t.Fatalf("ErrGecersizStemAyari bekleniyordu, geldi: %v", err)
	}
}
