package e2e

import (
	"net/http"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/service"
)

// setupLyricsRealApp creates an app with a real Groq client for lyrics testing.
func setupLyricsRealApp(t *testing.T) *fiber.App {
	t.Helper()
	loadEnvFile(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Groq.APIKey == "" {
		t.Skip("skipping: GROQ_API_KEY not configured")
	}

	t.Logf("Groq config: baseURL=%s model=%s", cfg.Groq.BaseURL, cfg.Groq.Model)

	// Redis for rate limiter
	redisClient := redis.NewClient(&redis.Options{
		Addr: cfg.Redis.Addr,
		DB:   15,
	})

	validate := validator.New()

	// Real Groq client
	groqClient := client.NewGroqClient(&cfg.Groq)
	if !groqClient.IsConfigured() {
		t.Skip("skipping: Groq client not configured")
	}

	lyricsService := service.NewLyricsService(groqClient)
	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)

	authMiddleware := middleware.NewLegacyAuthMiddleware(testJWTSecret)
	rateLimiter := middleware.NewRateLimiter(redisClient)

	app := fiber.New(fiber.Config{BodyLimit: 50 * 1024 * 1024})

	api := app.Group("/api", authMiddleware.Authenticate())
	lyrics := api.Group("/lyrics", rateLimiter.LyricsLimit(10000))
	lyrics.Post("/generate", lyricsHandler.Generate)
	lyrics.Post("/rewrite", lyricsHandler.Rewrite)

	return app
}

// TestLyricsGenerate_RealGroq tests lyrics generation with the real Groq API.
func TestLyricsGenerate_RealGroq(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Groq API test in short mode")
	}

	app := setupLyricsRealApp(t)
	token := generateToken(t)
	headers := map[string]string{"Authorization": "Bearer " + token}

	body := `{
		"genre": "pop",
		"sectionType": "chorus",
		"vibes": ["happy", "energetic"],
		"language": "en"
	}`

	t.Log("Sending lyrics generate request to real Groq API...")
	resp, err := doRequest(app, http.MethodPost, "/api/lyrics/generate", body, headers)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)

	// Verify drafts exist
	drafts, ok := result["drafts"].([]interface{})
	if !ok {
		t.Fatalf("expected 'drafts' array in response, got: %v", result)
	}

	if len(drafts) == 0 {
		t.Fatal("expected at least 1 draft")
	}

	t.Logf("Received %d draft(s) from Groq API", len(drafts))

	for i, d := range drafts {
		draft, ok := d.([]interface{})
		if !ok {
			t.Errorf("draft[%d]: expected array of strings, got %T", i, d)
			continue
		}
		if len(draft) == 0 {
			t.Errorf("draft[%d]: expected at least 1 line", i)
			continue
		}
		t.Logf("Draft %d (%d lines):", i+1, len(draft))
		for j, line := range draft {
			t.Logf("  [%d] %v", j+1, line)
		}
	}
}

// TestLyricsGenerate_Turkish_RealGroq tests Turkish lyrics generation.
func TestLyricsGenerate_Turkish_RealGroq(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Groq API test in short mode")
	}

	app := setupLyricsRealApp(t)
	token := generateToken(t)
	headers := map[string]string{"Authorization": "Bearer " + token}

	body := `{
		"genre": "pop",
		"sectionType": "verse",
		"vibes": ["romantic", "melancholic"],
		"language": "tr"
	}`

	t.Log("Sending Turkish lyrics generate request...")
	resp, err := doRequest(app, http.MethodPost, "/api/lyrics/generate", body, headers)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	drafts, ok := result["drafts"].([]interface{})
	if !ok || len(drafts) == 0 {
		t.Fatalf("expected drafts, got: %v", result)
	}

	t.Logf("Turkish lyrics - %d draft(s):", len(drafts))
	for i, d := range drafts {
		draft := d.([]interface{})
		t.Logf("Draft %d:", i+1)
		for j, line := range draft {
			t.Logf("  [%d] %v", j+1, line)
		}
	}
}

// TestLyricsRewrite_RealGroq tests lyrics rewriting with the real Groq API.
func TestLyricsRewrite_RealGroq(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Groq API test in short mode")
	}

	app := setupLyricsRealApp(t)
	token := generateToken(t)
	headers := map[string]string{"Authorization": "Bearer " + token}

	body := `{
		"genre": "rock",
		"sectionType": "chorus",
		"vibes": ["powerful", "anthemic"],
		"currentLyrics": "I walk alone in the dark\nSearching for a spark\nNothing feels the same\nI forgot your name",
		"instructions": "Make it more dramatic and powerful"
	}`

	t.Log("Sending lyrics rewrite request to real Groq API...")
	resp, err := doRequest(app, http.MethodPost, "/api/lyrics/rewrite", body, headers)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)
	lines, ok := result["lines"].([]interface{})
	if !ok || len(lines) == 0 {
		t.Fatalf("expected 'lines' array, got: %v", result)
	}

	t.Logf("Rewritten lyrics (%d lines):", len(lines))
	for i, line := range lines {
		t.Logf("  [%d] %v", i+1, line)
	}
}
