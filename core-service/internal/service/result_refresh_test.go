package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// fakeStorage produces a DIFFERENT URL on every URLFor call. That way the
// question "is the stored URL returned or a freshly generated one" gets a
// definite answer.
type fakeStorage struct {
	calls       int
	uploaded    []string
	urlForCount int
}

func (d *fakeStorage) Upload(ctx context.Context, key string, body io.Reader, contentType string) (string, time.Time, error) {
	d.uploaded = append(d.uploaded, key)
	u, e, err := d.URLFor(ctx, key)
	return u, e, err
}

func (d *fakeStorage) Delete(ctx context.Context, key string) error { return nil }

func (d *fakeStorage) GetSignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "", nil
}

func (d *fakeStorage) URLFor(ctx context.Context, key string) (string, time.Time, error) {
	d.calls++
	d.urlForCount++
	return fmt.Sprintf("https://makeasinger-private.hesap123.r2.cloudflarestorage.com/%s?X-Amz-Signature=taze%d", key, d.calls),
		time.Now().Add(time.Hour), nil
}

func testR2Cfg() *config.R2Config {
	return &config.R2Config{
		AccountID:     "hesap123",
		PublicBucket:  "makeasinger-public",
		PrivateBucket: "makeasinger-private",
		PublicURL:     "https://makesinger-cdn.celalettindemir.dev",
		PresignTTL:    time.Hour,
	}
}

// staleRecordBytes imitates the record a faulty version wrote to Redis: it
// holds a frozen signed URL and an expiresAt left in the PAST. The new code
// must read fileKey and regenerate the URL, not use this dead URL.
func staleRecordBytes(t *testing.T) []byte {
	t.Helper()
	past := time.Now().Add(-3 * time.Hour)
	raw := map[string]any{
		"fileKey":   "masters/p1/m1.wav",
		"fileUrl":   "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/p1/m1.wav?X-Amz-Signature=DONMUS",
		"expiresAt": past.Format(time.RFC3339Nano),
		"duration":  180.5,
		"profile":   "clean",
		"peakDb":    -0.3,
		"lufs":      -14,
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestMasterService_BuildResultRefreshesURLOnEveryRead(t *testing.T) {
	storage := &fakeStorage{}
	s := NewMasterService(nil, nil, storage, testR2Cfg())

	resp, err := s.BuildResult(context.Background(), staleRecordBytes(t))
	if err != nil {
		t.Fatalf("BuildResult: %v", err)
	}

	if resp.FileURL == "" || resp.FileURL == "https://makeasinger-private.hesap123.r2.cloudflarestorage.com/masters/p1/m1.wav?X-Amz-Signature=DONMUS" {
		t.Fatalf("the stored frozen URL was returned: %q", resp.FileURL)
	}
	if resp.ExpiresAt == nil {
		t.Fatal("ExpiresAt nil")
	}
	if !resp.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt is in the past: %v (the stored value may have been frozen)", resp.ExpiresAt)
	}
	if resp.Duration != 180.5 || resp.LUFS != -14 {
		t.Errorf("the measurements were lost: %+v", resp)
	}

	// The second read must again produce a NEW URL.
	resp2, err := s.BuildResult(context.Background(), staleRecordBytes(t))
	if err != nil {
		t.Fatalf("second BuildResult: %v", err)
	}
	if resp2.FileURL == resp.FileURL {
		t.Errorf("the same URL was returned on the second read, the signing was not repeated: %q", resp2.FileURL)
	}
}

func TestMasterService_BuildResultFailsWithoutKey(t *testing.T) {
	s := NewMasterService(nil, nil, &fakeStorage{}, testR2Cfg())
	if _, err := s.BuildResult(context.Background(), []byte(`{"duration":1}`)); err == nil {
		t.Error("an error was expected for a record without a key")
	}
}

func TestRenderService_BuildResultRefreshesStemURLs(t *testing.T) {
	storage := &fakeStorage{}
	s := NewRenderService(nil, nil, storage, testR2Cfg())

	record := &model.RenderResultRecord{
		ID:  "r1",
		BPM: 120,
		Stems: []model.StemResultRecord{
			{ID: "s1", Instrument: model.Instrument("drums"), FileKey: "stems/p1/s1.wav", Duration: 10},
		},
	}

	resp, err := s.BuildResult(context.Background(), record)
	if err != nil {
		t.Fatalf("BuildResult: %v", err)
	}
	if len(resp.Stems) != 1 {
		t.Fatalf("stem count = %d", len(resp.Stems))
	}
	if resp.Stems[0].FileURL == "" {
		t.Fatal("stem URL is empty")
	}
	if resp.Stems[0].ExpiresAt == nil || !resp.Stems[0].ExpiresAt.After(time.Now()) {
		t.Errorf("stem expiresAt is not fresh: %v", resp.Stems[0].ExpiresAt)
	}

	resp2, err := s.BuildResult(context.Background(), record)
	if err != nil {
		t.Fatalf("second BuildResult: %v", err)
	}
	if resp2.Stems[0].FileURL == resp.Stems[0].FileURL {
		t.Errorf("the stem URL was not regenerated: %q", resp2.Stems[0].FileURL)
	}
}

// TestRenderService_BuildResultRecordHasNoSignature: when the record is
// serialized it must NOT contain a signed URL.
func TestRenderService_BuildResultRecordHasNoSignature(t *testing.T) {
	record := &model.RenderResultRecord{
		Stems: []model.StemResultRecord{{ID: "s1", FileKey: "stems/p1/s1.wav"}},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); strings.Contains(got, "X-Amz-Signature") || strings.Contains(got, "fileUrl") {
		t.Errorf("the record has a signed URL: %s", got)
	}
}

// TestUploadService_SignsOnce: Upload already returns the URL; calling
// URLFor separately would have meant a second (wasted) signing.
func TestUploadService_SignsOnce(t *testing.T) {
	storage := &fakeStorage{}
	s := NewUploadService(storage, &config.R2Config{})

	resp, err := s.UploadVocal(context.Background(), "kullanici-a", "p1", "s1", "take1", nil, 0, model.MicInfo{})
	if err != nil {
		t.Fatalf("UploadVocal: %v", err)
	}
	if resp.FileURL == "" {
		t.Fatal("URL is empty")
	}
	if storage.urlForCount != 1 {
		t.Errorf("URLFor was called %d times, want 1 (the single signing inside Upload)", storage.urlForCount)
	}
	if resp.ExpiresAt == nil {
		t.Error("expiresAt expected for a private vocal")
	}
}

// TestUploadService_MockURLResolvesWithKeyFromURL verifies that, while
// r2Client is not configured (mock path), the URL UploadVocal produces can be
// resolved back to a key with KeyFromURL. This URL used to be the fixed
// "https://cdn.makeasinger.com/..." and, being an unknown host, KeyFromURL
// failed on a later master request ("unknown host").
func TestUploadService_MockURLResolvesWithKeyFromURL(t *testing.T) {
	r2Cfg := &config.R2Config{
		PrivateBucket: "makeasinger-private",
		AccountID:     "hesap123",
	}
	s := NewUploadService(nil, r2Cfg)

	resp, err := s.UploadVocal(context.Background(), "kullanici-a", "p1", "s1", "take1", nil, 0, model.MicInfo{})
	if err != nil {
		t.Fatalf("UploadVocal: %v", err)
	}

	wantKey := fmt.Sprintf("vocals/kullanici-a/p1/s1/%s.wav", resp.ID)
	gotKey, err := client.KeyFromURL(resp.FileURL, r2Cfg)
	if err != nil {
		t.Fatalf("could not resolve the mock URL with KeyFromURL: %v (URL: %s)", err, resp.FileURL)
	}
	if gotKey != wantKey {
		t.Errorf("resolved key = %q, want %q", gotKey, wantKey)
	}
}

// TestUploadService_GetSignedURLMockResolvesWithKeyFromURL verifies that the
// mock path of GetSignedURL also produces a URL that resolves under the same rules.
func TestUploadService_GetSignedURLMockResolvesWithKeyFromURL(t *testing.T) {
	r2Cfg := &config.R2Config{
		PrivateBucket: "makeasinger-private",
		AccountID:     "hesap123",
	}
	s := NewUploadService(nil, r2Cfg)

	key := "vocals/p1/take1.wav"
	url, err := s.GetSignedURL(context.Background(), key, time.Hour)
	if err != nil {
		t.Fatalf("GetSignedURL: %v", err)
	}

	gotKey, err := client.KeyFromURL(url, r2Cfg)
	if err != nil {
		t.Fatalf("could not resolve the mock URL with KeyFromURL: %v (URL: %s)", err, url)
	}
	if gotKey != key {
		t.Errorf("resolved key = %q, want %q", gotKey, key)
	}
}

func TestRenderService_BuildResultCarriesStemMode(t *testing.T) {
	s := NewRenderService(nil, nil, &fakeStorage{}, testR2Cfg())
	record := &model.RenderResultRecord{
		StemMode: model.StemModeMix,
		Stems:    []model.StemResultRecord{{ID: "s1", Instrument: model.InstrumentMix, FileKey: "stems/kullanici-a/p1/s1.mp3"}},
	}
	resp, err := s.BuildResult(context.Background(), record)
	if err != nil {
		t.Fatalf("BuildResult: %v", err)
	}
	if resp.StemMode != model.StemModeMix {
		t.Errorf("response stemMode = %q", resp.StemMode)
	}
}
