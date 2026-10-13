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

func storeJob(t *testing.T, rdb *redis.Client, job *model.Job) {
	t.Helper()
	if err := writeJob(context.Background(), rdb, job); err != nil {
		t.Fatalf("writeJob: %v", err)
	}
}

func TestRenderService_OtherUsersJobNotFound(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	storeJob(t, rdb, &model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued})

	if _, err := s.GetStatus(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("want ErrJobNotFound for another user, got: %v", err)
	}
	if _, err := s.GetStatus(context.Background(), "kullanici-a", "is1"); err != nil {
		t.Errorf("the owner could not read their own job: %v", err)
	}
}

func TestRenderService_OtherUserCannotCancel(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	storeJob(t, rdb, &model.Job{ID: "is1", Type: model.JobTypeRender, OwnerID: "kullanici-a", Status: model.JobStatusQueued})

	if _, err := s.CancelRender(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("want ErrJobNotFound for another user, got: %v", err)
	}
	job, err := readJob(context.Background(), rdb, "is1")
	if err != nil {
		t.Fatalf("readJob: %v", err)
	}
	if job.Status != model.JobStatusQueued {
		t.Errorf("someone else's cancel changed the job: %s", job.Status)
	}
}

func TestRenderService_OtherUsersResultNotFound(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, &fakeStorage{}, testR2Cfg())
	result, _ := json.Marshal(model.RenderResultRecord{ID: "r1", Stems: []model.StemResultRecord{{ID: "s1", FileKey: "stems/kullanici-a/p1/s1.mp3"}}})
	storeJob(t, rdb, &model.Job{ID: "is1", OwnerID: "kullanici-a", Status: model.JobStatusSucceeded, Result: result})

	if _, err := s.GetResult(context.Background(), "kullanici-b", "is1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("want ErrJobNotFound, got: %v", err)
	}
}

func TestRenderService_OwnerlessRecordHiddenFromAll(t *testing.T) {
	rdb := liveRedis(t)
	s := NewRenderService(rdb, nil, nil, testR2Cfg())
	storeJob(t, rdb, &model.Job{ID: "eski", Status: model.JobStatusQueued})

	if _, err := s.GetStatus(context.Background(), "kullanici-a", "eski"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("want ErrJobNotFound for a record without OwnerID, got: %v", err)
	}
}

func TestMasterService_OtherUsersJobNotFound(t *testing.T) {
	rdb := liveRedis(t)
	s := NewMasterService(rdb, nil, &fakeStorage{}, testR2Cfg())
	result, _ := json.Marshal(model.MasterResultRecord{FileKey: "masters/kullanici-a/p1/m1.wav"})
	storeJob(t, rdb, &model.Job{ID: "m1", Type: model.JobTypeMaster, OwnerID: "kullanici-a", Status: model.JobStatusSucceeded, Result: result})

	if _, err := s.GetStatus(context.Background(), "kullanici-b", "m1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("status: want ErrJobNotFound, got: %v", err)
	}
	if _, err := s.GetResult(context.Background(), "kullanici-b", "m1"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("result: want ErrJobNotFound, got: %v", err)
	}
	if _, err := s.GetResult(context.Background(), "kullanici-a", "m1"); err != nil {
		t.Errorf("the owner could not read the result: %v", err)
	}
}

// pendingTaskPayload decodes the job payload of the single pending task in the queue.
func pendingTaskPayload(t *testing.T, queue string, target any) {
	t.Helper()
	insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	defer insp.Close()
	tasks, err := insp.ListPendingTasks(queue)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("pending tasks = %d, error %v", len(tasks), err)
	}
	var envelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(tasks[0].Payload, &envelope); err != nil {
		t.Fatalf("task envelope: %v", err)
	}
	if err := json.Unmarshal(envelope.Payload, target); err != nil {
		t.Fatalf("task payload: %v", err)
	}
}

func TestRenderService_StartRenderWritesOwner(t *testing.T) {
	rdb := liveRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	resp, err := s.StartRender(context.Background(), "kullanici-a", &model.RenderStartRequest{ProjectID: uuid.New().String()})
	if err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	job, err := readJob(context.Background(), rdb, resp.JobID)
	if err != nil {
		t.Fatalf("readJob: %v", err)
	}
	if job.OwnerID != "kullanici-a" {
		t.Fatalf("job record owner = %q", job.OwnerID)
	}
	var p model.RenderJobPayload
	pendingTaskPayload(t, "render", &p)
	if p.OwnerID != "kullanici-a" {
		t.Errorf("task payload owner = %q", p.OwnerID)
	}
}

func TestMasterService_StartFinalWritesOwner(t *testing.T) {
	rdb := liveRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
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
	job, err := readJob(context.Background(), rdb, resp.JobID)
	if err != nil {
		t.Fatalf("readJob: %v", err)
	}
	if job.OwnerID != "kullanici-a" {
		t.Fatalf("job record owner = %q", job.OwnerID)
	}
	var p model.MasterJobPayload
	pendingTaskPayload(t, "master", &p)
	if p.OwnerID != "kullanici-a" {
		t.Errorf("task payload owner = %q", p.OwnerID)
	}
}

func TestRenderService_StartRenderWritesStemMode(t *testing.T) {
	rdb := liveRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	drums := model.InstrumentDrums
	req := &model.RenderStartRequest{
		ProjectID:       uuid.New().String(),
		Arrangement:     model.Arrangement{Instruments: []model.Instrument{model.InstrumentDrums}},
		StemMode:        model.StemModeFocus,
		FocusInstrument: &drums,
	}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	var p model.RenderJobPayload
	pendingTaskPayload(t, "render", &p)
	if p.StemMode != model.StemModeFocus || p.FocusInstrument == nil || *p.FocusInstrument != model.InstrumentDrums {
		t.Errorf("payload stem setting = %q %v", p.StemMode, p.FocusInstrument)
	}
}

func TestRenderService_StartRenderDefaultsToMix(t *testing.T) {
	rdb := liveRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: liveRedisAddr, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	req := &model.RenderStartRequest{ProjectID: uuid.New().String()}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	var p model.RenderJobPayload
	pendingTaskPayload(t, "render", &p)
	if p.StemMode != model.StemModeMix {
		t.Errorf("default mode = %q, want mix", p.StemMode)
	}
}

func TestRenderService_InvalidStemSettingsCreateNoJob(t *testing.T) {
	// Redis is nil: validation must happen BEFORE the job record; panics if touched.
	s := NewRenderService(nil, nil, nil, testR2Cfg())
	req := &model.RenderStartRequest{ProjectID: uuid.New().String(), StemMode: model.StemModeFocus}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); !errors.Is(err, model.ErrInvalidStemSettings) {
		t.Fatalf("want ErrInvalidStemSettings, got: %v", err)
	}
}
