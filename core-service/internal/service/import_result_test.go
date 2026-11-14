package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/jobs"
	"github.com/makeasinger/api/internal/model"
)

func TestJobCenter_DetailImport_SignsURLs(t *testing.T) {
	repo := jobs.NewMemoryStore(nil)
	notes := "imports/u1/" + cancelImportID + "/notes.json"
	rec := model.ImportRecord{
		DurationMs: 214300, BPM: nil,
		Structure: []model.ImportSection{{ID: "s1", Type: "verse", Bars: 8}},
		Lyrics:    []model.ImportLyricsSection{{SectionID: "s1", Lines: []string{}}},
		Stems: []model.ImportStemRecord{
			{ID: "vocal", Instrument: "vocal", Key: "imports/u1/j/vocal.mp3", Duration: 214.3},
			{ID: "drums", Instrument: "drums", Key: "imports/u1/j/drums.mp3", Duration: 214.3},
		},
		Guide: &model.GuideRecord{NotesKey: &notes},
	}
	raw, _ := json.Marshal(rec)
	job := &model.Job{ID: cancelImportID, Type: model.JobTypeImport, OwnerID: "u1", Status: model.JobStatusQueued,
		CreatedAt: time.Now()}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	_ = repo.Update(context.Background(), cancelImportID, func(j *model.Job) error {
		j.Status, j.Result = model.JobStatusSucceeded, raw
		return nil
	})

	center := NewJobCenter(repo, nil, nil).WithImportStorage(&fakeStorage{})
	d, err := center.Detail(context.Background(), "u1", cancelImportID)
	if err != nil {
		t.Fatal(err)
	}
	var res model.ImportResult
	if err := json.Unmarshal(d.Result, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Stems) != 2 || res.Stems[0].ID != "vocal" || res.Stems[0].Duration != 214.3 || res.Stems[0].WaveformData != nil {
		t.Fatalf("stems = %+v", res.Stems)
	}
	for _, s := range res.Stems {
		if !strings.Contains(s.FileURL, "X-Amz-Signature=") || strings.Contains(s.FileURL, "?X-Amz-Signature=taze0") {
			t.Fatalf("stem url not signed: %q", s.FileURL)
		}
	}
	if res.Guide == nil || res.Guide.NotesURL == nil || !strings.Contains(*res.Guide.NotesURL, "notes.json?X-Amz-Signature=") {
		t.Fatalf("guide = %+v", res.Guide)
	}
	if res.LyricTimeline != nil || res.BPM != nil {
		t.Fatalf("timeline/bpm = %+v %v", res.LyricTimeline, res.BPM)
	}
	// Signed again on every call.
	d2, _ := center.Detail(context.Background(), "u1", cancelImportID)
	if string(d2.Result) == string(d.Result) {
		t.Fatal("URLs were not signed again")
	}
	// Keys never leak.
	if strings.Contains(string(d.Result), `"key":"imports`) || strings.Contains(string(d.Result), "notesKey") {
		t.Fatalf("result leaks keys: %s", d.Result)
	}
}
