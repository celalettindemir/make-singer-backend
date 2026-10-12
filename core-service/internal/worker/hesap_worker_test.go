package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/service"
)

type kayitSilici struct{ onekler []string }

func (k *kayitSilici) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	k.onekler = append(k.onekler, prefix)
	return 0, nil
}

type bosKimlik struct{}

func (bosKimlik) HesapSil(ctx context.Context, userID string) error { return nil }

func TestHesapWorker_OnekleriTemizler(t *testing.T) {
	s := &kayitSilici{}
	svc := service.NewHesapService(bosKimlik{}, s, nil, nil, nil)
	gorev := asynq.NewTask(service.TaskTypeHesapTemizle, []byte(`{"userId":"6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"}`))
	if err := NewHesapWorker(svc).ProcessTask(context.Background(), gorev); err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if len(s.onekler) != 5 {
		t.Fatalf("5 onek bekleniyordu: %v", s.onekler)
	}
}

func TestHesapWorker_BozukYukTekrarlanmaz(t *testing.T) {
	svc := service.NewHesapService(bosKimlik{}, &kayitSilici{}, nil, nil, nil)
	err := NewHesapWorker(svc).ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeHesapTemizle, []byte("{")))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, gelen %v", err)
	}
}
