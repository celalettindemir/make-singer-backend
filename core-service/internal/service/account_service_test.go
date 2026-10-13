package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/identity"
	"github.com/redis/go-redis/v9"
)

type fakeIdentity struct {
	deleted []string
	err     error
}

func (f *fakeIdentity) DeleteAccount(ctx context.Context, userID string) error {
	f.deleted = append(f.deleted, userID)
	return f.err
}

type fakeDeleter struct {
	prefixes []string
	err      error
}

func (s *fakeDeleter) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	s.prefixes = append(s.prefixes, prefix)
	return 1, s.err
}

type fakeQueue struct {
	tasks   []*asynq.Task
	options [][]asynq.Option
}

func (q *fakeQueue) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, t)
	q.options = append(q.options, opts)
	return &asynq.TaskInfo{}, nil
}

// unreachableRedis: writing the mark fails; Delete must log that and
// carry on. That the mark is really written is measured in the live test.
func unreachableRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})
}

const accountID = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

func TestAccountService_DeleteRemovesAllAndSchedulesCleanup(t *testing.T) {
	ident, deleter, queue := &fakeIdentity{}, &fakeDeleter{}, &fakeQueue{}
	svc := NewAccountService(ident, deleter, unreachableRedis(), queue, nil)
	if err := svc.Delete(context.Background(), accountID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(ident.deleted, []string{accountID}) {
		t.Fatalf("deleted ID = %v", ident.deleted)
	}
	if len(deleter.prefixes) != 5 {
		t.Fatalf("expected 5 prefixes, got %v", deleter.prefixes)
	}
	if len(queue.tasks) != 1 || queue.tasks[0].Type() != TaskTypeAccountCleanup {
		t.Fatalf("the second cleanup task was not set up: %v", queue.tasks)
	}
	var p struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(queue.tasks[0].Payload(), &p); err != nil || p.UserID != accountID {
		t.Fatalf("task payload = %s", queue.tasks[0].Payload())
	}
}

func TestAccountService_DeleteMissingIdentityIsNotFound(t *testing.T) {
	ident, deleter, queue := &fakeIdentity{err: identity.ErrUserNotFound}, &fakeDeleter{}, &fakeQueue{}
	err := NewAccountService(ident, deleter, unreachableRedis(), queue, nil).Delete(context.Background(), accountID)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("want ErrAccountNotFound, got %v", err)
	}
	if len(deleter.prefixes) != 0 || len(queue.tasks) != 0 {
		t.Fatal("R2 must not be touched before the identity is deleted")
	}
}

func TestAccountService_DeleteIdentityErrorLeavesR2Untouched(t *testing.T) {
	ident, deleter, queue := &fakeIdentity{err: errors.New("pg is down")}, &fakeDeleter{}, &fakeQueue{}
	err := NewAccountService(ident, deleter, unreachableRedis(), queue, nil).Delete(context.Background(), accountID)
	if err == nil || errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("want a generic error, got %v", err)
	}
	if len(deleter.prefixes) != 0 || len(queue.tasks) != 0 {
		t.Fatal("R2 must not be touched before the identity is deleted")
	}
}

func TestAccountService_DeleteR2ErrorDoesNotFailRequest(t *testing.T) {
	ident, deleter, queue := &fakeIdentity{}, &fakeDeleter{err: errors.New("r2 is down")}, &fakeQueue{}
	if err := NewAccountService(ident, deleter, unreachableRedis(), queue, nil).Delete(context.Background(), accountID); err != nil {
		t.Fatalf("no error must be returned after the account is deleted: %v", err)
	}
	if len(queue.tasks) != 1 {
		t.Fatal("the second cleanup must still be set up on an R2 error")
	}
}

func TestAccountService_DeleteInvalidIDDeletesNothing(t *testing.T) {
	ident, deleter, queue := &fakeIdentity{}, &fakeDeleter{}, &fakeQueue{}
	err := NewAccountService(ident, deleter, unreachableRedis(), queue, nil).Delete(context.Background(), "../x")
	if !errors.Is(err, ErrAccountNotFound) || len(ident.deleted) != 0 {
		t.Fatalf("an invalid id must not be deleted: err=%v deleted=%v", err, ident.deleted)
	}
}

func TestAccountService_CleanupWithoutR2NoError(t *testing.T) {
	svc := NewAccountService(&fakeIdentity{}, nil, unreachableRedis(), &fakeQueue{}, nil)
	if err := svc.Cleanup(context.Background(), accountID); err != nil {
		t.Fatalf("cleanup must be a no-op without R2: %v", err)
	}
}

// The mark lives for the job record lifetime (24 hours): jobs waiting in the
// queue or running must be able to see during this time that the owner was deleted.
func TestAccountService_DeleteMarkLives24Hours(t *testing.T) {
	rdb := liveRedis(t)
	svc := NewAccountService(&fakeIdentity{}, &fakeDeleter{}, rdb, &fakeQueue{}, nil)
	if err := svc.Delete(context.Background(), accountID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ttl, err := rdb.TTL(context.Background(), deletionmark.Key(accountID)).Result()
	if err != nil || ttl <= 23*time.Hour || ttl > 24*time.Hour {
		t.Fatalf("marker TTL = %v (err %v), want ~24 hours", ttl, err)
	}
}

// The delayed cleanup runs one hour later: far later than the longest job
// attempt (render default 30 min timeout).
func TestAccountService_DeleteSchedulesCleanupOneHourLater(t *testing.T) {
	queue := &fakeQueue{}
	if err := NewAccountService(&fakeIdentity{}, &fakeDeleter{}, unreachableRedis(), queue, nil).Delete(context.Background(), accountID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queue.options) != 1 {
		t.Fatalf("expected a single task: %d", len(queue.options))
	}
	var delay time.Duration
	for _, o := range queue.options[0] {
		if o.Type() == asynq.ProcessInOpt {
			delay = o.Value().(time.Duration)
		}
	}
	if delay != time.Hour {
		t.Fatalf("delay = %v, want 1 hour", delay)
	}
}
