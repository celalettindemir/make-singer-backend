package worker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

const (
	impJobID = "9d2b6f3e-41a7-4c58-b0e1-7a5c3d8f2e64"
	impOwner = "u1"
	impSrc   = "imports/u1/" + impJobID + "/source.mp3"
	impPref  = "imports/u1/" + impJobID + "/"
)

type impStorage struct {
	fakeStorage
	deleted []string
}

func (s *impStorage) Delete(_ context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	return nil
}

type impSeparator struct {
	reqs  []*client.SeparateRequest
	err   error
	stems []client.SeparatedStem
}

func (s *impSeparator) IsConfigured() bool { return true }
func (s *impSeparator) Separate(_ context.Context, r *client.SeparateRequest) (*client.SeparateResponse, error) {
	s.reqs = append(s.reqs, r)
	if s.err != nil {
		return nil, s.err
	}
	return &client.SeparateResponse{DurationMs: 60000, Stems: s.stems}, nil
}

type impAudio struct {
	client.ImportAudio
	measureErr, notesErr error
	notesReqs            []*client.ImportNotesRequest
	bpm                  *float64
}

func (a *impAudio) IsConfigured() bool { return true }
func (a *impAudio) MeasureImport(context.Context, *client.ImportMeasureRequest) (*client.ImportMeasureResponse, error) {
	if a.measureErr != nil {
		return nil, a.measureErr
	}
	return &client.ImportMeasureResponse{BPM: a.bpm, Key: &model.KeyResult{Tonic: "D", Scale: "minor"}}, nil
}
func (a *impAudio) ImportNotes(_ context.Context, r *client.ImportNotesRequest) (*client.ImportNotesResponse, error) {
	a.notesReqs = append(a.notesReqs, r)
	if a.notesErr != nil {
		return nil, a.notesErr
	}
	return &client.ImportNotesResponse{}, nil
}

type impTranscriber struct {
	err error
	tr  *client.TimedTranscription
}

func (t *impTranscriber) IsConfigured() bool { return true }
func (t *impTranscriber) TranscribeTimed(context.Context, []byte, string) (*client.TimedTranscription, error) {
	return t.tr, t.err
}

type impEnv struct {
	w       *ImportWorker
	repo    jobs.Repo
	st      *impStorage
	sep     *impSeparator
	audio   *impAudio
	tr      *impTranscriber
	sp      *credit.FakeSpender
	cleaned []string
	last    bool
	deleted bool
}

type impDeleted struct{ e *impEnv }

func (d impDeleted) IsDeleted(context.Context, string) (bool, error) { return d.e.deleted, nil }

func newImpEnv(t *testing.T, status model.JobStatus) *impEnv {
	t.Helper()
	bpm := 120.0
	e := &impEnv{
		repo: jobs.NewMemoryStore(nil), st: &impStorage{},
		sep: &impSeparator{stems: []client.SeparatedStem{
			{Instrument: "rest", Key: impPref + "rest.mp3"}, {Instrument: "bass", Key: impPref + "bass.mp3"},
			{Instrument: "vocal", Key: impPref + "vocal.mp3"}, {Instrument: "drums", Key: impPref + "drums.mp3"},
		}},
		audio: &impAudio{bpm: &bpm},
		tr: &impTranscriber{tr: &client.TimedTranscription{
			Segments: []client.TimedSegment{{Text: "Gece boyunca", Start: 5, End: 8, AvgLogprob: -0.2}},
			Words:    []client.TimedWord{{Word: "Gece", Start: 5, End: 6}, {Word: "boyunca", Start: 6, End: 8}},
		}},
		sp: &credit.FakeSpender{IsEnforced: true, Plus: true},
	}
	payload, _ := json.Marshal(model.ImportPayload{SourceKey: impSrc, DurationMs: 60000, ContentType: "audio/mpeg"})
	job := &model.Job{ID: impJobID, Type: model.JobTypeImport, OwnerID: impOwner, ProjectID: "p1",
		ProjectName: "Yaz", Status: model.JobStatusQueued, Payload: payload, CreatedAt: time.Now()}
	if err := e.repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if status != model.JobStatusQueued {
		_ = e.repo.Update(context.Background(), impJobID, func(j *model.Job) error { j.Status = status; return nil })
	}
	e.w = NewImportWorker(ImportWorkerDeps{
		Repo: e.repo, Storage: e.st, Separator: e.sep, Audio: e.audio, Transcriber: e.tr, Credits: e.sp,
		Deletion: impDeleted{e},
		ScheduleCleanup: func(owner, key string) error {
			e.cleaned = append(e.cleaned, owner+"|"+key)
			return nil
		},
		TranscribeTimeout: time.Second,
		IsLastAttempt:     func(context.Context) bool { return e.last },
		Fetch:             func(context.Context, string) ([]byte, error) { return []byte("mp3"), nil },
	})
	return e
}

func (e *impEnv) run() error {
	p, _ := json.Marshal(service.ImportTaskPayload{JobID: impJobID})
	return e.w.ProcessTask(context.Background(), asynq.NewTask(service.TaskTypeImport, p))
}

func (e *impEnv) job(t *testing.T) *model.Job {
	t.Helper()
	j, err := e.repo.Get(context.Background(), impJobID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *impEnv) record(t *testing.T) model.ImportRecord {
	t.Helper()
	var rec model.ImportRecord
	if err := json.Unmarshal(e.job(t).Result, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestImportWorker_HappyPath(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	if err := e.run(); err != nil {
		t.Fatal(err)
	}
	j := e.job(t)
	if j.Status != model.JobStatusSucceeded || j.Progress != 100 || j.CompletedAt == nil {
		t.Fatalf("job = %+v", j)
	}
	// Steps and progress in order, through job events.
	evs, _ := e.repo.EventsAfter(context.Background(), impOwner, impJobID, 0, 100)
	var got []string
	for _, ev := range evs {
		if ev.Kind == jobs.KindProgress {
			var d struct {
				Progress int    `json:"progress"`
				Step     string `json:"step"`
			}
			_ = json.Unmarshal(ev.Data, &d)
			got = append(got, d.Step+":"+itoa(d.Progress))
		}
	}
	want := []string{"separating:10", "analyzing:60", "transcribing:70", "notes:90"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("progress events = %v, want %v", got, want)
	}
	if len(e.sep.reqs) != 1 || e.sep.reqs[0].OutputPrefix != impPref || e.sep.reqs[0].InputKey != impSrc {
		t.Fatalf("separate reqs = %+v", e.sep.reqs)
	}
	if len(e.audio.notesReqs) != 1 {
		t.Fatalf("notes reqs = %d", len(e.audio.notesReqs))
	}
	nr := e.audio.notesReqs[0]
	if nr.VocalKey != impPref+"vocal.mp3" || nr.NotesKey != impPref+"notes.json" || nr.BPM != 120 || len(nr.Words) != 2 || !nr.Words[0].Success {
		t.Fatalf("notes req = %+v", nr)
	}
	rec := e.record(t)
	var order []string
	for _, s := range rec.Stems {
		order = append(order, s.ID)
		if s.Duration != 60 {
			t.Fatalf("stem duration = %v", s.Duration)
		}
	}
	if !reflect.DeepEqual(order, []string{"vocal", "drums", "bass", "rest"}) {
		t.Fatalf("stem order = %v", order)
	}
	if rec.Guide == nil || rec.Guide.NotesKey == nil || *rec.Guide.NotesKey != impPref+"notes.json" || rec.LyricTimeline == nil {
		t.Fatalf("record = %+v", rec)
	}
	if !reflect.DeepEqual(e.st.deleted, []string{impSrc}) {
		t.Fatalf("deleted = %v", e.st.deleted)
	}
	if len(e.sp.Refunds) != 0 || len(e.cleaned) != 0 {
		t.Fatalf("refunds %v cleaned %v", e.sp.Refunds, e.cleaned)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestImportWorker_TranscriptionFails_SucceedsWithoutLyrics(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.tr.err = errors.New("whisper down")
	if err := e.run(); err != nil {
		t.Fatal(err)
	}
	j := e.job(t)
	rec := e.record(t)
	if j.Status != model.JobStatusSucceeded || rec.LyricTimeline != nil || rec.Guide != nil {
		t.Fatalf("job %v record %+v", j.Status, rec)
	}
	if len(rec.Structure) != 1 || rec.Structure[0].Type != "verse" || len(rec.Lyrics) != 1 || len(rec.Lyrics[0].Lines) != 0 {
		t.Fatalf("structure %+v lyrics %+v", rec.Structure, rec.Lyrics)
	}
	if len(e.sp.Refunds) != 0 || len(e.audio.notesReqs) != 0 {
		t.Fatalf("refunds %v notes %d", e.sp.Refunds, len(e.audio.notesReqs))
	}
}

func TestImportWorker_NoTranscriber_SucceedsWithoutLyrics(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.w.d.Transcriber = nil
	if err := e.run(); err != nil {
		t.Fatal(err)
	}
	if rec := e.record(t); e.job(t).Status != model.JobStatusSucceeded || rec.LyricTimeline != nil || rec.Guide != nil {
		t.Fatalf("record %+v", rec)
	}
}

func TestImportWorker_NotesOnlyForPlus(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.sp.Plus = false
	if err := e.run(); err != nil {
		t.Fatal(err)
	}
	if len(e.audio.notesReqs) != 0 || e.record(t).Guide != nil || e.job(t).Status != model.JobStatusSucceeded {
		t.Fatalf("notes %d guide %+v", len(e.audio.notesReqs), e.record(t).Guide)
	}
}

func TestImportWorker_NotesFail_GuideNil(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.audio.notesErr = errors.New("boom")
	if err := e.run(); err != nil {
		t.Fatal(err)
	}
	rec := e.record(t)
	if e.job(t).Status != model.JobStatusSucceeded || rec.Guide != nil || rec.LyricTimeline == nil {
		t.Fatalf("record %+v", rec)
	}
	if len(e.sp.Refunds) != 0 {
		t.Fatalf("refunds %v", e.sp.Refunds)
	}
}

func TestImportWorker_SeparationFails_LastAttempt_RefundsAndSchedulesCleanup(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.sep.err = errors.New("demucs down")
	e.last = true
	if err := e.run(); err == nil {
		t.Fatal("expected the error")
	}
	j := e.job(t)
	if j.Status != model.JobStatusFailed || j.ErrorCode != "IMPORT_FAILED" || j.CompletedAt == nil {
		t.Fatalf("job = %+v", j)
	}
	if len(e.sp.Refunds) != 1 || e.sp.Refunds[0].Op != credit.OpImport || e.sp.Refunds[0].ID != impJobID {
		t.Fatalf("refunds = %+v", e.sp.Refunds)
	}
	if !reflect.DeepEqual(e.cleaned, []string{impOwner + "|" + impSrc}) {
		t.Fatalf("cleaned = %v", e.cleaned)
	}
	if len(e.st.deleted) != 0 {
		t.Fatalf("source deleted: %v", e.st.deleted)
	}
}

func TestImportWorker_MeasureFails_LastAttempt_Fails(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.audio.measureErr = errors.New("bad audio")
	e.last = true
	if err := e.run(); err == nil {
		t.Fatal("expected the error")
	}
	if j := e.job(t); j.Status != model.JobStatusFailed || len(e.sp.Refunds) != 1 {
		t.Fatalf("job %v refunds %v", j.Status, e.sp.Refunds)
	}
}

func TestImportWorker_SeparationFails_NotLastAttempt_LeavesJob(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.sep.err = errors.New("demucs down")
	if err := e.run(); err == nil {
		t.Fatal("expected the error so asynq retries")
	}
	if j := e.job(t); j.Status != model.JobStatusRunning || j.ErrorCode != "" {
		t.Fatalf("job = %+v", j)
	}
	if len(e.sp.Refunds) != 0 || len(e.cleaned) != 0 {
		t.Fatalf("refunds %v cleaned %v", e.sp.Refunds, e.cleaned)
	}
}

func TestImportWorker_TerminalJob_NoOp(t *testing.T) {
	for _, st := range []model.JobStatus{model.JobStatusCanceled, model.JobStatusSucceeded, model.JobStatusFailed} {
		e := newImpEnv(t, st)
		if err := e.run(); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		if len(e.sep.reqs) != 0 || len(e.sp.Refunds) != 0 || e.job(t).Status != st {
			t.Fatalf("%s: touched the job", st)
		}
	}
}

func TestImportWorker_DeletedOwner_SkipsRetryNoRefund(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.deleted = true
	err := e.run()
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("err = %v", err)
	}
	if j := e.job(t); j.Status != model.JobStatusFailed || j.ErrorCode != "IMPORT_FAILED" {
		t.Fatalf("job = %+v", j)
	}
	if len(e.sep.reqs) != 0 || len(e.sp.Refunds) != 0 {
		t.Fatalf("separated %d refunds %v", len(e.sep.reqs), e.sp.Refunds)
	}
}

func TestImportWorker_CanceledDuringRun_NoResultNoRefund(t *testing.T) {
	e := newImpEnv(t, model.JobStatusQueued)
	e.audio.bpm = nil
	// Cancel while the (fake) transcriber runs.
	e.w.d.Fetch = func(ctx context.Context, _ string) ([]byte, error) {
		_ = e.repo.Update(ctx, impJobID, func(j *model.Job) error { j.Status = model.JobStatusCanceled; return nil })
		return []byte("mp3"), nil
	}
	if err := e.run(); err != nil {
		t.Fatal(err)
	}
	if j := e.job(t); j.Status != model.JobStatusCanceled || len(j.Result) != 0 {
		t.Fatalf("job = %+v", j)
	}
	if len(e.st.deleted) != 0 || len(e.sp.Refunds) != 0 {
		t.Fatalf("deleted %v refunds %v", e.st.deleted, e.sp.Refunds)
	}
}
