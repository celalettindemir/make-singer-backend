package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/service"
)

type recordingDeleter struct{ prefixes []string }

func (k *recordingDeleter) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	k.prefixes = append(k.prefixes, prefix)
	return 0, nil
}

type nopIdentity struct{}

func (nopIdentity) DeleteAccount(ctx context.Context, userID string) error { return nil }

func TestAccountWorker_CleansPrefixes(t *testing.T) {
	s := &recordingDeleter{}
	svc := service.NewAccountService(nopIdentity{}, s, nil, nil, nil)
	task := asynq.NewTask(service.TaskTypeAccountCleanup, []byte(`{"userId":"6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"}`))
	if err := NewAccountWorker(svc).ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.prefixes) != 6 {
		t.Fatalf("expected 6 prefixes: %v", s.prefixes)
	}
}

func TestAccountWorker_MalformedPayloadNotRetried(t *testing.T) {
	svc := service.NewAccountService(nopIdentity{}, &recordingDeleter{}, nil, nil, nil)
	err := NewAccountWorker(svc).ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeAccountCleanup, []byte("{")))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("want SkipRetry, got %v", err)
	}
}
