package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/service"
)

const (
	testUserID   = "test-user-123"
	testIssuer   = "https://kimlik.e2e.test"
	testClientID = "e2e-istemci"
)

// testKey is the RSA key that signs the e2e tokens, created once per package.
var testKey = func() *rsa.PrivateKey {
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return a
}()

// e2eR2Cfg is a fake R2 configuration that produces the hosts KeyFromURL recognizes.
func e2eR2Cfg() *config.R2Config {
	return &config.R2Config{
		AccountID:     "hesap123",
		PublicBucket:  "makeasinger-public",
		PrivateBucket: "makeasinger-private",
		PublicURL:     "https://makesinger-cdn.celalettindemir.dev",
	}
}

// e2eURL builds a URL that KeyFromURL can resolve back to an owner-prefixed key.
func e2eURL(t *testing.T, kind, userID string, parts ...string) string {
	t.Helper()
	key, err := client.UserKey(kind, userID, parts...)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return client.UnsignedURL(key, e2eR2Cfg())
}

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
	lyricsService := service.NewLyricsService(lyricsprovider.Fixed(groqClient))
	renderService := service.NewRenderService(redisClient, asynqClient, nil, e2eR2Cfg())
	masterService := service.NewMasterService(redisClient, asynqClient, nil, e2eR2Cfg())
	exportService := service.NewExportService(nil, nil, e2eR2Cfg()) // nil triggers mock fallbacks
	uploadService := service.NewUploadService(nil, e2eR2Cfg())

	// Handlers
	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
	renderHandler := handler.NewRenderHandler(renderService, validate)
	masterHandler := handler.NewMasterHandler(masterService, validate)
	exportHandler := handler.NewExportHandler(exportService, validate)
	uploadHandler := handler.NewUploadHandler(uploadService, validate)

	// Auth middleware: our own OP's RS256 access token
	authMiddleware := middleware.NewOPAuthMiddleware(testIssuer, testClientID, &testKey.PublicKey)
	rateLimiter := middleware.NewRateLimiter(redisClient, nil)

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

// generateToken produces an OP-style RS256 access token for test requests.
func generateToken(t *testing.T) string {
	t.Helper()
	return generateTokenFor(t, testUserID)
}

// generateTokenFor produces an OP-style RS256 access token for the given sub.
func generateTokenFor(t *testing.T, sub string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   testIssuer,
		"sub":   sub,
		"aud":   []string{testClientID},
		"jti":   "e2e-jeton",
		"email": "test@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString(testKey)
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
