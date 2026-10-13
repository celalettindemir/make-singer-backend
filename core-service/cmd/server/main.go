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
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/handler"
	"github.com/makeasinger/api/internal/identity"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/social"
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

	// Kendi OpenID Provider'imiz: /api/* icin TEK kimlik dogrulama yolu.
	// Issuer zorunludur; eski legacy HMAC / Zitadel / gateway yollari
	// kaldirildi, cunku her biri OP'suz bir "yedek" mod olarak yapilandirma
	// hatasinda /api'yi zayif ya da suresiz jetonlara aciyordu.
	//
	// Baslatma hatasi FATALDIR. Her istege 401 donen ama /health'te
	// saglikli gorunen bir pod sessiz bir kesintidir; CrashLoopBackOff
	// dogru ve gorunur sinyal, yeniden baslatma da gecici kesinti (orn.
	// Postgres) icin dogru kurtarma.
	if cfg.Auth.Issuer == "" {
		log.Fatalf("AUTH_ISSUER zorunlu: kimlik saglayicisi olmadan /api korunamaz")
	}
	kimlikSunucu, err := identity.Start(ctx, &cfg.Auth, redisClient, cfg.Server.Env)
	if err != nil {
		log.Fatalf("Kimlik saglayicisi baslatilamadi, servis baslatilmiyor: %v", err)
	}
	log.Printf("Kimlik saglayicisi %s uzerinde, issuer %s", cfg.Auth.Port, cfg.Auth.Issuer)
	defer func() {
		kapatCtx, iptal := context.WithTimeout(context.Background(), 10*time.Second)
		defer iptal()
		_ = kimlikSunucu.Shutdown(kapatCtx)
	}()

	// Kesfet (sosyal): kimlikle ayni veritabani, ama kendi havuzu, kendi
	// semasi (sosyal) ve kendi migration tablosu. Kimlik gibi FATAL:
	// sosyal tablolari olmayan bir pod her /api/social isteginde 500 doner.
	sosyalHavuz, err := identity.Connect(ctx, cfg.Auth.DBURL)
	if err != nil {
		log.Fatalf("Sosyal veritabanina baglanilamadi: %v", err)
	}
	defer sosyalHavuz.Close()
	if err := social.Migrate(ctx, sosyalHavuz); err != nil {
		log.Fatalf("Sosyal migration basarisiz: %v", err)
	}
	sosyalDepo := social.NewPostgresStore(sosyalHavuz)

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

	// Initialize services
	lyricsService := service.NewLyricsService(groqClient)
	renderService := service.NewRenderService(redisClient, asynqClient, storage, &cfg.R2)
	masterService := service.NewMasterService(redisClient, asynqClient, storage, &cfg.R2)
	exportService := service.NewExportService(storage, audioClient, &cfg.R2)
	uploadService := service.NewUploadService(storage, &cfg.R2)
	fileService := service.NewFileService(storage, &cfg.R2)
	wsTicketService := service.NewWSTicketService(redisClient)

	// silici: arayuz degiskeni; r2Client nil iken dogrudan atamak tipli
	// nil tuzagini kurar (bkz. storage).
	var silici client.PrefixDeleter
	if r2Client != nil {
		silici = r2Client
	}

	// Hesap silme: kullanici kaydi kendi OP'mizde tutulur.
	hesapService := service.NewAccountService(kimlikSunucu, silici, redisClient, asynqClient, sosyalDepo)
	hesapHandler := handler.NewAccountHandler(hesapService)

	// kopyalayici: arayuz degiskeni; r2Client nil iken dogrudan atamak
	// tipli nil tuzagini kurar (bkz. storage).
	var kopyalayici client.Copier
	if r2Client != nil {
		kopyalayici = r2Client
	}
	sosyalHandler := handler.NewSosyalHandler(
		social.NewService(sosyalDepo, asynqClient, storage, &cfg.R2).WithCopier(kopyalayici))
	sosyalWorker := worker.NewSocialWorker(sosyalDepo, kopyalayici, audioClient, silici, deletionmark.NewRedisChecker(redisClient))

	// Initialize handlers
	lyricsHandler := handler.NewLyricsHandler(lyricsService, validate)
	renderHandler := handler.NewRenderHandler(renderService, validate)
	masterHandler := handler.NewMasterHandler(masterService, validate)
	exportHandler := handler.NewExportHandler(exportService, validate)
	uploadHandler := handler.NewUploadHandler(uploadService, validate)
	fileHandler := handler.NewFileHandler(fileService, validate)
	wsTicketHandler := handler.NewWSTicketHandler(wsTicketService, validate)

	// /api/* icin tek yol: kendi OP'umuzun RS256 access token'i. Acik
	// anahtar surec icinden okunur, JWKS ucuna ag uzerinden gidilmez.
	apiAuthMiddleware := middleware.NewOPAuthMiddleware(
		cfg.Auth.Issuer, cfg.Auth.ClientID, kimlikSunucu.APIPublicKey()).Authenticate()
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
		// GERCEK hazirlik: "kimlikSunucu != nil" (yani Start basariyla
		// dondu mu) YETERLI DEGIL — dinleyici sonradan cokerse pod hala
		// "saglikli" gorunuyordu. Hazir(), dinleyicinin su an hizmet
		// verdigini soyler ve nil alici icin de guvenlidir.
		kimlikHazir := kimlikSunucu.Ready()

		// OP olmadan servis islevsizdir: her /api/* istegi 401 doner.
		// Boyle bir pod "ok" demeyi birakmali, yoksa yanlis saglik
		// sinyali sessiz bir kesintiyi gizler.
		durum := "ok"
		if !kimlikHazir {
			durum = "degraded"
			c.Status(fiber.StatusServiceUnavailable)
		}

		return c.JSON(fiber.Map{
			"status": durum,
			"services": fiber.Map{
				"groq":   groqClient.IsConfigured(),
				"suno":   sunoClient.IsConfigured(),
				"r2":     r2Client != nil,
				"audio":  audioClient.IsConfigured(),
				"auth":   kimlikHazir,
				"kimlik": kimlikHazir,
			},
		})
	})

	// Swagger UI
	app.Get("/swagger/*", fiberSwagger.HandlerDefault)

	// Kesfet herkese acik onizleme (oturumsuz, spec §4.8). /api auth
	// grubundan ONCE kaydedilir: Fiber rotalari kayit sirasiyla eslestirir
	// ve bu uc zinciri surdurmez, yani auth ve silinmis-kullanici kapisi
	// hic calismaz. Sinir istemci IP'si basinadir (X-Real-Ip); web
	// sunucusu X-Web-Anahtari ile ortak ve daha yuksek sinira gecer.
	sosyalHandler.PublicRotalar(app.Group("/api/public"),
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

	// WebSocket bileti: kimligi dogrulanmis istekle alinir.
	api.Post("/ws/ticket", rateLimiter.WSTicketLimit(cfg.RateLimit.WSTicketPerMin), wsTicketHandler.Issue)

	// Kayitli dosya adreslerini tazeler (imza 1 saatte dolar).
	api.Post("/files/sign", rateLimiter.SignLimit(cfg.RateLimit.SignPerMin), fileHandler.Sign)

	api.Delete("/account", hesapHandler.Delete)

	// Kesfet (sosyal) uclari. Limitler yalnizca ilgili uclardadir; yorum
	// limiti sozlesme geregi 429 yavasla doner.
	sosyalHandler.Rotalar(api.Group("/social"), handler.SosyalLimitleri{
		Yayin: rateLimiter.Limit("sosyal-yayin", cfg.RateLimit.SocialPublicationPerHour, time.Hour),
		Yorum: rateLimiter.LimitWithCode("sosyal-yorum", cfg.RateLimit.SocialCommentPerMin, time.Minute,
			"yavasla", "Biraz yavasla"),
		Cover: rateLimiter.Limit("sosyal-cover", cfg.RateLimit.SocialCoverPerHour, time.Hour),
	})

	// WebSocket routes
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	// Bilet upgrade'den once tuketilir; bilet yoksa ya da baska isinse 401.
	app.Get("/ws/jobs/:jobId", wsTicketHandler.Gate, websocket.New(func(c *websocket.Conn) {
		jobID := c.Params("jobId")
		hub.HandleConnection(c, jobID)
	}))

	// Start Asynq worker server
	go startWorkerServer(cfg, redisClient, renderService, masterService, sunoClient, audioClient, storage, hub, hesapService, sosyalWorker)

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
	hesapService *service.AccountService,
	sosyalWorker *worker.SocialWorker,
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
	// Isciler is sahibinin silinmis isaretini ucretli Suno cagrisindan ve
	// her R2 yazimindan once denetler.
	hesapDenetci := deletionmark.NewRedisChecker(redisClient)
	renderWorker := worker.NewRenderWorker(renderService, sunoClient, r2Client, hub, hesapDenetci)
	masterWorker := worker.NewMasterWorker(redisClient, audioClient, r2Client, masterService, hub, &cfg.R2, hesapDenetci)

	mux := asynq.NewServeMux()
	mux.HandleFunc(service.TaskTypeRender, renderWorker.ProcessTask)
	mux.HandleFunc(service.TaskTypeMaster, masterWorker.ProcessTask)
	mux.HandleFunc(service.TaskTypeAccountCleanup, worker.NewAccountWorker(hesapService).ProcessTask)
	mux.HandleFunc(social.TaskTypePrepare, sosyalWorker.Prepare)
	mux.HandleFunc(social.TaskTypeDeleteFiles, sosyalWorker.DeleteFiles)

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
