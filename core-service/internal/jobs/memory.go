package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/model"
)

// MemoryStore is an in-memory Repo for other packages' tests; it follows the
// PostgresStore semantics.
type MemoryStore struct {
	mu         sync.Mutex
	pub        Publisher
	jobs       map[string]*model.Job
	events     map[string][]Event
	onFinished []func(ctx context.Context, job model.Job)
}

var _ Repo = (*MemoryStore)(nil)

// NewMemoryStore returns an empty store; pub may be nil.
func NewMemoryStore(pub Publisher) *MemoryStore {
	return &MemoryStore{pub: pub, jobs: map[string]*model.Job{}, events: map[string][]Event{}}
}

// OnFinished registers a hook called when a job's status became succeeded or failed.
func (m *MemoryStore) OnFinished(fn func(ctx context.Context, job model.Job)) {
	m.onFinished = append(m.onFinished, fn)
}

func cloneJob(j *model.Job) *model.Job {
	c := *j
	c.Payload = append([]byte(nil), j.Payload...)
	c.Result = append([]byte(nil), j.Result...)
	if j.Payload == nil {
		c.Payload = nil
	}
	if j.Result == nil {
		c.Result = nil
	}
	if j.Error != nil {
		e := *j.Error
		c.Error = &e
	}
	return &c
}

// appendEvents must be called with m.mu held.
func (m *MemoryStore) appendEvents(job *model.Job, ds []Derived) ([]Event, error) {
	out := make([]Event, 0, len(ds))
	now := time.Now().UTC()
	for _, d := range ds {
		data, err := json.Marshal(d.Data)
		if err != nil {
			return nil, err
		}
		job.LastSeq++
		e := Event{JobID: job.ID, OwnerID: job.OwnerID, Seq: job.LastSeq, Kind: d.Kind, Data: data, At: now}
		m.events[job.ID] = append(m.events[job.ID], e)
		out = append(out, e)
	}
	return out, nil
}

func (m *MemoryStore) publish(ctx context.Context, events []Event) {
	if m.pub != nil && len(events) > 0 {
		m.pub.Publish(ctx, events)
	}
}

func (m *MemoryStore) Create(ctx context.Context, job *model.Job) error {
	m.mu.Lock()
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(job.ID); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("job id %q is not a uuid", job.ID)
	}
	if job.Status == "" {
		job.Status = model.JobStatusQueued
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.Type == model.JobTypeLyrics {
		for _, o := range m.jobs {
			if o.Type == model.JobTypeLyrics && o.OwnerID == job.OwnerID && o.ProjectID == job.ProjectID &&
				(o.Status == model.JobStatusQueued || o.Status == model.JobStatusRunning) {
				m.mu.Unlock()
				return &activeJobError{jobID: o.ID}
			}
		}
	}
	stored := cloneJob(job)
	stored.LastSeq = 0
	events, err := m.appendEvents(stored, []Derived{{Kind: KindQueued, Data: map[string]any{}}})
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.jobs[stored.ID] = stored
	job.LastSeq = stored.LastSeq
	m.mu.Unlock()
	m.publish(ctx, events)
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*model.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	return cloneJob(j), nil
}

func (m *MemoryStore) GetOwned(ctx context.Context, userID, id string) (*model.Job, error) {
	j, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !j.IsOwnedBy(userID) {
		return nil, ErrJobNotFound
	}
	return j, nil
}

func (m *MemoryStore) Update(ctx context.Context, id string, fn func(*model.Job) error) error {
	return m.UpdateWith(ctx, id, func(j *model.Job, _ Emitter) error { return fn(j) })
}

func (m *MemoryStore) UpdateWith(ctx context.Context, id string, fn func(*model.Job, Emitter) error) error {
	m.mu.Lock()
	stored, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return ErrJobNotFound
	}
	job := cloneJob(stored)
	before := *cloneJob(stored)
	em := &emitter{}
	if err := fn(job, em); err != nil {
		m.mu.Unlock()
		return err
	}
	finishDefaults(&before, job)
	events, err := m.appendEvents(job, append(DeriveEvents(before, *job), em.out...))
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.jobs[id] = job
	result := *cloneJob(job)
	m.mu.Unlock()
	m.publish(ctx, events)
	if finishedNow(before, result) {
		for _, h := range m.onFinished {
			h(ctx, result)
		}
	}
	return nil
}

func (m *MemoryStore) List(_ context.Context, userID string, since time.Time, limit int) ([]model.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Job{}
	for _, j := range m.jobs {
		if j.OwnerID == userID && !j.CreatedAt.Before(since) {
			out = append(out, *cloneJob(j))
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) EventsAfter(ctx context.Context, userID, id string, afterSeq int64, limit int) ([]Event, error) {
	if _, err := m.GetOwned(ctx, userID, id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Event{}
	for _, e := range m.events[id] {
		if e.Seq > afterSeq && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) Sweep(_ context.Context, now time.Time) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var jobsN, eventsN int64
	for id, j := range m.jobs {
		if j.CompletedAt != nil && j.CompletedAt.Before(now.Add(-EventRetention)) {
			eventsN += int64(len(m.events[id]))
			delete(m.events, id)
		}
		if j.CreatedAt.Add(ResultRetention).Before(now) {
			jobsN++
			delete(m.jobs, id)
			delete(m.events, id)
		}
	}
	return jobsN, eventsN, nil
}

func (m *MemoryStore) DeleteOwner(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		if j.OwnerID == userID {
			delete(m.jobs, id)
			delete(m.events, id)
		}
	}
	return nil
}
