package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/model"
)

// lyricsSahteGroq, gelen istekleri biriktirip verilen icerigi donen sahte Groq.
func lyricsSahteGroq(t *testing.T, icerik string, istekler *[]map[string]any) *client.GroqClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var govde map[string]any
		_ = json.NewDecoder(r.Body).Decode(&govde)
		*istekler = append(*istekler, govde)
		yanit, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": icerik}}},
		})
		_, _ = w.Write(yanit)
	}))
	t.Cleanup(srv.Close)
	return client.NewGroqClient(&config.GroqConfig{
		APIKey: "k", BaseURL: srv.URL, Model: "m", MaxTokens: 4000,
		TemperatureGenerate: 0.8, TemperatureRewrite: 0.6,
	})
}

func systemMesaji(t *testing.T, govde map[string]any) string {
	t.Helper()
	msgs, _ := govde["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("istekte mesaj yok")
	}
	m, _ := msgs[0].(map[string]any)
	s, _ := m["content"].(string)
	return s
}

func TestRewrite_IstenenDilinSystemPromptunuKullanir(t *testing.T) {
	var istekler []map[string]any
	s := NewLyricsService(lyricsSahteGroq(t, `{"lines":["bir","iki"]}`, &istekler))

	res, err := s.Rewrite(context.Background(), &model.LyricsRewriteRequest{
		CurrentLyrics: "bir\niki", Genre: model.Genre("pop"), SectionType: model.SectionChorus,
		Vibes: []string{"huzunlu"}, Language: model.LanguageTR,
	})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if len(res.Lines) != 2 {
		t.Errorf("satirlar = %v", res.Lines)
	}
	if sys := systemMesaji(t, istekler[0]); !strings.Contains(sys, "Turkish") {
		t.Errorf("system prompt Turkce olmali, geldi: %q", sys)
	}
	if istekler[0]["temperature"] != 0.6 {
		t.Errorf("rewrite sicakligi = %v, 0.6 bekleniyordu", istekler[0]["temperature"])
	}
}

func TestRewrite_DilVerilmezseIngilizce(t *testing.T) {
	var istekler []map[string]any
	s := NewLyricsService(lyricsSahteGroq(t, `{"lines":["a"]}`, &istekler))

	if _, err := s.Rewrite(context.Background(), &model.LyricsRewriteRequest{
		CurrentLyrics: "a", Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"x"},
	}); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if sys := systemMesaji(t, istekler[0]); !strings.Contains(sys, "English") {
		t.Errorf("varsayilan dil Ingilizce olmali, geldi: %q", sys)
	}
}

func TestGenerate_UretimSicakliginiKullanir(t *testing.T) {
	var istekler []map[string]any
	s := NewLyricsService(lyricsSahteGroq(t, `{"drafts":[["a","b"],["c","d"]]}`, &istekler))

	res, err := s.Generate(context.Background(), &model.LyricsGenerateRequest{
		Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"x"}, Language: model.LanguageTR,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Drafts) != 2 {
		t.Errorf("taslaklar = %v", res.Drafts)
	}
	if istekler[0]["temperature"] != 0.8 {
		t.Errorf("uretim sicakligi = %v, 0.8 bekleniyordu", istekler[0]["temperature"])
	}
}
