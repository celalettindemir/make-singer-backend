package e2e

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
)

var (
	streamBracketRe = regexp.MustCompile(`^\s*(\[.*\]|\(.*\))\s*$`)
	streamLabelRe   = regexp.MustCompile(`(?i)^\s*(verse|chorus|bridge|intro|outro|pre-chorus|hook|nakarat|kıta|kita|köprü|giriş|çıkış|couplet|refrain|pont)\s*:?\s*$`)
)

// TestRealLyricsStream_PerProvider streams the Turkish context request from
// every provider whose key is configured. Paid; skipped in short mode.
func TestRealLyricsStream_PerProvider(t *testing.T) {
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
			c := newClient()
			if !c.IsConfigured() {
				t.Skip("skipping: client not configured")
			}
			var req model.LyricsGenerateRequest
			if err := json.Unmarshal([]byte(contextBody), &req); err != nil {
				t.Fatalf("bad request body: %v", err)
			}
			svc := service.NewLyricsService(lyricsprovider.Fixed(c))

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			start := time.Now()
			var first time.Duration
			var lines []string
			n, err := svc.GenerateStream(ctx, &req, func(line string) error {
				if first == 0 {
					first = time.Since(start)
				}
				lines = append(lines, line)
				return nil
			})
			if err != nil {
				t.Fatalf("stream failed after %d lines: %v", len(lines), err)
			}
			t.Logf("time to first line %v, total %v, %d lines", first, time.Since(start), n)
			if n != len(lines) {
				t.Errorf("returned count %d != emitted %d", n, len(lines))
			}
			if len(lines) < 2 {
				t.Fatalf("want >= 2 lines, got %v", lines)
			}
			for _, l := range lines {
				t.Logf("line: %s", l)
				if streamBracketRe.MatchString(l) || streamLabelRe.MatchString(l) {
					t.Errorf("section label emitted as a line: %q", l)
				}
				if r := strings.TrimSpace(l); r == "" || (r[0] >= '0' && r[0] <= '9') {
					t.Errorf("empty line or digit start: %q", l)
				}
			}
		})
	}
}
