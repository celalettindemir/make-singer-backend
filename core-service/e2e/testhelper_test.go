package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/makeasinger/api/internal/auth"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/service"
)

const testJWTSecret = "test-secret-for-e2e"

// testApp holds all components needed for testing
type testApp struct {
	app *fiber.App
}

// setupApp creates a Fiber app identical to main.go but with unconfigured external clients.
// This triggers mock/fallback responses in all services.
func setupApp(t *testing.T) *testApp {
	t.Helper()

	// Redis (localhost — must be running)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // use DB 15 for tests to avoid collision
	})

	// Asynq client
	asynqClient := asynq.NewClient(asynq.RedisClientOpt{
		Addr: "localhost:6379",
		DB:   15,
	})
	t.Cleanup(func() { asynqClient.Close() })

	validate := validator.New()

	// External clients — all unconfigured so services use mock fallbacks
	groqClient := client.NewGroqClient(&config.GroqConfig{}) // no API key → mock
	// r2Client = nil → mock
	// audioClient = nil → mock
	// sunoClient not needed for handler tests

	// Services
	lyricsService := service.NewLyricsService(groqClient)
	renderService := service.NewRenderService(redisClient, asynqClient)
	masterService := service.NewMasterService(redisClient, asynqClient)
	exportService := service.NewExportService(nil, nil) // nil triggers mock fallbacks
	uploadService := service.NewUploadService(nil)

	// Handlers
	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
	renderHandler := handler.NewRenderHandler(renderService, validate)
	masterHandler := handler.NewMasterHandler(masterService, validate)
	exportHandler := handler.NewExportHandler(exportService, validate)
	uploadHandler := handler.NewUploadHandler(uploadService, validate)

	// Auth handler (for /auth/verify)
	authHandler := handler.NewAuthHandler(nil, testJWTSecret)

	// Auth middleware — legacy HMAC only
	authMiddleware := middleware.NewLegacyAuthMiddleware(testJWTSecret)
	rateLimiter := middleware.NewRateLimiter(redisClient)

	// Fiber app
	app := fiber.New(fiber.Config{
		BodyLimit: 50 * 1024 * 1024,
	})

	// Base routes
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"timestamp": 1234567890})
	})
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"services": fiber.Map{
				"groq":  false,
				"suno":  false,
				"r2":    false,
				"audio": false,
				"auth":  true,
			},
		})
	})
	app.Get("/auth/verify", authHandler.Verify)

	// API routes (authenticated)
	api := app.Group("/api", authMiddleware.Authenticate())

	// Use very high rate limits so tests don't get blocked
	lyrics := api.Group("/lyrics", rateLimiter.LyricsLimit(10000))
	lyrics.Post("/generate", lyricsHandler.Generate)
	lyrics.Post("/rewrite", lyricsHandler.Rewrite)

	render := api.Group("/render")
	render.Post("/start", rateLimiter.RenderLimit(10000), renderHandler.Start)
	render.Get("/status/:jobId", renderHandler.Status)
	render.Get("/result/:jobId", renderHandler.Result)
	render.Post("/cancel/:jobId", renderHandler.Cancel)

	master := api.Group("/master", rateLimiter.MasterLimit(10000))
	master.Post("/preview", masterHandler.Preview)
	master.Post("/final", masterHandler.Final)
	master.Get("/status/:jobId", masterHandler.Status)
	master.Get("/result/:jobId", masterHandler.Result)

	export := api.Group("/export", rateLimiter.ExportLimit(10000))
	export.Post("/mp3", exportHandler.MP3)
	export.Post("/wav", exportHandler.WAV)
	export.Post("/stems", exportHandler.Stems)

	upload := api.Group("/upload", rateLimiter.UploadLimit(10000))
	upload.Post("/vocal", uploadHandler.Vocal)
	upload.Delete("/vocal/:takeId", uploadHandler.DeleteVocal)

	return &testApp{app: app}
}

// generateToken creates a legacy HMAC JWT token for test requests.
func generateToken(t *testing.T) string {
	t.Helper()
	claims := auth.LegacyClaims{
		UserID: "test-user-123",
		Email:  "test@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "makeasinger-api",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("failed to generate test token: %v", err)
	}
	return signed
}

// doRequest is a helper to perform HTTP requests against the test app.
func doRequest(app *fiber.App, method, path string, body string, headers map[string]string) (*http.Response, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, path, bodyReader)
	if err != nil {
		return nil, err
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return app.Test(req, -1)
}

// doAuthRequest performs an authenticated request.
func doAuthRequest(t *testing.T, app *fiber.App, method, path, body string) (*http.Response, error) {
	t.Helper()
	token := generateToken(t)
	return doRequest(app, method, path, body, map[string]string{
		"Authorization": "Bearer " + token,
	})
}

// readBody reads and returns the response body as a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return string(b)
}

// parseJSON parses response body into a map.
func parseJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	body := readBody(t, resp)
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v\nbody: %s", err, body)
	}
	return result
}

// assertStatus checks the HTTP status code.
func assertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		t.Errorf("expected status %d, got %d", expected, resp.StatusCode)
	}
}
