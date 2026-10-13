package main

import (
	"context"
	"errors"
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
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/identity"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/social"
	ws "github.com/makeasinger/api/internal/websocket"
	"github.com/makeasinger/api/internal/worker"
	"github.com/makeasinger/api/pkg/response"
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
	if cfg.Server.APIDomain != "" {
		docs.SwaggerInfo.Host = cfg.Server.APIDomain
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

	// Our own OpenID Provider: the ONLY authentication path for /api/*.
	// The issuer is mandatory; the old legacy HMAC / Zitadel / gateway paths
	// were removed because each one was an OP-less "fallback" mode that, on a
	// misconfiguration, opened /api to weak or non-expiring tokens.
	//
	// A startup failure is FATAL. A pod that answers every request with 401
	// but looks healthy on /health is a silent outage; CrashLoopBackOff is
	// the correct, visible signal, and a restart is the correct recovery
	// from a transient outage (e.g. Postgres).
	if cfg.Auth.Issuer == "" {
		log.Fatalf("AUTH_ISSUER is required: /api cannot be protected without an identity provider")
	}
	identityServer, err := identity.Start(ctx, &cfg.Auth, redisClient, cfg.Server.Env)
	if err != nil {
		log.Fatalf("Could not start the identity provider, the service is not starting: %v", err)
	}
	log.Printf("Identity provider at %s, issuer %s", cfg.Auth.Port, cfg.Auth.Issuer)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = identityServer.Shutdown(shutdownCtx)
	}()

	// Discover (social): same database as identity, but its own pool, its own
	// schema (sosyal) and its own migration table. FATAL like identity:
	// a pod without the social tables answers every /api/social request with 500.
	socialPool, err := identity.Connect(ctx, cfg.Auth.DBURL)
	if err != nil {
		log.Fatalf("Could not connect to the social database: %v", err)
	}
	defer socialPool.Close()
	if err := social.Migrate(ctx, socialPool); err != nil {
		log.Fatalf("Social migration failed: %v", err)
	}
	socialStore := social.NewPostgresStore(socialPool)

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

	// storage: interface variable. Assigning r2Client directly to the
	// interface while it is nil sets the "typed nil pointer" trap (the
	// interface is not nil but the pointer inside it is), so we split the
	// assignment explicitly.
	var storage client.StorageClient
	if r2Client != nil {
		storage = r2Client
	}

	// Initialize services
	lyricsService := service.NewLyricsService(groqClient)
	renderService := service.NewRenderService(redisClient, asynqClient, storage, &cfg.R2)
	masterService := service.NewMasterService(redisClient, asynqClient, storage, &cfg.R2)
	exportService := service.NewExportService(storage, audioClient, &cfg.R2)
	uploadService := service.NewUploadService(storage, &cfg.R2)
	fileService := service.NewFileService(storage, &cfg.R2)
	wsTicketService := service.NewWSTicketService(redisClient)

	// prefixDeleter: interface variable; assigning r2Client directly while
	// it is nil sets the typed-nil trap (see storage).
	var prefixDeleter client.PrefixDeleter
	if r2Client != nil {
		prefixDeleter = r2Client
	}

	// Account deletion: the user record is kept in our own OP.
	accountService := service.NewAccountService(identityServer, prefixDeleter, redisClient, asynqClient, socialStore)
	accountHandler := handler.NewAccountHandler(accountService)

	// copier: interface variable; assigning r2Client directly while it is
	// nil sets the typed-nil trap (see storage).
	var copier client.Copier
	if r2Client != nil {
		copier = r2Client
	}
	socialHandler := handler.NewSocialHandler(
		social.NewService(socialStore, asynqClient, storage, &cfg.R2).WithCopier(copier))
	socialWorker := worker.NewSocialWorker(socialStore, copier, audioClient, prefixDeleter, deletionmark.NewRedisChecker(redisClient))

	// Initialize handlers
	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
	renderHandler := handler.NewRenderHandler(renderService, validate)
	masterHandler := handler.NewMasterHandler(masterService, validate)
	exportHandler := handler.NewExportHandler(exportService, validate)
	uploadHandler := handler.NewUploadHandler(uploadService, validate)
	fileHandler := handler.NewFileHandler(fileService, validate)
	wsTicketHandler := handler.NewWSTicketHandler(wsTicketService, validate)

	// The only path for /api/*: our own OP's RS256 access token. The public
	// key is read in-process; the JWKS endpoint is not fetched over the network.
	apiAuthMiddleware := middleware.NewOPAuthMiddleware(
		cfg.Auth.Issuer, cfg.Auth.ClientID, identityServer.APIPublicKey()).Authenticate()
	rateLimiter := middleware.NewRateLimiter(redisClient, cfg.RateLimit.Cloudflare)

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
		// ${reqHeaders} and ${body} are deliberately absent: the first would
		// persist the Authorization header in the log, the second the plain
		// text password of login/registration forms.
		logFormat = "[${time}] ${status} - ${latency} ${method} ${path} ${queryParams}\n"
		log.Println("Debug logging enabled (request headers and bodies are not logged)")
	}
	app.Use(logger.New(logger.Config{
		Format: logFormat,
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
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
		// REAL readiness: "identityServer != nil" (i.e. Start returned
		// successfully) is NOT ENOUGH; if the listener crashed later the pod
		// still looked "healthy". Ready() reports whether the listener is
		// serving right now and is also safe for a nil receiver.
		identityReady := identityServer.Ready()

		// Without the OP the service is non-functional: every /api/* request
		// gets 401. Such a pod must stop saying "ok"; otherwise a false
		// health signal hides a silent outage.
		status := "ok"
		if !identityReady {
			status = "degraded"
			c.Status(fiber.StatusServiceUnavailable)
		}

		return c.JSON(fiber.Map{
			"status": status,
			"services": fiber.Map{
				"groq":   groqClient.IsConfigured(),
				"suno":   sunoClient.IsConfigured(),
				"r2":     r2Client != nil,
				"audio":  audioClient.IsConfigured(),
				"auth":   identityReady,
				"kimlik": identityReady,
			},
		})
	})

	// Swagger UI
	app.Get("/swagger/*", fiberSwagger.HandlerDefault)

	// Discover public preview (no session, spec §4.8). Registered BEFORE the
	// /api auth group: Fiber matches routes in registration order and this
	// endpoint does not continue the chain, so the auth and deleted-user
	// gate never run. The limit is per client IP (X-Real-Ip); the web
	// server shares one, higher limit via X-Web-Anahtari.
	socialHandler.PublicRoutes(app.Group("/api/public"),
		rateLimiter.LimitIP("public", cfg.RateLimit.PublicPerMin, time.Minute,
			cfg.Social.WebKey, cfg.RateLimit.PublicWebPerMin))

	// API routes
	api := app.Group("/api", apiAuthMiddleware, middleware.DeletedUserGate(deletionmark.NewRedisChecker(redisClient)))

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

	// WebSocket ticket: obtained with an authenticated request.
	api.Post("/ws/ticket", rateLimiter.WSTicketLimit(cfg.RateLimit.WSTicketPerMin), wsTicketHandler.Issue)

	// Refreshes saved file URLs (the signature expires in 1 hour).
	api.Post("/files/sign", rateLimiter.SignLimit(cfg.RateLimit.SignPerMin), fileHandler.Sign)

	api.Delete("/account", accountHandler.Delete)

	// Discover (social) endpoints. Limits apply only to the relevant
	// endpoints; by contract the comment limit returns 429 "yavasla".
	socialHandler.Routes(api.Group("/social"), handler.SocialLimits{
		Publication: rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SocialPublicationPerHour, time.Hour),
		Comment: rateLimiter.LimitWithCode("sosyal-yorum", cfg.RateLimit.SocialCommentPerMin, time.Minute,
			"yavasla", i18n.KeySlowDown),
		Cover: rateLimiter.Limit("sosyal-cover", cfg.RateLimit.SocialCoverPerHour, time.Hour),
	})

	// WebSocket routes
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	// The ticket is consumed before the upgrade; 401 if there is no ticket or it belongs to another job.
	app.Get("/ws/jobs/:jobId", wsTicketHandler.Gate, websocket.New(func(c *websocket.Conn) {
		jobID := c.Params("jobId")
		hub.HandleConnection(c, jobID)
	}))

	// Start Asynq worker server
	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub, accountService, socialWorker)

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
	storage client.StorageClient,
	hub *ws.Hub,
	accountService *service.AccountService,
	socialWorker *worker.SocialWorker,
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
				"sosyal": 2,
				"bakim":  1,
			},
			LogLevel: asynqLogLevel,
		},
	)

	// Create workers with external clients
	// Workers check the job owner's deleted mark before the paid Suno call
	// and before every R2 write.
	deletionChecker := deletionmark.NewRedisChecker(redisClient)
	renderWorker := worker.NewRenderWorker(renderService, sunoClient, storage, hub, deletionChecker)
	masterWorker := worker.NewMasterWorker(redisClient, audioClient, storage, masterService, hub, &cfg.R2, deletionChecker)

	mux := asynq.NewServeMux()
	mux.HandleFunc(service.TaskTypeRender, renderWorker.ProcessTask)
	mux.HandleFunc(service.TaskTypeMaster, masterWorker.ProcessTask)
	mux.HandleFunc(service.TaskTypeAccountCleanup, worker.NewAccountWorker(accountService).ProcessTask)
	mux.HandleFunc(social.TaskTypePrepare, socialWorker.Prepare)
	mux.HandleFunc(social.TaskTypeDeleteFiles, socialWorker.DeleteFiles)

	if err := srv.Run(mux); err != nil {
		log.Printf("Asynq worker error: %v", err)
	}
}

// customErrorHandler answers errors that reach Fiber (unknown routes,
// oversized bodies, panics caught by recover) in the shared error shape
// with code SERVICE_ERROR and a localized message chosen by status.
func customErrorHandler(c *fiber.Ctx, err error) error {
	code, msg := errorStatusKey(err)
	return response.Error(c, code, response.CodeServiceError, msg, nil)
}

// errorStatusKey maps an error that reached Fiber to its HTTP status and
// message key. Non-Fiber errors are an internal error (500).
func errorStatusKey(err error) (int, i18n.Key) {
	var e *fiber.Error
	if !errors.As(err, &e) {
		return fiber.StatusInternalServerError, i18n.KeyInternal
	}
	switch e.Code {
	case fiber.StatusNotFound:
		return e.Code, i18n.KeyRouteNotFound
	case fiber.StatusMethodNotAllowed:
		return e.Code, i18n.KeyMethodNotAllowed
	case fiber.StatusRequestEntityTooLarge:
		return e.Code, i18n.KeyBodyTooLarge
	case fiber.StatusUpgradeRequired:
		return e.Code, i18n.KeyUpgradeRequired
	default:
		return e.Code, i18n.KeyRequestFailed
	}
}
