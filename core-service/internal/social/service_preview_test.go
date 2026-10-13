package social

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// ttlRecordingStorage records the duration passed to GetSignedURL.
type ttlRecordingStorage struct {
	ttl time.Duration
	err error
}

func (*ttlRecordingStorage) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (*ttlRecordingStorage) Delete(context.Context, string) error { return nil }
func (r *ttlRecordingStorage) GetSignedURL(_ context.Context, key string, ttl time.Duration) (string, error) {
	r.ttl = ttl
	return "https://imzali.example/" + key + "?sure", r.err
}
func (*ttlRecordingStorage) URLFor(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, errors.New("the preview must not use URLFor")
}

func previewStore() *fakeStore {
	store := newFakeStore()
	store.previews = map[string]*PreviewRecord{
		"Ab3dEf9h": {
			Preview: Preview{Title: "Sarki", Owner: ProfileSummary{Username: "ali"}, DurationSec: 180,
				Waveform: []float64{0.5}, Genre: "pop"},
			OwnerID: userA, PreviewKey: "sosyal/" + pubX + "/onizleme.mp3",
		},
		"Zz99Zz99": {Preview: Preview{Title: "Onizlemesiz", Waveform: []float64{}}},
	}
	return store
}

func TestService_PreviewSignedForOneHour(t *testing.T) {
	recorder := &ttlRecordingStorage{}
	s := NewService(previewStore(), &fakeQueue{}, recorder, testR2())
	preview, err := s.Preview(context.Background(), "Ab3dEf9h")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.PreviewURL == nil || *preview.PreviewURL != "https://imzali.example/sosyal/"+pubX+"/onizleme.mp3?sure" {
		t.Fatalf("onizlemeUrl = %v", preview.PreviewURL)
	}
	if recorder.ttl != time.Hour {
		t.Fatalf("signature lifetime = %v, want 1 hour", recorder.ttl)
	}
	if preview.Title != "Sarki" || preview.DurationSec != 180 || preview.Genre != "pop" || preview.Owner.Username != "ali" {
		t.Fatalf("preview = %+v", preview)
	}

	missing, err := s.Preview(context.Background(), "Zz99Zz99")
	if err != nil || missing.PreviewURL != nil {
		t.Fatalf("publication without a preview = %+v, %v", missing, err)
	}

	recorder.err = errors.New("signature error")
	if _, err := s.Preview(context.Background(), "Ab3dEf9h"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("a signature error must be a server error: %v", err)
	}
}

func TestService_PreviewWithoutR2(t *testing.T) {
	s, _ := newTestService(previewStore())
	preview, err := s.Preview(context.Background(), "Ab3dEf9h")
	if err != nil || preview.PreviewURL == nil || !strings.HasSuffix(*preview.PreviewURL, "/sosyal/"+pubX+"/onizleme.mp3") {
		t.Fatalf("URL without R2 = %+v, %v", preview, err)
	}
}

func TestService_PreviewNotFound(t *testing.T) {
	s, _ := newTestService(previewStore())
	if _, err := s.Preview(context.Background(), "Yok12345"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown slug: %v, want ErrNotFound", err)
	}

	// An invalid slug is a 404 without a query: the store returns a different error
	// on every call, so a call result would be distinguishable from ErrNotFound.
	store := previewStore()
	store.err = errors.New("the store must not be called")
	s, _ = newTestService(store)
	for _, slug := range []string{"kisa", "Ab3dEf9h/", "../../xx", ""} {
		if _, err := s.Preview(context.Background(), slug); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v, want ErrNotFound (without a store call)", slug, err)
		}
	}
}
