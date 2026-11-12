package service

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

type humStorage struct {
	fakeStorage
	mu      sync.Mutex
	deletes []string
}

func (h *humStorage) Upload(ctx context.Context, key string, body io.Reader, ct string) (string, time.Time, error) {
	_, _ = io.Copy(io.Discard, body)
	return h.fakeStorage.Upload(ctx, key, strings.NewReader(""), ct)
}

func (h *humStorage) Delete(ctx context.Context, key string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deletes = append(h.deletes, key)
	return nil
}

type fakePreparer struct {
	durationMs int
	err        error
	in, out    string
}

func (f *fakePreparer) PrepareHum(ctx context.Context, req *client.HumPrepareRequest) (*client.HumPrepareResponse, error) {
	f.in, f.out = req.InputKey, req.OutputKey
	if f.err != nil {
		return nil, f.err
	}
	return &client.HumPrepareResponse{DurationMs: f.durationMs}, nil
}

type fakeTranscriber struct {
	text    string
	err     error
	block   bool
	calls   int
	gotLang string
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, audio []byte, filename, language string) (*client.Transcription, error) {
	f.calls++
	f.gotLang = language
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return &client.Transcription{Text: f.text}, nil
}
func (f *fakeTranscriber) IsConfigured() bool { return true }

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestHumService_Upload_StoresMp3AndTranscript(t *testing.T) {
	st := &humStorage{}
	prep := &fakePreparer{durationMs: 12600}
	tr := &fakeTranscriber{text: "Oy şeftaşım, şeftaşım. Sarı sarı gözlerine kurban olmuşum"}
	s := NewHumService(st, testR2Cfg(), prep, tr, time.Second)
	resp, err := s.Upload(context.Background(), "u1", "p1", model.LanguageTR, "audio/wav", []byte("RIFF"))
	if err != nil {
		t.Fatal(err)
	}
	if !uuidRe.MatchString(resp.HumID) {
		t.Fatalf("humId = %q", resp.HumID)
	}
	src := "hums/u1/p1/" + resp.HumID + "-src.wav"
	mp3 := "hums/u1/p1/" + resp.HumID + ".mp3"
	if len(st.uploaded) != 1 || st.uploaded[0] != src {
		t.Errorf("uploads = %v", st.uploaded)
	}
	if prep.in != src || prep.out != mp3 {
		t.Errorf("prepare %q -> %q", prep.in, prep.out)
	}
	if len(st.deletes) != 1 || st.deletes[0] != src {
		t.Errorf("deletes = %v", st.deletes)
	}
	if resp.DurationMs != 12600 {
		t.Errorf("durationMs = %d", resp.DurationMs)
	}
	if resp.Transcript == nil || len(resp.Transcript.Lines) != 2 {
		t.Fatalf("transcript = %+v", resp.Transcript)
	}
	if tr.gotLang != "tr" {
		t.Errorf("language = %q", tr.gotLang)
	}
}

func TestHumService_TranscriptionTimeout_NullTranscript(t *testing.T) {
	st := &humStorage{}
	s := NewHumService(st, testR2Cfg(), &fakePreparer{durationMs: 9000}, &fakeTranscriber{block: true}, 50*time.Millisecond)
	start := time.Now()
	resp, err := s.Upload(context.Background(), "u1", "p1", "", "audio/wav", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Transcript != nil {
		t.Errorf("transcript = %+v, want nil", resp.Transcript)
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestHumService_TranscriptionError_NullTranscript(t *testing.T) {
	s := NewHumService(&humStorage{}, testR2Cfg(), &fakePreparer{durationMs: 9000}, &fakeTranscriber{err: errors.New("boom")}, time.Second)
	resp, err := s.Upload(context.Background(), "u1", "p1", "", "audio/wav", []byte("x"))
	if err != nil || resp.Transcript != nil {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestHumService_NoWords_NullTranscript(t *testing.T) {
	s := NewHumService(&humStorage{}, testR2Cfg(), &fakePreparer{durationMs: 9000}, &fakeTranscriber{text: "ımm nanana eee"}, time.Second)
	resp, err := s.Upload(context.Background(), "u1", "p1", "tr", "audio/wav", []byte("x"))
	if err != nil || resp.Transcript != nil {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestHumService_DurationRejected_DeletesBoth(t *testing.T) {
	st := &humStorage{}
	tr := &fakeTranscriber{text: "hello world"}
	s := NewHumService(st, testR2Cfg(), &fakePreparer{durationMs: 3200}, tr, time.Second)
	_, err := s.Upload(context.Background(), "u1", "p1", "", "audio/wav", []byte("x"))
	var de *HumDurationError
	if !errors.As(err, &de) || de.DurationMs != 3200 {
		t.Fatalf("err = %v", err)
	}
	if len(st.deletes) != 2 || !strings.HasSuffix(st.deletes[0], "-src.wav") || !strings.HasSuffix(st.deletes[1], ".mp3") {
		t.Errorf("deletes = %v", st.deletes)
	}
	if tr.calls != 0 {
		t.Error("transcriber must not be called")
	}
}

func TestHumService_Unreadable_DeletesSource(t *testing.T) {
	st := &humStorage{}
	s := NewHumService(st, testR2Cfg(), &fakePreparer{err: client.ErrAudioUnprocessable}, nil, time.Second)
	_, err := s.Upload(context.Background(), "u1", "p1", "", "audio/wav", []byte("x"))
	if !errors.Is(err, ErrHumUnreadable) {
		t.Fatalf("err = %v", err)
	}
	if len(st.deletes) != 1 || !strings.HasSuffix(st.deletes[0], "-src.wav") {
		t.Errorf("deletes = %v", st.deletes)
	}
}

func TestHumService_NoPreparer_Unavailable(t *testing.T) {
	s := NewHumService(&humStorage{}, testR2Cfg(), nil, nil, time.Second)
	if _, err := s.Upload(context.Background(), "u1", "p1", "", "audio/wav", []byte("x")); !errors.Is(err, ErrHumUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestHumService_Mock(t *testing.T) {
	s := NewHumService(nil, testR2Cfg(), nil, nil, time.Second)
	resp, err := s.Upload(context.Background(), "u1", "p1", "", "audio/wav", []byte("x"))
	if err != nil || resp.DurationMs != 12000 || resp.Transcript != nil || !strings.Contains(resp.FileURL, "hums/u1/p1/"+resp.HumID+".mp3") {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestHumService_Delete_BuildsKey(t *testing.T) {
	st := &humStorage{}
	s := NewHumService(st, testR2Cfg(), nil, nil, time.Second)
	id := "0b8f2c1e-1111-4222-8333-444455556666"
	if err := s.Delete(context.Background(), "u1", "p1", id); err != nil {
		t.Fatal(err)
	}
	if len(st.deletes) != 1 || st.deletes[0] != "hums/u1/p1/"+id+".mp3" {
		t.Errorf("deletes = %v", st.deletes)
	}
	if err := s.Delete(context.Background(), "u1", "p1", "../x"); !errors.Is(err, client.ErrInvalidSegment) {
		t.Errorf("err = %v", err)
	}
}
