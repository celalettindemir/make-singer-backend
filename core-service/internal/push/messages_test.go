package push

import (
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func TestCompose_Copy(t *testing.T) {
	partial := []byte(`{"sections":[{"index":0,"status":"done","lines":["a"]},{"index":1,"status":"failed","lines":[]},{"index":2,"status":"failed","lines":[]}]}`)
	allDone := []byte(`{"sections":[{"index":0,"status":"done","lines":["a"]}]}`)
	tests := []struct {
		name     string
		job      model.Job
		lang     string
		enforced bool
		want     string
	}{
		{"lyrics tr", model.Job{Type: "lyrics", Status: "succeeded", ProjectName: "Yaz", Result: allDone}, "tr", true, "Sözlerin hazır — Yaz"},
		{"lyrics en", model.Job{Type: "lyrics", Status: "succeeded", ProjectName: "Summer", Result: allDone}, "en", true, "Your lyrics are ready — Summer"},
		{"lyrics fr uses en", model.Job{Type: "lyrics", Status: "succeeded", ProjectName: "Ete", Result: allDone}, "fr", true, "Your lyrics are ready — Ete"},
		{"lyrics empty name tr", model.Job{Type: "lyrics", Status: "succeeded", Result: allDone}, "tr", true, "Sözlerin hazır"},
		{"lyrics empty name en", model.Job{Type: "lyrics", Status: "succeeded", Result: allDone}, "en", true, "Your lyrics are ready"},
		{"lyrics partial tr", model.Job{Type: "lyrics", Status: "succeeded", ProjectName: "Yaz", Result: partial}, "tr", true, "Sözlerin hazır, 2 bölüm yazılamadı"},
		{"lyrics partial en", model.Job{Type: "lyrics", Status: "succeeded", ProjectName: "Yaz", Result: partial}, "en", true, "Your lyrics are ready, 2 sections could not be written"},
		{"render tr", model.Job{Type: "render", Status: "succeeded"}, "tr", true, "Şarkın hazır — dinlemeye gel"},
		{"render en", model.Job{Type: "render", Status: "succeeded"}, "en", true, "Your song is ready — come listen"},
		{"master tr", model.Job{Type: "master", Status: "succeeded"}, "tr", true, "Son halin hazır"},
		{"master en", model.Job{Type: "master", Status: "succeeded"}, "en", true, "Your final mix is ready"},
		{"failed enforced tr", model.Job{Type: "render", Status: "failed"}, "tr", true, "Şarkın üretilemedi, kredin iade edildi"},
		{"failed enforced en", model.Job{Type: "master", Status: "failed"}, "en", true, "Your song could not be made, your credits were refunded"},
		{"failed shadow tr", model.Job{Type: "render", Status: "failed"}, "tr", false, "Şarkın üretilemedi"},
		{"failed shadow en", model.Job{Type: "lyrics", Status: "failed"}, "en", false, "Your song could not be made"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Compose(tt.job, tt.lang, tt.enforced)
			if m.Body != tt.want {
				t.Errorf("body = %q, want %q", m.Body, tt.want)
			}
			if m.Title != "MakeASinger" {
				t.Errorf("title = %q", m.Title)
			}
		})
	}
}

func TestCompose_Data(t *testing.T) {
	m := Compose(model.Job{ID: "j1", Type: "master", Status: "succeeded", ProjectID: "p1"}, "en", true)
	want := map[string]string{"jobId": "j1", "projectId": "p1", "step": "master", "type": "master", "status": "succeeded"}
	if len(m.Data) != len(want) {
		t.Fatalf("data = %v", m.Data)
	}
	for k, v := range want {
		if m.Data[k] != v {
			t.Errorf("data[%s] = %q, want %q", k, m.Data[k], v)
		}
	}
}
