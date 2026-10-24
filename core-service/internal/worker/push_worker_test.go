package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/push"
)

type fakePushRepo struct {
	job     *model.Job
	devices []model.Device
	deleted []string
}

func (r *fakePushRepo) Get(context.Context, string) (*model.Job, error) {
	if r.job == nil {
		return nil, jobs.ErrJobNotFound
	}
	return r.job, nil
}
func (r *fakePushRepo) Devices(context.Context, string) ([]model.Device, error) {
	return r.devices, nil
}
func (r *fakePushRepo) DeleteToken(_ context.Context, token string) error {
	r.deleted = append(r.deleted, token)
	return nil
}

type fakeSender struct {
	errs map[string]error
	sent map[string]push.Message
}

func (s *fakeSender) Send(_ context.Context, token string, m push.Message) error {
	if s.sent == nil {
		s.sent = map[string]push.Message{}
	}
	if err := s.errs[token]; err != nil {
		return err
	}
	s.sent[token] = m
	return nil
}

func pushTask(t *testing.T) *asynq.Task {
	t.Helper()
	b, _ := json.Marshal(PushTaskPayload{JobID: "j1"})
	return asynq.NewTask(TaskTypePush, b)
}

func finishedJob() *model.Job {
	return &model.Job{ID: "j1", OwnerID: "u1", Type: "render", Status: model.JobStatusSucceeded}
}

func TestPushWorker_DisabledSendsNothing(t *testing.T) {
	repo := &fakePushRepo{job: finishedJob(), devices: []model.Device{{Token: "a", Language: "tr"}}}
	s := &fakeSender{}
	w := NewPushWorker(repo, s, nil, false)
	if err := w.ProcessTask(context.Background(), pushTask(t)); err != nil {
		t.Fatal(err)
	}
	if len(s.sent) != 0 {
		t.Errorf("sent %v while disabled", s.sent)
	}
}

func TestPushWorker_SendsInDeviceLanguage(t *testing.T) {
	repo := &fakePushRepo{job: finishedJob(), devices: []model.Device{{Token: "a", Language: "tr"}, {Token: "b", Language: "en"}}}
	s := &fakeSender{}
	w := NewPushWorker(repo, s, nil, true)
	if err := w.ProcessTask(context.Background(), pushTask(t)); err != nil {
		t.Fatal(err)
	}
	if s.sent["a"].Body != "Şarkın hazır — dinlemeye gel" || s.sent["b"].Body != "Your song is ready — come listen" {
		t.Errorf("sent = %+v", s.sent)
	}
}

func TestPushWorker_DeletesUnregisteredToken(t *testing.T) {
	repo := &fakePushRepo{job: finishedJob(), devices: []model.Device{{Token: "dead", Language: "tr"}, {Token: "ok", Language: "tr"}}}
	s := &fakeSender{errs: map[string]error{"dead": push.ErrUnregistered}}
	w := NewPushWorker(repo, s, nil, true)
	if err := w.ProcessTask(context.Background(), pushTask(t)); err != nil {
		t.Fatal(err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "dead" {
		t.Errorf("deleted = %v", repo.deleted)
	}
	if _, ok := s.sent["ok"]; !ok {
		t.Error("the live device was not notified")
	}
}

func TestPushWorker_RetriesOnlyWhenEveryDeviceFailed(t *testing.T) {
	boom := errors.New("boom")
	repo := &fakePushRepo{job: finishedJob(), devices: []model.Device{{Token: "a"}, {Token: "b"}}}
	w := NewPushWorker(repo, &fakeSender{errs: map[string]error{"a": boom, "b": boom}}, nil, true)
	if err := w.ProcessTask(context.Background(), pushTask(t)); err == nil {
		t.Error("want an error when every device failed")
	}
	w = NewPushWorker(repo, &fakeSender{errs: map[string]error{"a": boom}}, nil, true)
	if err := w.ProcessTask(context.Background(), pushTask(t)); err != nil {
		t.Errorf("partial failure must not retry: %v", err)
	}
}

func TestPushWorker_SkipsUnfinishedAndMissingJobs(t *testing.T) {
	s := &fakeSender{}
	repo := &fakePushRepo{job: &model.Job{ID: "j1", Status: model.JobStatusRunning}, devices: []model.Device{{Token: "a"}}}
	if err := NewPushWorker(repo, s, nil, true).ProcessTask(context.Background(), pushTask(t)); err != nil || len(s.sent) != 0 {
		t.Errorf("running job: err=%v sent=%v", err, s.sent)
	}
	repo.job = nil
	if err := NewPushWorker(repo, s, nil, true).ProcessTask(context.Background(), pushTask(t)); err != nil {
		t.Errorf("missing job: %v", err)
	}
}

func TestPushWorker_FailedCopyFollowsEnforced(t *testing.T) {
	j := finishedJob()
	j.Status = model.JobStatusFailed
	repo := &fakePushRepo{job: j, devices: []model.Device{{Token: "a", Language: "en"}}}
	s := &fakeSender{}
	enforced := true
	w := NewPushWorker(repo, s, func() bool { return enforced }, true)
	_ = w.ProcessTask(context.Background(), pushTask(t))
	if s.sent["a"].Body != "Your song could not be made, your credits were refunded" {
		t.Errorf("body = %q", s.sent["a"].Body)
	}
	enforced = false
	_ = w.ProcessTask(context.Background(), pushTask(t))
	if s.sent["a"].Body != "Your song could not be made" {
		t.Errorf("body = %q", s.sent["a"].Body)
	}
}

type recordingEnqueuer struct {
	task *asynq.Task
	opts []asynq.Option
	err  error
}

func (r *recordingEnqueuer) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	r.task, r.opts = t, opts
	return nil, r.err
}

func TestEnqueuePush_QueuesTask(t *testing.T) {
	e := &recordingEnqueuer{}
	if err := EnqueuePush(e, "j9"); err != nil {
		t.Fatal(err)
	}
	if e.task.Type() != "bildirim:gonder" || string(e.task.Payload()) != `{"jobId":"j9"}` {
		t.Errorf("task = %s %s", e.task.Type(), e.task.Payload())
	}
	var queue string
	var retry int
	for _, o := range e.opts {
		switch o.Type() {
		case asynq.QueueOpt:
			queue = o.Value().(string)
		case asynq.MaxRetryOpt:
			retry = o.Value().(int)
		}
	}
	if queue != "bildirim" || retry != 3 {
		t.Errorf("queue=%q retry=%d", queue, retry)
	}
}
