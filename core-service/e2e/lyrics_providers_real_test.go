package e2e

import (
	"net/http"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
)

// contextBody is a Turkish chorus request with a topic and the song so far
// (native/api/examples/generateLyrics.request.context.json).
const contextBody = `{"genre": "pop", "sectionType": "chorus", "vibes": ["melancholic", "romantic"], "language": "tr",
	"topic": "Eski sevgiliye yazılmış, kızgın değil, özlem dolu", "bpm": 96,
	"song": [{"type": "intro", "bars": 8},
		{"type": "verse", "bars": 16, "lyrics": "Eski sokakta yürüdüm bu sabah\nKapının önünde durdum bir an\nAdın hâlâ zilin üstünde yazılı\nBen hâlâ o günde kaldım sanki"},
		{"type": "chorus", "bars": 16}, {"type": "verse", "bars": 16}],
	"sectionIndex": 2}`

// TestRealLyrics_ContextPerProvider sends the context request to every
// provider whose key is configured. Paid; skipped in short mode.
func TestRealLyrics_ContextPerProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real provider tests in short mode")
	}
	cfg := loadRealConfig(t)
	providers := map[string]func() client.ChatClient{
		"groq":      func() client.ChatClient { return client.NewGroqClient(&cfg.Groq) },
		"openai":    func() client.ChatClient { return client.NewOpenAIClient(&cfg.OpenAI) },
		"anthropic": func() client.ChatClient { return client.NewAnthropicClient(&cfg.Anthropic) },
	}
	for name, newClient := range providers {
		t.Run(name, func(t *testing.T) {
			runContextRequest(t, cfg, newClient())
		})
	}
}

func runContextRequest(t *testing.T, cfg *config.Config, c client.ChatClient) {
	app := setupLyricsRealAppWith(t, cfg, c)
	headers := map[string]string{"Authorization": "Bearer " + generateToken(t)}
	resp, err := doRequest(app, http.MethodPost, "/api/lyrics/generate", contextBody, headers)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)
	drafts, _ := parseJSON(t, resp)["drafts"].([]interface{})
	if len(drafts) < 2 {
		t.Fatalf("want 2 drafts, got %v", drafts)
	}
	for i, d := range drafts {
		lines, _ := d.([]interface{})
		if len(lines) < 2 {
			t.Errorf("draft %d has %d lines", i, len(lines))
		}
		for _, l := range lines {
			t.Logf("draft %d: %v", i+1, l)
		}
	}
}
