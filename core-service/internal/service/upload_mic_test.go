package service

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/model"
)

// metaStorage is a fakeStorage that also records the object metadata.
type metaStorage struct {
	fakeStorage
	meta     map[string]string
	withMeta int
}

func (m *metaStorage) UploadWithMetadata(ctx context.Context, key string, body io.Reader, contentType string, meta map[string]string) (string, time.Time, error) {
	m.withMeta++
	m.meta = meta
	return m.Upload(ctx, key, body, contentType)
}

func TestUploadService_MicMetadata(t *testing.T) {
	st := &metaStorage{}
	s := NewUploadService(st, testR2Cfg())
	_, err := s.UploadVocal(context.Background(), "u", "p1", "s1", "t", nil, 0,
		model.MicInfo{Kind: "bluetooth", Name: "AirPods Pro (Çağrı)"})
	if err != nil {
		t.Fatal(err)
	}
	if st.meta["mic-kind"] != "bluetooth" {
		t.Errorf("mic-kind = %q", st.meta["mic-kind"])
	}
	if want := "AirPods%20Pro%20%28%C3%87a%C4%9Fr%C4%B1%29"; st.meta["mic-name"] != want {
		t.Errorf("mic-name = %q, want %q", st.meta["mic-name"], want)
	}
}

func TestUploadService_MicMetadataOnlyWhenPresent(t *testing.T) {
	st := &metaStorage{}
	s := NewUploadService(st, testR2Cfg())
	if _, err := s.UploadVocal(context.Background(), "u", "p1", "s1", "t", nil, 0, model.MicInfo{}); err != nil {
		t.Fatal(err)
	}
	if st.withMeta != 0 || len(st.meta) != 0 {
		t.Errorf("no metadata expected, got %v (calls %d)", st.meta, st.withMeta)
	}
	if len(st.uploaded) != 1 {
		t.Fatalf("plain Upload must be used, uploaded=%v", st.uploaded)
	}

	st2 := &metaStorage{}
	s2 := NewUploadService(st2, testR2Cfg())
	if _, err := s2.UploadVocal(context.Background(), "u", "p1", "s1", "t", nil, 0, model.MicInfo{Kind: "usb"}); err != nil {
		t.Fatal(err)
	}
	if _, has := st2.meta["mic-name"]; has || st2.meta["mic-kind"] != "usb" {
		t.Errorf("meta = %v", st2.meta)
	}
}

func TestUploadService_MicMock(t *testing.T) {
	s := NewUploadService(nil, testR2Cfg())
	if _, err := s.UploadVocal(context.Background(), "u", "p1", "s1", "t", nil, 0, model.MicInfo{Kind: "usb"}); err != nil {
		t.Fatal(err)
	}
}
