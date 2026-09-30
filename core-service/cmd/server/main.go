package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	fiberSwagger "github.com/gofiber/swagger"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/makeasinger/api/docs"
	"github.com/makeasinger/api/internal/auth"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/kimlik"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/service"
	ws "github.com/makeasinger/api/internal/websocket"
	"github.com/makeasinger/api/internal/worker"
)

// @title          Make-Singer API
// @version        1.0
// @description    Backend API for Make-Singer — AI-powered music creation platform.
// @host           localhost:8000
// @BasePath       /
// @schemes        http https
// @securityDefinitions.apikey BearerAuth
// @in             header
// @name           Authorization
// @description    Enter your bearer token in the format **Bearer &lt;token&gt;**
func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Configure Swagger host/scheme based on environment
	if cfg.Server.ApiDomain != "" {
		docs.SwaggerInfo.Host = cfg.Server.ApiDomain
		docs.SwaggerInfo.Schemes = []string{"https"}
	} else {
		docs.SwaggerInfo.Host = "localhost:" + cfg.Server.Port
		docs.SwaggerInfo.Schemes = []string{"http"}
	}

	// Initialize Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Printf("Warning: Redis not available: %v", err)
	}

	// Kendi OpenID Provider'imiz. Issuer bos ise hic baslamaz; API o
	// zaman eski auth yolunda kalir (yerel gelistirme).
	//
	// Issuer DOLU ise baslatma hatasi FATALDIR. Eskiden yalnizca uyari
	// loglaniyordu; o durumda /api/* legacy HMAC yoluna dusuyordu ve
	// legacy dogrulama ne exp ne iss ne aud istedigi icin, varsayilan
	// sirla (`change-me-in-production`) imzalanmis SURESIZ bir jeton
	// kabul ediliyordu. Yani gecici bir Postgres kesintisi tum API'yi
	// aciyordu. Her istege 401 donen ama /health'te sagliklı gorunen
	// bir pod sessiz bir kesintidir; CrashLoopBackOff dogru ve gorunur
	// sinyal, yeniden baslatma da gecici kesinti icin dogru kurtarma.
	var kimlikSunucu *kimlik.Sunucu
	if cfg.Auth.Issuer != "" {
		var err error
		kimlikSunucu, err = kimlik.Start(ctx, &cfg.Auth, redisClient, cfg.Server.Env)
		if err != nil {
			log.Fatalf("Kimlik saglayicisi baslatilamadi, servis baslatilmiyor: %v", err)
		} else {
			log.Printf("Kimlik saglayicisi %s uzerinde, issuer %s", cfg.Auth.Port, cfg.Auth.Issuer)
			defer func() {
				kapatCtx, iptal := context.WithTimeout(context.Background(), 10*time.Second)
				defer iptal()
				_ = kimlikSunucu.Kapat(kapatCtx)
			}()
		}
	}

	// Initialize Asynq client
	asynqClient := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer asynqClient.Close()

	// Initialize validator
	validate := validator.New()

	// Initialize WebSocket hub
	hub := ws.NewHub()
	go hub.Run()

	// Initialize external clients
	groqClient := client.NewGroqClient(&cfg.Groq)
	sunoClient := client.NewSunoClient(&cfg.Suno)
	audioClient := client.NewAudioClient(&cfg.Audio)

	// Initialize R2 client (optional - continues if not configured)
	var r2Client *client.R2Client
	if cfg.R2.AccessKeyID != "" && cfg.R2.SecretAccessKey != "" {
		var err error
		r2Client, err = client.NewR2Client(&cfg.R2)
		if err != nil {
			log.Printf("Warning: R2 client not initialized: %v", err)
		}
	} else {
		log.Println("Info: R2 storage not configured, using mock storage")
	}

	// storage: arayuz degiskeni. r2Client nil iken dogrudan arayuze
	// atamak "tipli nil isaretci" tuzagini kurar (arayuz nil olmaz ama
	// icindeki isaretci nil'dir); bu yuzden acikca ayiriyoruz.
	var storage client.StorageClient
	if r2Client != nil {
		storage = r2Client
	}

	// Initialize Zitadel JWKS verifier (optional - falls back to legacy JWT)
	var jwksVerifier *auth.JWKSVerifier
	if cfg.Zitadel.Issuer != "" {
		var err error
		jwksVerifier, err = auth.NewJWKSVerifier(&cfg.Zitadel)
		if err != nil {
			log.Printf("Warning: JWKS verifier not initialized: %v", err)
		} else {
			defer jwksVerifier.Close()
		}
	}

	// Initialize services
	lyricsService := service.NewLyricsService(groqClient)
	renderService := service.NewRenderService(redisClient, asynqClient, storage, &cfg.R2)
	masterService := service.NewMasterService(redisClient, asynqClient, storage, &cfg.R2)
	exportService := service.NewExportService(storage, audioClient, &cfg.R2)
	uploadService := service.NewUploadService(storage, &cfg.R2)

	// Initialize handlers
	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
	renderHandler := handler.NewRenderHandler(renderService, validate)
	masterHandler := handler.NewMasterHandler(masterService, validate)
	exportHandler := handler.NewExportHandler(exportService, validate)
	uploadHandler := handler.NewUploadHandler(uploadService, validate)

	// Mod secimi YAPILANDIRMADAN yapilir, calisma zamani basarisindan
	// degil: bkz. middleware.APIAuthModuSec. Issuer doluyken legacy ve
	// gateway dallari secilemez.
	authModu := middleware.APIAuthModuSec(cfg.Auth.Issuer, cfg.Gateway.Enabled)

	// Initialize auth handler for ForwardAuth verification
	var tokenVerifier auth.TokenVerifier
	if jwksVerifier != nil {
		tokenVerifier = jwksVerifier
	}
	legacySir := cfg.JWT.Secret
	if authModu == middleware.ModOP {
		// OP modunda /auth/verify legacy HMAC zincirini KOSMAMALI.
		// Aksi halde bu uc, varsayilan sirla imzalanmis suresiz bir
		// jetona 200 + X-User-Id/X-User-Email doner; yani Traefik
		// ForwardAuth icin bir kimlik oracle'i olur.
		tokenVerifier = nil
		legacySir = ""
	}
	authHandler := handler.NewAuthHandler(tokenVerifier, legacySir)

	// Initialize middleware (with fallback support)
	var apiAuthMiddleware fiber.Handler
	switch authModu {
	case middleware.ModOP:
		// Kendi OP'umuz: access token'lari onun anahtariyla dogrula.
		// Anahtar surec icinden okunur, kendi JWKS ucumuza ag uzerinden
		// gidilmez. Bu modda gateway veya legacy yola dusulmez.
		if kimlikSunucu == nil {
			// Ulasilamaz: Issuer doluyken Start hatasi yukarida fatal.
			// Yine de nil dereference yerine acik bir hata verelim.
			log.Fatalf("Tutarsiz durum: OP modu secildi ama kimlik sunucusu yok")
		}
		log.Println("Info: kimlik saglayicisi modu — RS256 access token dogrulanacak")
		apiAuthMiddleware = middleware.NewOPAuthMiddleware(
			cfg.Auth.Issuer, cfg.Auth.ClientID, kimlikSunucu.APIAcikAnahtar()).Authenticate()
	case middleware.ModGateway:
		// Behind Traefik: auth is handled by ForwardAuth, read X-User-* headers
		log.Println("Info: Gateway mode enabled — using header-based auth")
		apiAuthMiddleware = middleware.GatewayAuthMiddleware()
	default:
		// Direct mode: auth is handled by the backend itself
		var authMiddleware *middleware.AuthMiddleware
		if jwksVerifier != nil && cfg.JWT.Secret != "" {
			authMiddleware = middleware.NewAuthMiddlewareWithFallback(jwksVerifier, cfg.JWT.Secret)
		} else if jwksVerifier != nil {
			authMiddleware = middleware.NewAuthMiddleware(jwksVerifier)
		} else {
			authMiddleware = middleware.NewLegacyAuthMiddleware(cfg.JWT.Secret)
		}
		apiAuthMiddleware = authMiddleware.Authenticate()
	}
	rateLimiter := middleware.NewRateLimiter(redisClient)

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: customErrorHandler,
		BodyLimit:    50 * 1024 * 1024, // 50MB
	})

	// Global middleware
	app.Use(recover.New())
	isDebug := strings.EqualFold(cfg.Server.LogLevel, "debug")
	logFormat := "[${time}] ${status} - ${latency} ${method} ${path}\n"
	if isDebug {
		// ${reqHeaders} ve ${body} BILINCLI olarak yok: birincisi
		// Authorization header'ini, ikincisi giris/kayit formlarinin duz
		// metin sifresini loga kalici hale getirirdi.
		logFormat = "[${time}] ${status} - ${latency} ${method} ${path} ${queryParams}\n"
		log.Println("Debug logging enabled (istek header'lari ve govdesi loglanmaz)")
	}
	app.Use(logger.New(logger.Config{
		Format: logFormat,
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization",
	}))

	// Base URL - timestamp
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"timestamp": time.Now().Unix(),
		})
	})

	// Health check
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"services": fiber.Map{
				"groq":   groqClient.IsConfigured(),
				"suno":   sunoClient.IsConfigured(),
				"r2":     r2Client != nil,
				"audio":  audioClient.IsConfigured(),
				"auth":   kimlikSunucu != nil || jwksVerifier != nil,
				"kimlik": kimlikSunucu != nil,
			},
		})
	})

	// Swagger UI
	app.Get("/swagger/*", fiberSwagger.HandlerDefault)

	// ForwardAuth verification endpoint (internal, called by Traefik)
	app.Get("/auth/verify", authHandler.Verify)

	// API routes
	api := app.Group("/api", apiAuthMiddleware)

	// Lyrics routes
	lyrics := api.Group("/lyrics", rateLimiter.LyricsLimit(cfg.RateLimit.LyricsPerMin))
	lyrics.Post("/generate", lyricsHandler.Generate)
	lyrics.Post("/rewrite", lyricsHandler.Rewrite)

	// Render routes
	render := api.Group("/render")
	render.Post("/start", rateLimiter.RenderLimit(cfg.RateLimit.RenderPerHour), renderHandler.Start)
	render.Get("/status/:jobId", renderHandler.Status)
	render.Get("/result/:jobId", renderHandler.Result)
	render.Post("/cancel/:jobId", renderHandler.Cancel)

	// Master routes
	master := api.Group("/master", rateLimiter.MasterLimit(cfg.RateLimit.MasterPerHour))
	master.Post("/preview", masterHandler.Preview)
	master.Post("/final", masterHandler.Final)
	master.Get("/status/:jobId", masterHandler.Status)
	master.Get("/result/:jobId", masterHandler.Result)

	// Export routes
	export := api.Group("/export", rateLimiter.ExportLimit(cfg.RateLimit.ExportPerHour))
	export.Post("/mp3", exportHandler.MP3)
	export.Post("/wav", exportHandler.WAV)
	export.Post("/stems", exportHandler.Stems)

	// Upload routes
	upload := api.Group("/upload", rateLimiter.UploadLimit(cfg.RateLimit.UploadPerHour))
	upload.Post("/vocal", uploadHandler.Vocal)
	upload.Delete("/vocal/:takeId", uploadHandler.DeleteVocal)

	// WebSocket routes
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	app.Get("/ws/jobs/:jobId", websocket.New(func(c *websocket.Conn) {
		jobID := c.Params("jobId")
		hub.HandleConnection(c, jobID)
	}))

	// Start Asynq worker server
	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down server...")
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	// Start server
	addr := ":" + cfg.Server.Port
	log.Printf("Server starting on %s", addr)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func startWorkerServer(
	cfg *config.Config,
	redisClient *redis.Client,
	renderService *service.RenderService,
	masterService *service.MasterService,
	sunoClient *client.SunoClient,
	audioClient *client.AudioClient,
	r2Client client.StorageClient,
	hub *ws.Hub,
) {
	asynqLogLevel := asynq.InfoLevel
	if strings.EqualFold(cfg.Server.LogLevel, "debug") {
		asynqLogLevel = asynq.DebugLevel
	} else if strings.EqualFold(cfg.Server.LogLevel, "warn") {
		asynqLogLevel = asynq.WarnLevel
	} else if strings.EqualFold(cfg.Server.LogLevel, "error") {
		asynqLogLevel = asynq.ErrorLevel
	}

	srv := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		},
		asynq.Config{
			Concurrency: 10,
			Queues: map[string]int{
				"render": 6,
				"master": 4,
			},
			LogLevel: asynqLogLevel,
		},
	)

	// Create workers with external clients
	renderWorker := worker.NewRenderWorker(renderService, sunoClient, r2Client, hub)
	masterWorker := worker.NewMasterWorker(redisClient, audioClient, r2Client, masterService, hub, &cfg.R2)

	mux := asynq.NewServeMux()
	mux.HandleFunc(service.TaskTypeRender, renderWorker.ProcessTask)
	mux.HandleFunc(service.TaskTypeMaster, masterWorker.ProcessTask)

	if err := srv.Run(mux); err != nil {
		log.Printf("Asynq worker error: %v", err)
	}
}

func customErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"

	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}

	return c.Status(code).JSON(fiber.Map{
		"error": fiber.Map{
			"code":    "SERVICE_ERROR",
			"message": message,
		},
	})
}
