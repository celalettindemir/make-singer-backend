package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

type fakeJobSweeper struct {
	calls int
	err   error
}

func (f *fakeJobSweeper) Sweep(_ context.Context, _ time.Time) (int64, int64, error) {
	f.calls++
	return 2, 3, f.err
}

type fakeLyricsStale struct {
	calls int
	err   error
}

func (f *fakeLyricsStale) FailStale(_ context.Context, _ time.Time) (int, error) {
	f.calls++
	return 1, f.err
}

func TestJobsWorker_SweepRuns(t *testing.T) {
	s := &fakeJobSweeper{}
	w := NewJobsWorker(s, nil)
	if err := w.Sweep(context.Background(), asynq.NewTask(TaskTypeJobsSweep, nil)); err != nil || s.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, s.calls)
	}
	s.err = errors.New("db down")
	if err := w.Sweep(context.Background(), asynq.NewTask(TaskTypeJobsSweep, nil)); !errors.Is(err, s.err) {
		t.Fatalf("err = %v, want the sweep error (asynq logs it)", err)
	}
}

func TestJobsWorker_FailStaleLyricsRuns(t *testing.T) {
	l := &fakeLyricsStale{}
	w := NewJobsWorker(nil, l)
	if err := w.FailStaleLyrics(context.Background(), asynq.NewTask(TaskTypeLyricsStale, nil)); err != nil || l.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, l.calls)
	}
	if TaskTypeJobsSweep != "uretim:temizle" || TaskTypeLyricsStale != "soz:bayat-tara" || QueueMaintenance != "bakim" {
		t.Fatal("wire names changed")
	}
}
