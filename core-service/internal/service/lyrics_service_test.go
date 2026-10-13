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

// fakeLyricsGroq is a fake Groq that collects the incoming requests and returns the given content.
func fakeLyricsGroq(t *testing.T, content string, requests *[]map[string]any) *client.GroqClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*requests = append(*requests, body)
		response, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		})
		_, _ = w.Write(response)
	}))
	t.Cleanup(srv.Close)
	return client.NewGroqClient(&config.GroqConfig{
		APIKey: "k", BaseURL: srv.URL, Model: "m", MaxTokens: 4000,
		TemperatureGenerate: 0.8, TemperatureRewrite: 0.6,
	})
}

func systemMessage(t *testing.T, body map[string]any) string {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages in the request")
	}
	m, _ := msgs[0].(map[string]any)
	s, _ := m["content"].(string)
	return s
}

func TestLyricsService_RewriteUsesRequestedLanguagePrompt(t *testing.T) {
	var requests []map[string]any
	s := NewLyricsService(fakeLyricsGroq(t, `{"lines":["bir","iki"]}`, &requests))

	res, err := s.Rewrite(context.Background(), &model.LyricsRewriteRequest{
		CurrentLyrics: "bir\niki", Genre: model.Genre("pop"), SectionType: model.SectionChorus,
		Vibes: []string{"huzunlu"}, Language: model.LanguageTR,
	})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if len(res.Lines) != 2 {
		t.Errorf("lines = %v", res.Lines)
	}
	if sys := systemMessage(t, requests[0]); !strings.Contains(sys, "Turkish") {
		t.Errorf("the system prompt must be Turkish, got: %q", sys)
	}
	if requests[0]["temperature"] != 0.6 {
		t.Errorf("rewrite temperature = %v, want 0.6", requests[0]["temperature"])
	}
}

func TestLyricsService_RewriteDefaultsToEnglish(t *testing.T) {
	var requests []map[string]any
	s := NewLyricsService(fakeLyricsGroq(t, `{"lines":["a"]}`, &requests))

	if _, err := s.Rewrite(context.Background(), &model.LyricsRewriteRequest{
		CurrentLyrics: "a", Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"x"},
	}); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if sys := systemMessage(t, requests[0]); !strings.Contains(sys, "English") {
		t.Errorf("the default language must be English, got: %q", sys)
	}
}

func TestLyricsService_GenerateUsesGenerateTemperature(t *testing.T) {
	var requests []map[string]any
	s := NewLyricsService(fakeLyricsGroq(t, `{"drafts":[["a","b"],["c","d"]]}`, &requests))

	res, err := s.Generate(context.Background(), &model.LyricsGenerateRequest{
		Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"x"}, Language: model.LanguageTR,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Drafts) != 2 {
		t.Errorf("drafts = %v", res.Drafts)
	}
	if requests[0]["temperature"] != 0.8 {
		t.Errorf("generation temperature = %v, want 0.8", requests[0]["temperature"])
	}
}

func userMessage(t *testing.T, body map[string]any) string {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	if len(msgs) < 2 {
		t.Fatal("no user message in the request")
	}
	m, _ := msgs[1].(map[string]any)
	s, _ := m["content"].(string)
	return s
}

func TestLyricsService_UserPromptsDemandRequestedLanguage(t *testing.T) {
	var requests []map[string]any
	gen := NewLyricsService(fakeLyricsGroq(t, `{"drafts":[["bir"]]}`, &requests))
	if _, err := gen.Generate(context.Background(), &model.LyricsGenerateRequest{
		Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"happy"}, Language: model.LanguageTR,
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	rew := NewLyricsService(fakeLyricsGroq(t, `{"lines":["bir"]}`, &requests))
	if _, err := rew.Rewrite(context.Background(), &model.LyricsRewriteRequest{
		CurrentLyrics: "one", Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"happy"}, Language: model.LanguageTR,
	}); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	for i, req := range requests {
		if u := userMessage(t, req); !strings.Contains(u, "Write ALL lyrics in Turkish") {
			t.Errorf("request %d user prompt must demand Turkish, got: %q", i, u)
		}
	}
}

func TestLyricsService_GenerateRecoversFromJSONValidateFailed(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"json_validate_failed","failed_generation":"{\"drafts\":[[\"x\"]]}"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"drafts\":[[\"a\",\"b\"],[\"c\"]]}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	s := NewLyricsService(client.NewGroqClient(&config.GroqConfig{APIKey: "k", BaseURL: srv.URL, Model: "m"}))

	res, err := s.Generate(context.Background(), &model.LyricsGenerateRequest{
		Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"x"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Drafts) != 2 || calls != 2 {
		t.Errorf("drafts = %v, calls = %d", res.Drafts, calls)
	}
}

func TestLyricsService_GeneratePromptStatesDraftsShape(t *testing.T) {
	var requests []map[string]any
	s := NewLyricsService(fakeLyricsGroq(t, `{"drafts":[["a"]]}`, &requests))
	if _, err := s.Generate(context.Background(), &model.LyricsGenerateRequest{
		Genre: model.Genre("pop"), SectionType: model.SectionVerse, Vibes: []string{"x"},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	msgs, _ := requests[0]["messages"].([]any)
	user, _ := msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "array of plain strings") && !strings.Contains(user, "plain strings") {
		t.Errorf("prompt does not state the drafts shape: %q", user)
	}
}
