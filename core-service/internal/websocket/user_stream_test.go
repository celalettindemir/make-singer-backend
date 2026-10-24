package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

const (
	jUUID1 = "11111111-1111-4111-8111-111111111111"
	jUUID2 = "22222222-2222-4222-8222-222222222222"
)

type fakeConn struct {
	in     chan []byte
	closed chan struct{}
	mu     sync.Mutex
	out    [][]byte
	wrote  chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{in: make(chan []byte, 8), closed: make(chan struct{}), wrote: make(chan struct{}, 64)}
}

func (f *fakeConn) ReadMessage() (int, []byte, error) {
	select {
	case m := <-f.in:
		return 1, m, nil
	case <-f.closed:
		return 0, nil, errors.New("closed")
	}
}

func (f *fakeConn) WriteMessage(typ int, data []byte) error {
	if typ != 1 {
		return nil
	}
	f.mu.Lock()
	f.out = append(f.out, append([]byte(nil), data...))
	f.mu.Unlock()
	f.wrote <- struct{}{}
	return nil
}

func (f *fakeConn) SetReadDeadline(time.Time) error { return nil }

func (f *fakeConn) messages() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.out...)
}

// waitFor blocks until n text messages have been written.
func (f *fakeConn) waitFor(t *testing.T, n int) [][]byte {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for len(f.messages()) < n {
		select {
		case <-f.wrote:
		case <-deadline:
			t.Fatalf("got %d messages, want %d: %s", len(f.messages()), n, f.messages())
		}
	}
	return f.messages()
}

func seqsOf(t *testing.T, msgs [][]byte) []int64 {
	t.Helper()
	var out []int64
	for _, m := range msgs {
		var f struct {
			Type  string `json:"type"`
			Event struct {
				Seq int64 `json:"seq"`
			} `json:"event"`
		}
		if err := json.Unmarshal(m, &f); err != nil || f.Type != "event" {
			t.Fatalf("bad frame %s: %v", m, err)
		}
		out = append(out, f.Event.Seq)
	}
	return out
}

func newJob(t *testing.T, repo jobs.Repo, id, owner string) {
	t.Helper()
	if err := repo.Create(context.Background(), &model.Job{ID: id, OwnerID: owner, Status: model.JobStatusQueued}); err != nil {
		t.Fatal(err)
	}
}

// addProgress appends events 2..n to the job.
func addProgress(t *testing.T, repo jobs.Repo, id string, n int) {
	t.Helper()
	for i := 1; i < n; i++ {
		p := i
		err := repo.Update(context.Background(), id, func(j *model.Job) error {
			j.Status = model.JobStatusRunning
			j.Progress = p
			j.CurrentStep = "s"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// hookRepo runs after once after the first EventsAfter call.
type hookRepo struct {
	jobs.Repo
	once  sync.Once
	after func()
}

func (h *hookRepo) EventsAfter(ctx context.Context, userID, id string, seq int64, limit int) ([]jobs.Event, error) {
	evs, err := h.Repo.EventsAfter(ctx, userID, id, seq, limit)
	h.once.Do(h.after)
	return evs, err
}

func TestUserStream_ReplayThenLiveNoGapNoDuplicate(t *testing.T) {
	store := jobs.NewMemoryStore(nil)
	newJob(t, store, jUUID1, "u1")
	addProgress(t, store, jUUID1, 3) // seq 1..3

	var streams *UserStreams
	repo := &hookRepo{Repo: store}
	streams = NewUserStreams(repo)
	repo.after = func() {
		// Seq 4 is committed and published while the replay runs. The replay
		// read already happened, so only the live buffer carries it; a
		// duplicate of seq 3 is delivered too and must be dropped.
		evs, _ := store.EventsAfter(context.Background(), "u1", jUUID1, 2, 10)
		streams.Deliver(evs[0]) // seq 3 again
		streams.Deliver(jobs.Event{JobID: jUUID1, OwnerID: "u1", Seq: 4, Kind: jobs.KindProgress,
			Data: json.RawMessage(`{}`), At: time.Now()})
	}

	c := newFakeConn()
	c.in <- ([]byte(`{"type":"resume","jobs":{"` + jUUID1 + `":1}}`))
	go streams.serve(c, "u1")
	got := seqsOf(t, c.waitFor(t, 3))
	if len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("seqs = %v, want [2 3 4]", got)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(c.messages()); n != 3 {
		t.Errorf("%d messages, want exactly 3", n)
	}
	close(c.closed)
}

func TestUserStream_EventFrameMatchesContract(t *testing.T) {
	streams := NewUserStreams(jobs.NewMemoryStore(nil))
	c := newFakeConn()
	go streams.serve(c, "u1")
	c.in <- []byte(`{"type":"ping"}`)
	c.waitFor(t, 1)
	streams.Deliver(jobs.Event{JobID: "j1", OwnerID: "u1", Seq: 5, Kind: jobs.KindLine,
		Data: json.RawMessage(`{"section":1,"text":"x"}`), At: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)})
	msgs := c.waitFor(t, 2)
	if string(msgs[0]) != `{"type":"pong"}` {
		t.Errorf("pong = %s", msgs[0])
	}
	want := `{"type":"event","event":{"jobId":"j1","seq":5,"kind":"lyrics.line","data":{"section":1,"text":"x"},"at":"2026-10-24T09:00:00Z"}}`
	if string(msgs[1]) != want {
		t.Errorf("frame = %s\nwant    %s", msgs[1], want)
	}
	close(c.closed)
}

func TestUserStream_OtherUsersEventsNotDelivered(t *testing.T) {
	store := jobs.NewMemoryStore(nil)
	newJob(t, store, jUUID2, "u2")
	streams := NewUserStreams(store)

	c := newFakeConn()
	// Resuming another user's job replays nothing.
	c.in <- []byte(`{"type":"resume","jobs":{"` + jUUID2 + `":0}}`)
	go streams.serve(c, "u1")
	time.Sleep(100 * time.Millisecond)
	streams.Deliver(jobs.Event{JobID: jUUID2, OwnerID: "u2", Seq: 1, Kind: jobs.KindQueued, Data: json.RawMessage(`{}`)})
	streams.Deliver(jobs.Event{JobID: "j-mine", OwnerID: "u1", Seq: 1, Kind: jobs.KindQueued, Data: json.RawMessage(`{}`)})
	msgs := c.waitFor(t, 1)
	time.Sleep(100 * time.Millisecond)
	msgs = c.messages()
	if len(msgs) != 1 || seqsOf(t, msgs)[0] != 1 {
		t.Fatalf("messages = %s, want only u1's event", msgs)
	}
	var f eventFrame
	_ = json.Unmarshal(msgs[0], &f)
	if f.Event.JobID != "j-mine" {
		t.Errorf("delivered job %s", f.Event.JobID)
	}
	close(c.closed)
}
