package worker

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/social"
)

const (
	socialTestPubID   = "9a1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	socialTestOwnerID = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

type fakePreparationStore struct {
	record       *social.PreparationRecord
	readErr      error
	published    *social.PreparationResult
	markedFailed bool
}

func (d *fakePreparationStore) GetPreparation(context.Context, string) (*social.PreparationRecord, error) {
	if d.readErr != nil {
		return nil, d.readErr
	}
	if d.record == nil {
		return nil, social.ErrNotFound
	}
	return d.record, nil
}

func (d *fakePreparationStore) Publish(_ context.Context, _ string, s social.PreparationResult) error {
	d.published = &s
	return nil
}

func (d *fakePreparationStore) MarkFailed(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err // the real pool does not run queries on a dead context either
	}
	d.markedFailed = true
	return nil
}

type copyCall struct{ src, dst string }

type fakeSocialCopier struct {
	copies []copyCall
	err    error
}

func (c *fakeSocialCopier) Copy(_ context.Context, src, dst string) error {
	if c.err != nil {
		return c.err
	}
	c.copies = append(c.copies, copyCall{src, dst})
	return nil
}

type fakeAnalyzer struct {
	req  *client.AnalysisRequest
	resp *client.AnalysisResponse
	err  error
}

func (a *fakeAnalyzer) Analyze(_ context.Context, r *client.AnalysisRequest) (*client.AnalysisResponse, error) {
	a.req = r
	return a.resp, a.err
}

func fillPeaks(n int, value float64) []float64 {
	p := make([]float64, n)
	for i := range p {
		p[i] = value
	}
	return p
}

func preparingRecord() *social.PreparationRecord {
	return &social.PreparationRecord{
		ID: socialTestPubID, OwnerID: socialTestOwnerID, Status: social.StatusPreparing,
		Preparation: social.Preparation{
			MasterKey: "masters/" + socialTestOwnerID + "/p1/m1.wav",
			Stems: []social.PreparationStem{
				{ID: "drums", Instrument: "drums", Duration: 184, Key: "stems/" + socialTestOwnerID + "/p1/drums.mp3"},
				{ID: "bass", Instrument: "bass", Duration: 184, Key: "stems/" + socialTestOwnerID + "/p1/bass.mp3"},
			},
		},
	}
}

type socialSetup struct {
	store    *fakePreparationStore
	copier   *fakeSocialCopier
	analyzer *fakeAnalyzer
	w        *SocialWorker
}

func newSocialTestWorker(record *social.PreparationRecord, checker deletionmark.Checker) *socialSetup {
	setup := &socialSetup{
		store:    &fakePreparationStore{record: record},
		copier:   &fakeSocialCopier{},
		analyzer: &fakeAnalyzer{resp: &client.AnalysisResponse{Peaks: fillPeaks(64, 0.5), Duration: 184}},
	}
	setup.w = NewSocialWorker(setup.store, setup.copier, setup.analyzer, nil, checker)
	return setup
}

func prepareTask() *asynq.Task {
	return asynq.NewTask(social.TaskTypePrepare, []byte(`{"yayinId":"`+socialTestPubID+`"}`))
}

func TestSocialWorker_CopiesAnalyzesPublishes(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), nil)
	if err := setup.w.Prepare(context.Background(), prepareTask()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	want := []copyCall{
		{"masters/" + socialTestOwnerID + "/p1/m1.wav", "sosyal/" + socialTestPubID + "/master.wav"},
		{"stems/" + socialTestOwnerID + "/p1/drums.mp3", "sosyal/" + socialTestPubID + "/stems/drums.mp3"},
		{"stems/" + socialTestOwnerID + "/p1/bass.mp3", "sosyal/" + socialTestPubID + "/stems/bass.mp3"},
	}
	if len(setup.copier.copies) != len(want) {
		t.Fatalf("kopyalar = %v", setup.copier.copies)
	}
	for i, b := range want {
		if setup.copier.copies[i] != b {
			t.Errorf("kopya %d = %v, beklenen %v", i, setup.copier.copies[i], b)
		}
	}
	if *setup.analyzer.req != (client.AnalysisRequest{InputKey: "sosyal/" + socialTestPubID + "/master.wav", PreviewKey: "sosyal/" + socialTestPubID + "/onizleme.mp3", PeakCount: 64, PreviewSeconds: 30}) {
		t.Fatalf("analiz istegi = %+v", *setup.analyzer.req)
	}
	s := setup.store.published
	if s == nil || s.MasterKey != "sosyal/"+socialTestPubID+"/master.wav" || len(s.Waveform) != 64 || s.PreviewKey != "sosyal/"+socialTestPubID+"/onizleme.mp3" {
		t.Fatalf("sonuc = %+v", s)
	}
	if len(s.Stems) != 2 || s.Stems[1] != (social.StemRecord{ID: "bass", Instrument: "bass", Duration: 184, Key: "sosyal/" + socialTestPubID + "/stems/bass.mp3"}) {
		t.Fatalf("stem listesi = %+v", s.Stems)
	}
	if setup.store.markedFailed {
		t.Fatal("basarili is hata yapmamali")
	}
}

func TestSocialWorker_SkipsMissingDeletedOrReady(t *testing.T) {
	deletedRecord := preparingRecord()
	deletedRecord.Deleted = true
	publishedRecord := preparingRecord()
	publishedRecord.Status = social.StatusPublished
	for name, record := range map[string]*social.PreparationRecord{"yok": nil, "silinmis": deletedRecord, "yayinda": publishedRecord} {
		setup := newSocialTestWorker(record, nil)
		if err := setup.w.Prepare(context.Background(), prepareTask()); err != nil {
			t.Errorf("%s: hata donmemeli: %v", name, err)
		}
		if len(setup.copier.copies) != 0 || setup.store.published != nil {
			t.Errorf("%s: hicbir sey yapilmamali", name)
		}
	}
}

func TestSocialWorker_DeletedOwnerNotCopiedMarkedFailed(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), &fakeChecker{answer: alwaysDeleted})
	err := setup.w.Prepare(context.Background(), prepareTask())
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu: %v", err)
	}
	if len(setup.copier.copies) != 0 || !setup.store.markedFailed || setup.store.published != nil {
		t.Fatalf("kopya=%d hata=%v", len(setup.copier.copies), setup.store.markedFailed)
	}
}

// If the account is deleted in the middle of the copies, the remaining copies are not made.
func TestSocialWorker_OwnerDeletedMidJob(t *testing.T) {
	deletedOnSecondCheck := func(n int) (bool, error) { return n >= 1, nil }
	setup := newSocialTestWorker(preparingRecord(), &fakeChecker{answer: deletedOnSecondCheck})
	if err := setup.w.Prepare(context.Background(), prepareTask()); !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu: %v", err)
	}
	if len(setup.copier.copies) != 1 || !setup.store.markedFailed {
		t.Fatalf("yalnizca master kopyalanmali: %v", setup.copier.copies)
	}
}

func TestSocialWorker_CheckErrorRetried(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), &fakeChecker{answer: func(int) (bool, error) { return false, errors.New("redis kapali") }})
	err := setup.w.Prepare(context.Background(), prepareTask())
	if err == nil || errors.Is(err, asynq.SkipRetry) || setup.store.markedFailed {
		t.Fatalf("tekrar denenebilir hata bekleniyordu: %v hata=%v", err, setup.store.markedFailed)
	}
}

func TestSocialWorker_CopyErrorOnLastAttemptMarksFailed(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), nil)
	setup.copier.err = errors.New("r2 kapali")
	if err := setup.w.Prepare(context.Background(), prepareTask()); err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("tekrar denenebilir hata bekleniyordu: %v", err)
	}
	if setup.store.markedFailed || setup.store.published != nil {
		t.Fatal("son deneme degilken hata durumuna alinmamali")
	}
	setup.w.isLastAttempt = func(context.Context) bool { return true }
	if err := setup.w.Prepare(context.Background(), prepareTask()); err == nil {
		t.Fatal("hata donmeli")
	}
	if !setup.store.markedFailed {
		t.Fatal("son denemede yayin hata olmali")
	}
}

// If the job's deadline expires on the last attempt (DeadlineExceeded), the publication is still marked failed.
func TestSocialWorker_DeadlineOnLastAttemptMarksFailed(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), nil)
	setup.copier.err = context.DeadlineExceeded
	setup.w.isLastAttempt = func(context.Context) bool { return true }
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := setup.w.Prepare(ctx, prepareTask()); err == nil {
		t.Fatal("hata donmeli")
	}
	if !setup.store.markedFailed {
		t.Fatal("suresi dolan son denemede yayin hata olmali")
	}
}

// On shutdown (Canceled) asynq requeues the job; the publication must not be marked failed.
func TestSocialWorker_CanceledJobNotMarkedFailed(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), nil)
	setup.copier.err = context.Canceled
	setup.w.isLastAttempt = func(context.Context) bool { return true }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := setup.w.Prepare(ctx, prepareTask()); !errors.Is(err, context.Canceled) {
		t.Fatalf("iptal hatasi donmeli: %v", err)
	}
	if setup.store.markedFailed {
		t.Fatal("iptal edilen is hata durumuna alinmamali")
	}
}

func TestSocialWorker_AnalyzeErrorDoesNotBlockPublish(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), nil)
	setup.analyzer.err = errors.New("audio-service kapali")
	if err := setup.w.Prepare(context.Background(), prepareTask()); err != nil {
		t.Fatalf("analiz hatasi isi bozmamali: %v", err)
	}
	s := setup.store.published
	if s == nil || s.Waveform == nil || len(s.Waveform) != 0 || s.PreviewKey != "" {
		t.Fatalf("bos dalga formu, onizlemesiz yayinlanmali: %+v", s)
	}
}

func TestSocialWorker_AnalyzeOutputValidated(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), nil)
	setup.analyzer.resp = &client.AnalysisResponse{Peaks: fillPeaks(10, 0.5)}
	if err := setup.w.Prepare(context.Background(), prepareTask()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	if len(setup.store.published.Waveform) != 0 {
		t.Fatalf("64 olmayan tepe dizisi atilmali: %v", setup.store.published.Waveform)
	}

	setup = newSocialTestWorker(preparingRecord(), nil)
	p := fillPeaks(64, 0.5)
	p[0], p[1], p[2] = -0.2, 1.7, math.NaN()
	setup.analyzer.resp = &client.AnalysisResponse{Peaks: p}
	if err := setup.w.Prepare(context.Background(), prepareTask()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	d := setup.store.published.Waveform
	if d[0] != 0 || d[1] != 1 || d[2] != 0 || d[3] != 0.5 {
		t.Fatalf("degerler 0-1'e kenetlenmeli: %v", d[:4])
	}
}

func TestSocialWorker_WithoutR2AndAnalyzer(t *testing.T) {
	d := &fakePreparationStore{record: preparingRecord()}
	w := NewSocialWorker(d, nil, nil, nil, nil)
	if err := w.Prepare(context.Background(), prepareTask()); err != nil {
		t.Fatalf("Hazirla: %v", err)
	}
	if d.published == nil || d.published.MasterKey != "sosyal/"+socialTestPubID+"/master.wav" || len(d.published.Waveform) != 0 {
		t.Fatalf("sonuc = %+v", d.published)
	}
}

func TestSocialWorker_MalformedPayloadNotRetried(t *testing.T) {
	setup := newSocialTestWorker(preparingRecord(), nil)
	for _, payload := range []string{"{", `{"yayinId":"../x"}`} {
		if err := setup.w.Prepare(context.Background(), asynq.NewTask(social.TaskTypePrepare, []byte(payload))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: SkipRetry bekleniyordu: %v", payload, err)
		}
		if err := setup.w.DeleteFiles(context.Background(), asynq.NewTask(social.TaskTypeDeleteFiles, []byte(payload))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: DosyaSil SkipRetry bekleniyordu: %v", payload, err)
		}
	}
}

type fakeSocialDeleter struct {
	prefixes []string
	err      error
}

func (s *fakeSocialDeleter) DeletePrefix(_ context.Context, prefix string) (int, error) {
	s.prefixes = append(s.prefixes, prefix)
	return 1, s.err
}

func TestSocialWorker_DeleteFiles(t *testing.T) {
	task := asynq.NewTask(social.TaskTypeDeleteFiles, []byte(`{"yayinId":"`+socialTestPubID+`"}`))
	deletedRecord := preparingRecord()
	deletedRecord.Deleted = true

	cases := []struct {
		name        string
		record      *social.PreparationRecord
		wantDeleted bool
	}{
		{"silinmis", deletedRecord, true},
		{"satir yok", nil, true},
		{"canli", preparingRecord(), false},
	}
	for _, d := range cases {
		s := &fakeSocialDeleter{}
		w := NewSocialWorker(&fakePreparationStore{record: d.record}, nil, nil, s, nil)
		if err := w.DeleteFiles(context.Background(), task); err != nil {
			t.Fatalf("%s: %v", d.name, err)
		}
		if d.wantDeleted != (len(s.prefixes) == 1 && s.prefixes[0] == "sosyal/"+socialTestPubID+"/") {
			t.Errorf("%s: onekler = %v", d.name, s.prefixes)
		}
	}

	s := &fakeSocialDeleter{err: errors.New("r2 kapali")}
	w := NewSocialWorker(&fakePreparationStore{record: deletedRecord}, nil, nil, s, nil)
	if err := w.DeleteFiles(context.Background(), task); err == nil {
		t.Fatal("R2 hatasi tekrar denensin diye donmeli")
	}
	w = NewSocialWorker(&fakePreparationStore{readErr: errors.New("pg kapali")}, nil, nil, &fakeSocialDeleter{}, nil)
	if err := w.DeleteFiles(context.Background(), task); err == nil {
		t.Fatal("okuma hatasi donmeli")
	}
}
