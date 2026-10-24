package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/jobs"
)

const (
	resumeWait   = 10 * time.Second
	pingInterval = 30 * time.Second
	replayPage   = 500
	liveBuffer   = 256
	// maxResumeJobs caps the jobs one resume message may name.
	maxResumeJobs = 50
	// maxClientMessage is the read limit of a client frame.
	maxClientMessage = 64 * 1024
	// writeWait is the deadline of one frame write.
	writeWait = 10 * time.Second
)

// streamConn is the part of *websocket.Conn the stream uses.
type streamConn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(messageType int, data []byte) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

// UserStreams serves /ws/me: each connection receives the job events of one
// user, replayed from the store after a resume message and then live.
type UserStreams struct {
	repo jobs.Repo

	mu   sync.Mutex
	subs map[string]map[*userSub]struct{} // by owner id
}

type userSub struct {
	buf      chan jobs.Event
	overflow chan struct{} // closed when buf filled up; the client must reconnect and resume
	once     sync.Once
}

func NewUserStreams(repo jobs.Repo) *UserStreams {
	return &UserStreams{repo: repo, subs: make(map[string]map[*userSub]struct{})}
}

// Deliver hands a bus event to the owner's connections on this pod. It never
// blocks: a connection that cannot keep up is closed and catches up by replay.
func (u *UserStreams) Deliver(e jobs.Event) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for s := range u.subs[e.OwnerID] {
		select {
		case s.buf <- e:
		default:
			s.once.Do(func() { close(s.overflow) })
		}
	}
}

func (u *UserStreams) add(userID string) *userSub {
	s := &userSub{buf: make(chan jobs.Event, liveBuffer), overflow: make(chan struct{})}
	u.mu.Lock()
	if u.subs[userID] == nil {
		u.subs[userID] = make(map[*userSub]struct{})
	}
	u.subs[userID][s] = struct{}{}
	u.mu.Unlock()
	return s
}

func (u *UserStreams) remove(userID string, s *userSub) {
	u.mu.Lock()
	delete(u.subs[userID], s)
	if len(u.subs[userID]) == 0 {
		delete(u.subs, userID)
	}
	u.mu.Unlock()
}

// Handle runs one /ws/me connection until it closes.
func (u *UserStreams) Handle(c *websocket.Conn, userID string) {
	c.SetReadLimit(maxClientMessage)
	u.serve(c, userID)
}

type clientMessage struct {
	Type string           `json:"type"`
	Jobs map[string]int64 `json:"jobs"`
}

// wireEvent is the JobEvent contract shape (no ownerId).
type wireEvent struct {
	JobID string          `json:"jobId"`
	Seq   int64           `json:"seq"`
	Kind  string          `json:"kind"`
	Data  json.RawMessage `json:"data"`
	At    time.Time       `json:"at"`
}

type eventFrame struct {
	Type  string    `json:"type"`
	Event wireEvent `json:"event"`
}

func encodeEvent(e jobs.Event) []byte {
	data := e.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	raw, _ := json.Marshal(eventFrame{Type: "event", Event: wireEvent{
		JobID: e.JobID, Seq: e.Seq, Kind: e.Kind, Data: data, At: e.At,
	}})
	return raw
}

func (u *UserStreams) serve(c streamConn, userID string) {
	// Register first so nothing published during replay is lost.
	sub := u.add(userID)
	defer u.remove(userID, sub)

	var writeMu sync.Mutex
	writeType := func(typ int, b []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = c.SetWriteDeadline(time.Now().Add(writeWait))
		return c.WriteMessage(typ, b)
	}
	write := func(b []byte) error { return writeType(websocket.TextMessage, b) }
	pong := func() error { return write([]byte(`{"type":"pong"}`)) }

	// The resume message may be the first thing the client sends; a client
	// that sends nothing (or something else) within 10 s just goes live.
	sent := map[string]int64{}
	_ = c.SetReadDeadline(time.Now().Add(resumeWait))
	if _, raw, err := c.ReadMessage(); err == nil {
		var m clientMessage
		if json.Unmarshal(raw, &m) == nil {
			switch m.Type {
			case "resume":
				u.replay(userID, sanitizeResume(m.Jobs), sent, write)
			case "ping":
				_ = pong()
			}
		}
	} else if !isTimeout(err) {
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		u.live(sub, c, sent, write, writeType)
	}()

	// Reader: pings until the client goes away.
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			break
		}
		var m clientMessage
		if json.Unmarshal(raw, &m) == nil && m.Type == "ping" {
			if pong() != nil {
				break
			}
		}
	}
	// Stop the live goroutine.
	sub.once.Do(func() { close(sub.overflow) })
	<-done
}

// sanitizeResume keeps only job ids that are UUIDs and at most maxResumeJobs
// of them; the rest is ignored.
func sanitizeResume(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for id, seq := range in {
		if len(out) == maxResumeJobs {
			break
		}
		if _, err := uuid.Parse(id); err == nil {
			out[id] = seq
		}
	}
	return out
}

// replay sends, per job, the stored events after the client's last seq.
func (u *UserStreams) replay(userID string, jobsSeq map[string]int64, sent map[string]int64, write func([]byte) error) {
	for jobID, last := range jobsSeq {
		if last < 0 {
			last = 0
		}
		sent[jobID] = last
		for {
			evs, err := u.repo.EventsAfter(context.Background(), userID, jobID, sent[jobID], replayPage)
			if err != nil {
				if !errors.Is(err, jobs.ErrJobNotFound) {
					log.Printf("Warning: replay of job %s failed: %v", jobID, err)
				}
				break
			}
			for _, e := range evs {
				if write(encodeEvent(e)) != nil {
					return
				}
				sent[jobID] = e.Seq
			}
			if len(evs) < replayPage {
				break
			}
		}
	}
}

// live drains the buffer, dropping what replay already sent, and pings.
func (u *UserStreams) live(sub *userSub, c streamConn, sent map[string]int64, write func([]byte) error, writeType func(int, []byte) error) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case e := <-sub.buf:
			if e.Seq <= sent[e.JobID] {
				continue
			}
			if write(encodeEvent(e)) != nil {
				return
			}
			sent[e.JobID] = e.Seq
		case <-ticker.C:
			if writeType(websocket.PingMessage, nil) != nil {
				return
			}
		case <-sub.overflow:
			_ = writeType(websocket.CloseMessage, []byte{})
			// The client must reconnect and resume; closing also ends the reader.
			_ = c.Close()
			return
		}
	}
}

func isTimeout(err error) bool {
	var ne interface{ Timeout() bool }
	return errors.As(err, &ne) && ne.Timeout()
}
