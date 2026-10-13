package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/model"
)

type fakeSuno struct {
	generateErr, waitErr, splitErr error
	track                          *client.Track
	stems                          []client.Stem
	generateReq                    *client.GenerateMusicRequest
	generateCalls                  int
	stemWaitErr                    error
	splitArgs                      [2]string
	splitCalls                     int

	// focus mode
	isolateArgs     [3]string
	isolateCalls    int
	isolateErr      error
	isolated        *client.IsolatedStem
	isolatedWaitErr error
}

func (s *fakeSuno) GenerateMusic(_ context.Context, r *client.GenerateMusicRequest) (string, error) {
	s.generateCalls++
	s.generateReq = r
	return "t1", s.generateErr
}
func (s *fakeSuno) WaitMusic(context.Context, string, time.Duration, time.Duration) (*client.Track, error) {
	return s.track, s.waitErr
}
func (s *fakeSuno) SplitStems(_ context.Context, taskID, audioID string) (string, error) {
	s.splitCalls++
	s.splitArgs = [2]string{taskID, audioID}
	return "s1", s.splitErr
}
func (s *fakeSuno) WaitStems(context.Context, string, time.Duration, time.Duration) ([]client.Stem, error) {
	return s.stems, s.stemWaitErr
}
func (s *fakeSuno) IsolateStem(_ context.Context, taskID, audioID, stemName string) (string, error) {
	s.isolateCalls++
	s.isolateArgs = [3]string{taskID, audioID, stemName}
	return "i1", s.isolateErr
}
func (s *fakeSuno) WaitIsolated(context.Context, string, time.Duration, time.Duration) (*client.IsolatedStem, error) {
	return s.isolated, s.isolatedWaitErr
}
func (s *fakeSuno) IsConfigured() bool { return true }

type uploadCall struct {
	key, contentType string
	data             []byte
}

type fakeStorage struct{ uploads []uploadCall }

func (d *fakeStorage) Upload(_ context.Context, key string, body io.Reader, contentType string) (string, time.Time, error) {
	b, _ := io.ReadAll(body)
	d.uploads = append(d.uploads, uploadCall{key, contentType, b})
	return "https://depo.test/" + key, time.Time{}, nil
}
func (d *fakeStorage) Delete(context.Context, string) error { return nil }
func (d *fakeStorage) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (d *fakeStorage) URLFor(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func audioServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/yok.mp3" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ses:" + r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testPayload() *model.RenderJobPayload {
	return &model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", StemMode: model.StemModeStems, Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{"x"}}}
}

func testWorker(s *fakeSuno, d client.StorageClient) *RenderWorker {
	return workerWithChecker(s, d, nil)
}

func workerWithChecker(s *fakeSuno, d client.StorageClient, h deletionmark.Checker) *RenderWorker {
	w := NewRenderWorker(nil, s, d, nil, h)
	w.pollInterval = time.Millisecond
	return w
}

func noProgress(int, string) {}

func TestRenderWorker_SunoHappyPath(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{
		track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 180},
		stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
			{Instrument: model.InstrumentPiano, URL: srv.URL + "/piyano.mp3"},
		},
	}
	d := &fakeStorage{}

	res, err := testWorker(s, d).generateWithSuno(context.Background(), testPayload(), noProgress)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if !s.generateReq.Instrumental || !strings.HasPrefix(s.generateReq.Style, "pop, instrumental") {
		t.Errorf("uretim istegi = %+v", s.generateReq)
	}
	if s.splitArgs != [2]string{"t1", "a1"} {
		t.Errorf("SplitStems argumanlari = %v", s.splitArgs)
	}
	if res.Duration != 180 || len(res.Stems) != 2 {
		t.Fatalf("sonuc = %+v", res)
	}
	if len(d.uploads) != 2 {
		t.Fatalf("yukleme sayisi = %d, 2 bekleniyordu", len(d.uploads))
	}
	for i, y := range d.uploads {
		st := res.Stems[i]
		if y.key != st.FileKey || !strings.HasPrefix(y.key, "stems/kullanici-a/p1/") || !strings.HasSuffix(y.key, ".mp3") {
			t.Errorf("anahtar = %q, kayit %q", y.key, st.FileKey)
		}
		if y.contentType != "audio/mpeg" {
			t.Errorf("icerik tipi = %q", y.contentType)
		}
		if st.Duration != 180 {
			t.Errorf("stem suresi = %v", st.Duration)
		}
	}
	if !bytes.Equal(d.uploads[0].data, []byte("ses:/davul.mp3")) || res.Stems[1].Instrument != model.InstrumentPiano {
		t.Errorf("yuklenen icerik/enstruman yanlis: %q, %v", d.uploads[0].data, res.Stems[1].Instrument)
	}
}

func TestRenderWorker_SunoGenerateRejectionIsRetryable(t *testing.T) {
	s := &fakeSuno{generateErr: errors.New("kredi yok")}
	_, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), testPayload(), noProgress)
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ucret oncesi hata tekrar denenebilir olmali, geldi: %v", err)
	}
}

func TestRenderWorker_SunoNoRetryAfterPaidStep(t *testing.T) {
	srv := audioServer(t)
	cases := map[string]*fakeSuno{
		"bekleme hatasi":      {waitErr: errors.New("zaman asimi")},
		"bolme hatasi":        {track: &client.Track{ID: "a1"}, splitErr: errors.New("reddedildi")},
		"stem bekleme hatasi": {track: &client.Track{ID: "a1"}, stemWaitErr: errors.New("zaman asimi")},
		"stem yok":            {track: &client.Track{ID: "a1"}},
		"indirme 404": {track: &client.Track{ID: "a1"}, stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/yok.mp3"},
		}},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			d := &fakeStorage{}
			_, err := testWorker(s, d).generateWithSuno(context.Background(), testPayload(), noProgress)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
			}
			if len(d.uploads) != 0 {
				t.Errorf("basarisiz iste yukleme olmamali: %d", len(d.uploads))
			}
		})
	}
}

func TestRenderWorker_SunoNotCalledWithoutStorage(t *testing.T) {
	s := &fakeSuno{}
	_, err := testWorker(s, nil).generateWithSuno(context.Background(), testPayload(), noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if s.generateCalls != 0 {
		t.Errorf("depo yokken Suno %d kez cagrildi", s.generateCalls)
	}
}

func TestRenderWorker_SunoEstimatesMissingDuration(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{
		track: &client.Track{ID: "a1", Duration: 0},
		stems: []client.Stem{{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"}},
	}
	p := testPayload()
	bpm := 120
	p.Brief.BPM.Value = &bpm
	p.Brief.Structure = []model.SongSection{{ID: "s1", Type: "verse", Bars: 8}}

	res, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), p, noProgress)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if res.Duration != 16 || len(res.Stems) != 1 || res.Stems[0].Duration != 16 {
		t.Fatalf("sure = %v, stem = %+v; 16 bekleniyordu", res.Duration, res.Stems)
	}
}

func TestRenderWorker_SunoNotCalledForOwnerlessJob(t *testing.T) {
	s := &fakeSuno{}
	p := testPayload()
	p.OwnerID = ""
	_, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), p, noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if s.generateCalls != 0 {
		t.Errorf("sahipsiz iste Suno %d kez cagrildi", s.generateCalls)
	}
}

// panickingStorage panics on upload (for example a typed-nil R2 client).
type panickingStorage struct{ fakeStorage }

func (d *panickingStorage) Upload(context.Context, string, io.Reader, string) (string, time.Time, error) {
	panic("nil R2 istemcisi")
}

// TestRenderWorker_SunoPanicAfterChargeNotRetried: asynq catches a panic and
// RETRIES the job, which charges Suno again (12 credits were wasted this way
// in the live test). A panic after the paid step must be SkipRetry.
func TestRenderWorker_SunoPanicAfterChargeNotRetried(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{
		track: &client.Track{ID: "a1", Duration: 30},
		stems: []client.Stem{{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"}},
	}
	_, err := testWorker(s, &panickingStorage{}).generateWithSuno(context.Background(), testPayload(), noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}

func TestRenderWorker_SunoMixModeDoesNotSplit(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 120}}
	d := &fakeStorage{}
	p := testPayload()
	p.StemMode = model.StemModeMix

	res, err := testWorker(s, d).generateWithSuno(context.Background(), p, noProgress)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.splitCalls != 0 || s.isolateCalls != 0 {
		t.Errorf("mix modunda ayirma cagrildi: split=%d advanced=%d", s.splitCalls, s.isolateCalls)
	}
	if res.StemMode != model.StemModeMix || len(res.Stems) != 1 || res.Stems[0].Instrument != model.InstrumentMix {
		t.Fatalf("sonuc = %+v", res)
	}
	if len(d.uploads) != 1 || !bytes.Equal(d.uploads[0].data, []byte("ses:/tam.mp3")) || d.uploads[0].contentType != "audio/mpeg" {
		t.Errorf("yukleme = %+v", d.uploads)
	}
	if !strings.HasPrefix(d.uploads[0].key, "stems/kullanici-a/p1/") {
		t.Errorf("anahtar = %q", d.uploads[0].key)
	}
}

func TestRenderWorker_SunoEmptyModeActsAsMix(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 120}}
	p := testPayload()
	p.StemMode = ""
	res, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), p, noProgress)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.splitCalls != 0 || res.StemMode != model.StemModeMix {
		t.Errorf("bos mod mix gibi islenmeli: split=%d mod=%q", s.splitCalls, res.StemMode)
	}
}

func TestRenderWorker_SunoMixModeMissingTrackURLNoRetry(t *testing.T) {
	s := &fakeSuno{track: &client.Track{ID: "a1", Duration: 120}}
	p := testPayload()
	p.StemMode = model.StemModeMix
	_, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), p, noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}

func TestRenderWorker_SunoFocusModeTwoStems(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{
		track:    &client.Track{ID: "a1", Duration: 141},
		isolated: &client.IsolatedStem{ExtractURL: srv.URL + "/davul.mp3", RemoveURL: srv.URL + "/kalan.mp3", Duration: 141},
	}
	d := &fakeStorage{}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	drums := model.InstrumentDrums
	p.FocusInstrument = &drums

	res, err := testWorker(s, d).generateWithSuno(context.Background(), p, noProgress)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.isolateArgs != [3]string{"t1", "a1", "Drum Kit"} || s.splitCalls != 0 {
		t.Errorf("ayirma argumanlari = %v, split=%d", s.isolateArgs, s.splitCalls)
	}
	if res.StemMode != model.StemModeFocus || len(res.Stems) != 2 {
		t.Fatalf("sonuc = %+v", res)
	}
	if res.Stems[0].Instrument != model.InstrumentDrums || res.Stems[1].Instrument != model.InstrumentRest {
		t.Errorf("enstrumanlar = %v, %v", res.Stems[0].Instrument, res.Stems[1].Instrument)
	}
	if len(d.uploads) != 2 || !bytes.Equal(d.uploads[0].data, []byte("ses:/davul.mp3")) ||
		!bytes.Equal(d.uploads[1].data, []byte("ses:/kalan.mp3")) {
		t.Errorf("yuklemeler = %+v", d.uploads)
	}
}

func TestRenderWorker_SunoFocusIsolateErrorSkipsRetry(t *testing.T) {
	s := &fakeSuno{
		track:      &client.Track{ID: "a1", Duration: 141},
		isolateErr: errors.New("ayirma reddedildi"),
	}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	drums := model.InstrumentDrums
	p.FocusInstrument = &drums
	_, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), p, noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}

func TestRenderWorker_SunoFocusUnmappedInstrumentNotCalled(t *testing.T) {
	s := &fakeSuno{}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	p.FocusInstrument = nil
	_, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), p, noProgress)
	if !errors.Is(err, asynq.SkipRetry) || s.generateCalls != 0 {
		t.Fatalf("ucret oncesi SkipRetry ve sifir uretim bekleniyordu: err=%v uretim=%d", err, s.generateCalls)
	}
}

func TestRenderWorker_SunoFocusIsolateWaitErrorNoRetry(t *testing.T) {
	s := &fakeSuno{track: &client.Track{ID: "a1"}, isolatedWaitErr: errors.New("katalog eslesmesi")}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	drums := model.InstrumentDrums
	p.FocusInstrument = &drums
	_, err := testWorker(s, &fakeStorage{}).generateWithSuno(context.Background(), p, noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}

// fakeChecker decides with a function what the n-th check (from 0) returns,
// which simulates the account being deleted in the middle of the job.
type fakeChecker struct {
	answer func(n int) (bool, error)
	asked  []string
}

func (d *fakeChecker) IsDeleted(_ context.Context, userID string) (bool, error) {
	n := len(d.asked)
	d.asked = append(d.asked, userID)
	return d.answer(n)
}

func alwaysDeleted(int) (bool, error) { return true, nil }

func TestRenderWorker_SunoNotCalledForDeletedOwner(t *testing.T) {
	s := &fakeSuno{}
	h := &fakeChecker{answer: alwaysDeleted}
	d := &fakeStorage{}
	_, err := workerWithChecker(s, d, h).generateWithSuno(context.Background(), testPayload(), noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if s.generateCalls != 0 || len(d.uploads) != 0 {
		t.Fatalf("silinmis sahip icin uretim=%d yukleme=%d", s.generateCalls, len(d.uploads))
	}
	if len(h.asked) == 0 || h.asked[0] != "kullanici-a" {
		t.Fatalf("is sahibi denetlenmedi: %v", h.asked)
	}
}

func TestRenderWorker_SunoCheckErrorBeforeChargeIsRetryable(t *testing.T) {
	s := &fakeSuno{}
	h := &fakeChecker{answer: func(int) (bool, error) { return false, errors.New("redis kapali") }}
	_, err := workerWithChecker(s, &fakeStorage{}, h).generateWithSuno(context.Background(), testPayload(), noProgress)
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ucret oncesi denetim hatasi tekrar denenebilir olmali, geldi: %v", err)
	}
	if s.generateCalls != 0 {
		t.Fatalf("denetim hatasinda Suno %d kez cagrildi", s.generateCalls)
	}
}

// If the account is deleted after the paid call, no stem is uploaded and
// asynq does not charge the job again.
func TestRenderWorker_SunoNoUploadForOwnerDeletedAfterCharge(t *testing.T) {
	srv := audioServer(t)
	cases := map[string]func(n int) (bool, error){
		"silindi": func(n int) (bool, error) { return n > 0, nil },
		"denetim hatasi": func(n int) (bool, error) {
			if n > 0 {
				return false, errors.New("redis kapali")
			}
			return false, nil
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			s := &fakeSuno{
				track: &client.Track{ID: "a1", Duration: 30},
				stems: []client.Stem{
					{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
					{Instrument: model.InstrumentPiano, URL: srv.URL + "/piyano.mp3"},
				},
			}
			d := &fakeStorage{}
			_, err := workerWithChecker(s, d, &fakeChecker{answer: answer}).generateWithSuno(context.Background(), testPayload(), noProgress)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
			}
			if s.generateCalls != 1 || len(d.uploads) != 0 {
				t.Fatalf("uretim=%d yukleme=%d; 1 ve 0 bekleniyordu", s.generateCalls, len(d.uploads))
			}
		})
	}
}

// If deleted before the second stem, the second upload is not made: the
// check is repeated before every upload.
func TestRenderWorker_SunoChecksBeforeEveryUpload(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{
		track: &client.Track{ID: "a1", Duration: 30},
		stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
			{Instrument: model.InstrumentPiano, URL: srv.URL + "/piyano.mp3"},
		},
	}
	d := &fakeStorage{}
	// 0: before the charge, 1: before the first upload, 2: before the second upload
	h := &fakeChecker{answer: func(n int) (bool, error) { return n >= 2, nil }}
	_, err := workerWithChecker(s, d, h).generateWithSuno(context.Background(), testPayload(), noProgress)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if len(d.uploads) != 1 {
		t.Fatalf("yukleme sayisi = %d, 1 bekleniyordu", len(d.uploads))
	}
}

func TestRenderWorker_SunoChecksEveryStepForLiveOwner(t *testing.T) {
	srv := audioServer(t)
	s := &fakeSuno{
		track: &client.Track{ID: "a1", Duration: 30},
		stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
			{Instrument: model.InstrumentPiano, URL: srv.URL + "/piyano.mp3"},
		},
	}
	d := &fakeStorage{}
	h := &fakeChecker{answer: func(int) (bool, error) { return false, nil }}
	if _, err := workerWithChecker(s, d, h).generateWithSuno(context.Background(), testPayload(), noProgress); err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if len(d.uploads) != 2 || len(h.asked) != 3 {
		t.Fatalf("yukleme=%d denetim=%d; 2 ve 3 bekleniyordu", len(d.uploads), len(h.asked))
	}
}
