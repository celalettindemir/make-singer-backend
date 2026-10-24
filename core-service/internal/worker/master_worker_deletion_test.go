package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

// fakeAudio counts Master calls. The real audio service writes the output
// to R2 itself, so the Master call is the master job's R2 upload.
type fakeAudio struct {
	masterCalls int
	err         error // if nil, an error is still returned (see Master)
}

func (s *fakeAudio) Master(context.Context, *client.MasterRequest) (*client.MasterResponse, error) {
	s.masterCalls++
	if s.err == nil {
		// The success path enters the hub broadcast (no hub in the test); the
		// call is counted and an error is returned.
		return nil, errors.New("fake audio: master was called")
	}
	return nil, s.err
}
func (s *fakeAudio) Encode(context.Context, *client.EncodeRequest) (*client.EncodeResponse, error) {
	return nil, nil
}
func (s *fakeAudio) CreateZip(context.Context, *client.ZipRequest) (*client.ZipResponse, error) {
	return nil, nil
}
func (s *fakeAudio) HealthCheck(context.Context) error { return nil }

// masterTestWorker: the job store holds no job; status updates are silently
// skipped (the hub is never touched).
func masterTestWorker(audio *fakeAudio, h *fakeChecker) *MasterWorker {
	// To avoid the typed-nil trap, a nil pointer is not assigned to the interface.
	var d deletionmark.Checker
	if h != nil {
		d = h
	}
	return NewMasterWorker(jobs.NewMemoryStore(nil), audio, &fakeStorage{}, nil, nil, testR2Cfg(), d)
}

func masterPayload() *model.MasterJobPayload {
	return &model.MasterJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", Profile: model.MasterProfile("clean")}
}

func TestMasterWorker_NoUploadForDeletedOwner(t *testing.T) {
	audio := &fakeAudio{}
	h := &fakeChecker{answer: alwaysDeleted}
	err := masterTestWorker(audio, h).processWithAudioService(context.Background(), "j1", masterPayload())
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got: %v", err)
	}
	if audio.masterCalls != 0 {
		t.Fatalf("master was called %d times for a deleted owner", audio.masterCalls)
	}
	if len(h.asked) == 0 || h.asked[0] != "kullanici-a" {
		t.Fatalf("the job owner was not checked: %v", h.asked)
	}
}

func TestMasterWorker_CheckErrorIsRetryable(t *testing.T) {
	audio := &fakeAudio{}
	h := &fakeChecker{answer: func(int) (bool, error) { return false, errors.New("redis is down") }}
	err := masterTestWorker(audio, h).processWithAudioService(context.Background(), "j1", masterPayload())
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("a check error must be retryable, got: %v", err)
	}
	if audio.masterCalls != 0 {
		t.Fatalf("master was called %d times on a check error", audio.masterCalls)
	}
}

func TestMasterWorker_CallsMasterForLiveOwner(t *testing.T) {
	audio := &fakeAudio{}
	h := &fakeChecker{answer: func(int) (bool, error) { return false, nil }}
	_ = masterTestWorker(audio, h).processWithAudioService(context.Background(), "j1", masterPayload())
	if audio.masterCalls != 1 {
		t.Fatalf("master was called %d times, want 1", audio.masterCalls)
	}
}

func TestMasterWorker_NoCheckWithoutChecker(t *testing.T) {
	audio := &fakeAudio{}
	_ = masterTestWorker(audio, nil).processWithAudioService(context.Background(), "j1", masterPayload())
	if audio.masterCalls != 1 {
		t.Fatalf("master was called %d times, want 1", audio.masterCalls)
	}
}
