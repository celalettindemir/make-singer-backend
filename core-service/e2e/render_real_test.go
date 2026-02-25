package e2e

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/makeasinger/api/internal/auth"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
	"github.com/makeasinger/api/internal/worker"

	"github.com/golang-jwt/jwt/v5"
)

// loadEnvFile reads a .env file and sets environment variables.
func loadEnvFile(t *testing.T) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	envPath := filepath.Join(filepath.Dir(filename), "..", ".env")

	f, err := os.Open(envPath)
	if err != nil {
		t.Skipf("skipping: .env file not found at %s", envPath)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			os.Setenv(parts[0], parts[1])
		}
	}
}

// setupRealApp creates a full app with real external clients + Asynq worker.
// Returns the app and a cleanup function.
func setupRealApp(t *testing.T) (*fiber.App, func()) {
	t.Helper()
	loadEnvFile(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Suno.APIKey == "" {
		t.Skip("skipping: SUNO_API_KEY not configured")
	}

	// Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       15, // test DB
	})

	// Asynq client
	asynqClient := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       15,
	})

	validate := validator.New()

	// Real external clients
	groqClient := client.NewGroqClient(&cfg.Groq)
	sunoClient := client.NewSunoClient(&cfg.Suno)

	// R2 client (optional)
	var r2Client *client.R2Client
	if cfg.R2.AccessKeyID != "" && cfg.R2.SecretAccessKey != "" {
		r2Client, _ = client.NewR2Client(&cfg.R2)
	}

	// WebSocket hub
	hub := websocket.NewHub()
	go hub.Run()

	// Services
	lyricsService := service.NewLyricsService(groqClient)
	renderService := service.NewRenderService(redisClient, asynqClient)
	masterService := service.NewMasterService(redisClient, asynqClient)
	exportService := service.NewExportService(nil, nil)
	uploadService := service.NewUploadService(nil)

	// Handlers
	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
	renderHandler := handler.NewRenderHandler(renderService, validate)
	masterHandler := handler.NewMasterHandler(masterService, validate)
	exportHandler := handler.NewExportHandler(exportService, validate)
	uploadHandler := handler.NewUploadHandler(uploadService, validate)
	authHandler := handler.NewAuthHandler(nil, testJWTSecret)

	// Middleware
	authMiddleware := middleware.NewLegacyAuthMiddleware(testJWTSecret)
	rateLimiter := middleware.NewRateLimiter(redisClient)

	// Fiber app
	app := fiber.New(fiber.Config{BodyLimit: 50 * 1024 * 1024})

	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"timestamp": time.Now().Unix()})
	})
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	app.Get("/auth/verify", authHandler.Verify)

	api := app.Group("/api", authMiddleware.Authenticate())

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

	// Start Asynq worker server (non-blocking)
	asynqSrv := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       15,
		},
		asynq.Config{
			Concurrency: 2,
			Queues:      map[string]int{"render": 1, "master": 1},
			LogLevel:    asynq.WarnLevel,
		},
	)

	renderWorker := worker.NewRenderWorker(renderService, sunoClient, r2Client, hub)
	mux := asynq.NewServeMux()
	mux.HandleFunc(service.TaskTypeRender, renderWorker.ProcessTask)

	if err := asynqSrv.Start(mux); err != nil {
		t.Fatalf("failed to start asynq worker: %v", err)
	}

	cleanup := func() {
		asynqSrv.Shutdown()
		asynqClient.Close()
	}

	return app, cleanup
}

func generateRealToken(t *testing.T) string {
	t.Helper()
	claims := auth.LegacyClaims{
		UserID: "e2e-test-user",
		Email:  "e2e@makeasinger.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "makeasinger-api",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	return signed
}

// TestRenderFullPipeline_RealSuno tests the full render pipeline with real Suno API.
// This test takes several minutes as it waits for actual music generation.
func TestRenderFullPipeline_RealSuno(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Suno API test in short mode")
	}

	app, cleanup := setupRealApp(t)
	defer cleanup()

	token := generateRealToken(t)
	headers := map[string]string{"Authorization": "Bearer " + token}

	// Step 1: Start a render job
	projectID := uuid.New().String()
	sectionID := uuid.New().String()
	body := fmt.Sprintf(`{
		"projectId": "%s",
		"brief": {
			"genre": "electronic",
			"vibes": ["chill", "ambient"],
			"bpm": {"mode": "fixed", "value": 90},
			"key": {"mode": "manual", "tonic": "A", "scale": "minor"},
			"structure": [
				{"id": "%s", "type": "verse", "bars": 4}
			]
		},
		"arrangement": {
			"instruments": ["synth", "drums", "bass"],
			"density": "minimal",
			"groove": "straight"
		}
	}`, projectID, sectionID)

	t.Log("Starting render job...")
	resp, err := doRequest(app, http.MethodPost, "/api/render/start", body, headers)
	if err != nil {
		t.Fatalf("start request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusAccepted)

	startResult := parseJSON(t, resp)
	jobID, ok := startResult["jobId"].(string)
	if !ok || jobID == "" {
		t.Fatal("expected jobId in start response")
	}
	t.Logf("Job started: %s (status: %s)", jobID, startResult["status"])

	// Step 2: Poll for completion (max 15 minutes)
	deadline := time.Now().Add(15 * time.Minute)
	pollInterval := 5 * time.Second
	var lastStatus string

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		resp, err = doRequest(app, http.MethodGet, "/api/render/status/"+jobID, "", headers)
		if err != nil {
			t.Fatalf("status request failed: %v", err)
		}
		assertStatus(t, resp, http.StatusOK)

		statusResult := parseJSON(t, resp)
		status := statusResult["status"].(string)
		progress := statusResult["progress"].(float64)
		step := ""
		if s, ok := statusResult["currentStep"].(string); ok {
			step = s
		}

		if status != lastStatus {
			t.Logf("Job %s: status=%s progress=%.0f%% step=%s", jobID, status, progress, step)
			lastStatus = status
		}

		switch model.JobStatus(status) {
		case model.JobStatusSucceeded:
			t.Log("Job completed successfully!")
			goto checkResult

		case model.JobStatusFailed:
			errMsg := "unknown"
			if e, ok := statusResult["error"].(string); ok {
				errMsg = e
			}
			t.Fatalf("Job failed: %s", errMsg)

		case model.JobStatusCanceled:
			t.Fatal("Job was canceled unexpectedly")
		}
	}
	t.Fatal("Job timed out after 15 minutes")

checkResult:
	// Step 3: Get the result
	resp, err = doRequest(app, http.MethodGet, "/api/render/result/"+jobID, "", headers)
	if err != nil {
		t.Fatalf("result request failed: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	result := parseJSON(t, resp)

	// Verify result structure
	if result["id"] == nil || result["id"] == "" {
		t.Error("expected 'id' in result")
	}
	if result["bpm"] == nil {
		t.Error("expected 'bpm' in result")
	}
	if result["duration"] == nil {
		t.Error("expected 'duration' in result")
	}
	t.Logf("Result: id=%s bpm=%v duration=%vs", result["id"], result["bpm"], result["duration"])

	// Verify key
	key, ok := result["key"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'key' object in result")
	}
	t.Logf("Key: %s %s", key["tonic"], key["scale"])

	// Verify stems
	stems, ok := result["stems"].([]interface{})
	if !ok {
		t.Fatal("expected 'stems' array in result")
	}
	if len(stems) == 0 {
		t.Error("expected at least one stem")
	}

	for i, s := range stems {
		stem := s.(map[string]interface{})
		t.Logf("Stem[%d]: instrument=%s url=%s duration=%v",
			i, stem["instrument"], stem["fileUrl"], stem["duration"])

		if stem["fileUrl"] == nil || stem["fileUrl"] == "" {
			t.Errorf("stem[%d]: expected fileUrl", i)
		}
	}

	t.Logf("Full render pipeline completed with %d stems", len(stems))
}
