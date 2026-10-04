package kimlik

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"

	"github.com/makeasinger/api/internal/config"
)

// migrationKilitAnahtari, Migrate'i sarmalayan Postgres advisory lock'un
// sabit anahtaridir. Rasgele degil: kumede rolling update sirasinda ayni
// isim/amaca sahip her pod ayni kilidi istemeli. Deger, "make-singer
// kimlik migration" metninin CRC32'sinden turetildi; anlami onemli
// degil, sadece bu uygulamaya ozgu ve sabit olmasi onemli.
const migrationKilitAnahtari int64 = 0x6B696D6C696B31 // "kimlik1" bayt dizisi, okunabilir bir imza

// Sunucu, kendi OpenID Provider'imizin ikinci HTTP dinleyicisini ve ona
// bagli kaynaklari tasir. main.go bunu tek bir Start cagrisi ile kurar ve
// kapanista Kapat ile temizler.
type Sunucu struct {
	srv     *http.Server
	havuz   *pgxpool.Pool
	anahtar *Anahtar

	// hazir, dinleyicinin GERCEKTEN hizmet verip vermedigini tutar.
	// /health eskiden yalnizca "kimlikSunucu != nil" bakiyordu, yani
	// Start dondukten SONRA dinleyicinin cokmesini (orn. kabul
	// dongusunun hata ile durmasi) hic yakalamiyor ve hala
	// "kimlik": true donuyordu: tam kesinti + yanlis saglik sinyali.
	hazir atomic.Bool
}

// Hazir, OP dinleyicisinin su an hizmet verdigini soyler. /health bunu
// kullanir; nil alici (OP hic kurulmadi) icin de guvenle cagrilabilir.
func (s *Sunucu) Hazir() bool {
	return s != nil && s.hazir.Load()
}

// Start, kendi OpenID Provider'imizi ayaga kaldirir ve ikinci bir HTTP
// dinleyicisinde (cfg.Port) discovery belgesini, JWKS'i ve OIDC
// uclarini yayinlamaya baslar. Issuer bos ise hic baslamaz: bos issuer
// ile uretilen discovery belgesi kullanilamaz ve API eski auth yolunda
// kalmalidir. env, cfg.Server.Env'dir (SERVER_ENV); issuer http:// ise
// guvensiz modun sadece yerel gelistirmede acilabilmesi icin kullanilir
// (bkz. issuerGuvensizIzniDogrula).
func Start(ctx context.Context, cfg *config.AuthConfig, rdb *redis.Client, env string) (*Sunucu, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("issuer bos: kimlik saglayicisi baslatilamaz")
	}

	if err := issuerGuvensizIzniDogrula(cfg.Issuer, env); err != nil {
		return nil, err
	}

	if err := yapilandirmaDogrula(cfg); err != nil {
		return nil, err
	}

	anahtar, err := AnahtarYukle(cfg.SigningKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("imzalama anahtari yuklenemedi: %w", err)
	}

	cryptoAnahtar, err := CryptoAnahtar(cfg.CryptoKey)
	if err != nil {
		return nil, fmt.Errorf("crypto anahtari gecersiz: %w", err)
	}

	havuz, err := Baglan(ctx, cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("veritabanina baglanilamadi: %w", err)
	}

	if err := migrateKilitli(ctx, havuz); err != nil {
		havuz.Close()
		return nil, fmt.Errorf("migration basarisiz: %w", err)
	}

	kullaniciDepo := NewPostgresUserStore(havuz)
	jetonDepo := NewPostgresTokenStore(havuz, rdb)
	istekDepo := NewIstekDepo(rdb)

	depo := NewDepo(cfg, kullaniciDepo, jetonDepo, istekDepo, anahtar)
	depo.SaglikBagla(havuz, rdb)

	opCfg := &op.Config{
		CryptoKey: cryptoAnahtar,
		// DIKKAT: CodeMethodS256 YALNIZCA discovery belgesini besler
		// (pkg/op/op.go CodeMethodS256Supported -> pkg/op/discovery.go);
		// hicbir dogrulamada kullanilmaz, yani tek basina HICBIR SEY
		// ZORLAMAZ. S256 zorunlulugunu BIZ zorluyoruz, iki katmanda:
		//   1. /authorize: pkceZorlayici (pkce.go), op.AuthorizeValidator
		//      uzanti noktasi uzerinden — code_challenge yoksa veya
		//      method S256 degilse (bos dahil) istek reddedilir.
		//   2. Token ucu: AuthIstek.GetCodeChallenge (istek.go)
		//      fail-closed; S256 disindaki method'da nil doner ve
		//      kutuphane public client'ta "PKCE required" ile reddeder.
		CodeMethodS256:        true,
		AuthMethodPost:        false, // public client, secret yok
		GrantTypeRefreshToken: true,
		SupportedUILocales:    []language.Tag{language.Turkish, language.English},
		SupportedScopes: []string{
			oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess,
		},
	}

	// issuerGuvensizIzniDogrula yukarida gecti; yani buraya http://
	// issuer ile gelindiyse guvensiz mod bilerek izin verilmis demektir
	// (yerel gelistirme / Docker ile elle dogrulama). Kutuphane
	// varsayilan olarak https disinda issuer kabul etmez.
	var opOpts []op.Option
	if strings.HasPrefix(cfg.Issuer, "http://") {
		opOpts = append(opOpts, op.WithAllowInsecure())
	}

	saglayici, err := op.NewOpenIDProvider(cfg.Issuer, opCfg, depo, opOpts...)
	if err != nil {
		havuz.Close()
		return nil, fmt.Errorf("OpenID Provider kurulamadi: %w", err)
	}

	// Cerezin Secure bayragi issuer'a gore: https'te ACIK, http://
	// yerel gelistirmede KAPALI (aksi halde tarayici cerezi hic
	// saklamaz ve yerel akis kirilir).
	cerezGuvenli := !strings.HasPrefix(cfg.Issuer, "http://")

	// Hiz limiti: POST /giris ve POST /kayit icin. Kurulamazsa Start
	// HATA DONER (fail-closed): limitsiz bir giris ucu ile ayaga kalkmak
	// hem sinirsiz brute-force hem de ana API'yi de dusuren bir CPU DoS
	// yuzeyi demektir. Bkz. hizlimit.go.
	hizLimit, err := NewHizLimit(rdb, HizLimitAyar{
		GirisIPPerMin:      cfg.LoginIPPerMin,
		GirisEpostaPerSaat: cfg.LoginEmailPerHour,
		KayitIPPerSaat:     cfg.SignupIPPerHour,
		GuvenilenProxy:     cfg.TrustedProxies,
	}, cryptoAnahtar[:])
	if err != nil {
		havuz.Close()
		return nil, fmt.Errorf("hiz limiti kurulamadi: %w", err)
	}

	mux := muxKur(saglayici, kullaniciDepo, istekDepo, cerezGuvenli, hizLimit)

	// Dinleyici Start ICINDE, SENKRON acilir. Eskiden ListenAndServe bir
	// goroutine icinde cagriliyordu; port cakismasi gibi bir hata Start
	// DONDUKTEN SONRA sessizce olusuyor, /health bunu yakalamiyordu.
	// Simdi boyle bir hata Start'i basarisiz kilar ve main.go log.Fatalf
	// ile durur: CrashLoopBackOff dogru ve gorunur sinyaldir.
	dinleyici, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		havuz.Close()
		return nil, fmt.Errorf("kimlik dinleyicisi acilamadi (port %s): %w", cfg.Port, err)
	}

	srv := &http.Server{
		Handler: mux,
		// M8: yalnizca ReadHeaderTimeout vardi; govde ve yanit icin sure
		// siniri YOKTU, yani yavas govde gonderen bir istemci baglantiyi
		// (ve goroutine'i) sure siz tutabiliyordu (Slowloris benzeri).
		// Degerler ucun gercek maliyetine gore: en pahali istek bcrypt
		// ile ~258 ms, formlar birkac yuz bayt.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sunucu := &Sunucu{srv: srv, havuz: havuz, anahtar: anahtar}
	sunucu.hazir.Store(true)
	go func() {
		if err := srv.Serve(dinleyici); err != nil && err != http.ErrServerClosed {
			// Burada log.Fatal cagirmiyoruz: bu goroutine ana akistan
			// ayrik, panik yerine sessizce durur. Ama ARTIK sessiz
			// degil: hazir=false yazilir, /health bunu "kimlik": false
			// olarak gosterir ve kume pod'u saglikli saymaz.
			sunucu.hazir.Store(false)
			log.Printf("kimlik dinleyicisi durdu: %v", err)
		}
	}()

	return sunucu, nil
}

// yapilandirmaDogrula, OP'nin HIC BASLAMAMASI gereken yapilandirma
// hatalarini toplar. Hepsi canli olarak dogrulandi: uc durumun
// ucunde de Start eskiden HATASIZ donuyordu.
//
//   - ClientID bos: middleware.NewOPAuthMiddleware kurulumHatasi'na
//     duser ve HER /api/* istegini 401 reddeder, ama /health hala
//     "kimlik": true / "auth": true donerdi — tam kesinti + yanlis
//     saglik sinyali. config.yaml bunun yasak oldugunu yaziyordu ama
//     kod zorlamiyordu.
//   - RedirectURIs bos: her /authorize reddedilir, hic kullanici giris
//     yapamaz.
//   - AccessTTL <= 0: AccessKaydet kaydi hic yazmaz (jeton_depo.go) ve
//     access token exp=now ile uretilir => her /api/* 401, her
//     /userinfo 403.
//   - RefreshTTL <= 0: her refresh kaydi dogdugu an olu => tum
//     kullanicilar aninda disari atilir.
//
// Sure degerleri 0 olmasi UYDURMA bir senaryo degil: viper, cozulemeyen
// bir sure degerinde (orn. "15min", "60d") varsayilana DUSMEZ, sessizce
// 0s verir (bkz. config.go'daki not).
func yapilandirmaDogrula(cfg *config.AuthConfig) error {
	if cfg.ClientID == "" {
		return fmt.Errorf("client_id bos: AUTH_CLIENT_ID zorunlu (bos iken her /api/* istegi 401 reddedilir)")
	}
	if len(cfg.RedirectURIs) == 0 {
		return fmt.Errorf("redirect_uris bos: AUTH_REDIRECT_URIS zorunlu (bos iken her /authorize reddedilir)")
	}
	for _, uri := range cfg.RedirectURIs {
		if strings.TrimSpace(uri) == "" {
			return fmt.Errorf("redirect_uris bos bir girdi iceriyor: AUTH_REDIRECT_URIS degerini kontrol edin")
		}
	}
	if cfg.AccessTTL <= 0 {
		return fmt.Errorf("access_ttl pozitif olmali, cozulen deger: %s (AUTH_ACCESS_TTL gecerli bir sure olmali, orn. 15m)", cfg.AccessTTL)
	}
	if cfg.RefreshTTL <= 0 {
		return fmt.Errorf("refresh_ttl pozitif olmali, cozulen deger: %s (AUTH_REFRESH_TTL gecerli bir sure olmali, orn. 1440h)", cfg.RefreshTTL)
	}
	return nil
}

// muxKur, OP'nin HTTP yuzeyini kurar. Start'tan ayri bir fonksiyon:
// testler ayni kablolamayi (sahte depolarla) kurup gercek bir
// dinleyici uzerinde akisi ucan uca olcebilsin.
//
// Yol ONCELIKLERI (Go 1.22+ ServeMux spesiflik kurali): "/authorize" ve
// "/authorize/callback" TAM yol desenleridir ve "/" subtree desenini
// yener; yani ikisi de kutuphaneye gitmek yerine bizim sarmalayicimizdan
// gecer. Kutuphane kodu DEGISTIRILMEZ veya KOPYALANMAZ: her iki
// sarmalayici dogrulamadan sonra kutuphanenin kendi handler'ina delege
// eder.
func muxKur(
	saglayici *op.Provider,
	kullaniciDepo UserStore,
	istekDepo *IstekDepo,
	cerezGuvenli bool,
	hizLimit *HizLimit,
) *http.ServeMux {
	mux := http.NewServeMux()

	// /giris ve /kayit sayfalarini baglar. POST handler'lari
	// op.NewIssuerInterceptor ile sarilir: araci issuer'i istek
	// baglamina koyar, geriCagirma (op.AuthCallbackURL) onu oradan
	// okur. Sarmadan baglamak akisi yonlendirme asamasinda kirar.
	araci := op.NewIssuerInterceptor(saglayici.IssuerFromRequest)
	sayfalar := NewSayfalar(
		kullaniciDepo,
		istekDepo,
		istekDepo, // IstekDepo ayni zamanda OturumDepo'yu karsilar
		op.AuthCallbackURL(saglayici),
		cerezGuvenli,
	)
	sayfalar.CallbackDelege(http.HandlerFunc(op.AuthorizeCallbackHandler(saglayici)))
	sayfalar.Bagla(mux, araci, hizLimit)

	yolAuthorize := saglayici.AuthorizationEndpoint().Relative()

	// /authorize: kutuphanenin AuthorizeValidator uzanti noktasiyla
	// S256 PKCE zorunlu kilinir (bkz. pkce.go). op.Authorize'i kendimiz
	// cagiriyoruz cunku kutuphanenin kendi router'i validator'u degil
	// saglayicinin kendisini kullanir.
	zorlayici := &pkceZorlayici{Provider: saglayici}
	mux.Handle(yolAuthorize, araci.Handler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			op.Authorize(w, r, zorlayici)
		})))

	// /authorize/callback: cerez baglamasi dogrulanmadan kutuphaneye
	// GECILMEZ (bkz. Sayfalar.Callback).
	mux.HandleFunc(yolAuthorize+"/callback", araci.HandlerFunc(sayfalar.Callback))

	// Discovery belgesi: kutuphanenin urettigini AYNEN yayinlamiyoruz,
	// desteklenmeyen akislari ayikliyoruz (bkz. discoveryDuzelt).
	mux.Handle(oidc.DiscoveryEndpoint, araci.Handler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			belge := op.CreateDiscoveryConfig(r.Context(), saglayici, saglayici.Storage())
			discoveryDuzelt(belge)
			op.Discover(w, belge)
		})))

	// Kok yol en sona baglanir: kutuphanenin diger uclari
	// (/oauth/token, /userinfo, /keys, /end_session) buradan gecer.
	mux.Handle("/", saglayici)

	return mux
}

// discoveryDuzelt, discovery belgesini GERCEKTEN destekledigimiz akislara
// indirir.
//
// NEDEN: zitadel/oidc v3.51.8 bu alanlari yapilandirmadan OKUMAZ,
// SABIT uretir (pkg/op/discovery.go: ResponseTypes hep code + id_token +
// "id_token token" doner; GrantTypes hep implicit ekler;
// GrantTypeJWTAuthorizationSupported sabit true doner) ve
// device_authorization_endpoint'i kosulsuz yazar. op.Config'de bunlari
// kisitlayan bir alan YOK, bu yuzden belgeyi yayindan once kendimiz
// duzeltiyoruz. Kutuphane kodu DEGISTIRILMEZ veya KOPYALANMAZ: belge
// kutuphanenin kendi op.CreateDiscoveryConfig'i ile uretilir, biz
// yalnizca yanlis alanlari ayikliyoruz.
//
// Bu bir GUVENLIK duzeltmesi DEGIL: inceleyici implicit, jwt-bearer ve
// device akislarinin hepsini denedi, hepsi dogru reddediliyor. Duzeltme
// UYUMLULUK icin: metadata'ya guvenip implicit deneyen uyumlu bir RP
// gereksiz yere hata alirdi.
func discoveryDuzelt(belge *oidc.DiscoveryConfiguration) {
	// Yalnizca authorization code akisi (PKCE S256 zorunlu, bkz.
	// pkce.go). id_token / "id_token token" (implicit) desteklenmiyor.
	belge.ResponseTypesSupported = []string{string(oidc.ResponseTypeCode)}

	// Desteklenen grant'lar: code + refresh_token. Kutuphanenin kosulsuz
	// ekledigi implicit ve urn:ietf:params:oauth:grant-type:jwt-bearer
	// burada DUSER.
	belge.GrantTypesSupported = []oidc.GrantType{
		oidc.GrantTypeCode,
		oidc.GrantTypeRefreshToken,
	}

	// Device authorization grant uygulanmadi (Depo
	// op.DeviceAuthorizationStorage'i karsilamiyor), ucu ilan etmek
	// yanlis.
	belge.DeviceAuthorizationEndpoint = ""
}

// issuerGuvensizIzniDogrula, issuer http:// ile basliyorsa guvensiz moda
// (op.WithAllowInsecure) izin verilip verilmeyecegine karar verir.
// Kutuphane (zitadel/oidc) host'a bakmadan yalnizca semaya gore karar
// verir; bu yuzden guvensiz izni BIZ, iki kosul BIRLIKTE saglandiginda
// veriyoruz:
//  1. env production degil (SERVER_ENV=production ise izin YOK).
//  2. issuer'in host kismi localhost, 127.0.0.1 veya ::1 (port'lu
//     haller dahil, url.Hostname() ile cozulur).
//
// Aksi halde https zorunlu hatasi doner ve Start hicbir baglanti
// acmadan durur. Issuer sir degildir, hata mesajinda ve uyari
// loglarinda acikca gecebilir.
func issuerGuvensizIzniDogrula(issuer, env string) error {
	if !strings.HasPrefix(issuer, "http://") {
		return nil
	}
	if env == "production" {
		return fmt.Errorf("issuer http: production ortaminda https zorunlu, guvensiz mod izinli degil: %s", issuer)
	}
	u, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("issuer cozulemedi: %w", err)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return fmt.Errorf("issuer http: yalnizca localhost/127.0.0.1/::1 icin guvensiz moda izin verilir, https zorunlu: %s", issuer)
	}
	log.Printf("UYARI: issuer http ve yerel, GUVENSIZ mod acik: %s", issuer)
	return nil
}

// migrateKilitli, Migrate'i bir Postgres advisory lock ile sarar. Kumede
// rolling update sirasinda iki pod es zamanli Migrate kosarsa ikisi de
// "uygulanmadi" kontrolunu gecebilir ve schema_migrations birincil
// anahtar cakismasina duser; kilit bunu engeller. Kilidi ayni baglanti
// uzerinden alip birakmak icin havuzdan tek bir baglanti odunc aliyoruz.
func migrateKilitli(ctx context.Context, havuz *pgxpool.Pool) error {
	baglanti, err := havuz.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migration icin baglanti alinamadi: %w", err)
	}
	defer baglanti.Release()

	if _, err := baglanti.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationKilitAnahtari); err != nil {
		return fmt.Errorf("migration kilidi alinamadi: %w", err)
	}
	defer func() {
		// Kilidi ayni baglanti uzerinden birak; birakma hatasi migration
		// sonucunu degistirmez ama sessizce yutulmamali, iz birakilmali.
		if _, err := baglanti.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationKilitAnahtari); err != nil {
			log.Printf("migration kilidi birakilamadi: %v", err)
		}
	}()

	return Migrate(ctx, havuz)
}

// Kapat, HTTP dinleyicisini nazikce durdurur ve veritabani havuzunu
// kapatir. Havuz, dinleyici kapanisi hata verse bile HER YOLDA kapatilir;
// aksi halde Shutdown hatasinda havuz sizar.
func (s *Sunucu) Kapat(ctx context.Context) error {
	// Kapanis baslar baslamaz /health "kimlik": false demeli.
	s.hazir.Store(false)
	shutdownErr := s.srv.Shutdown(ctx)
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("dinleyici kapatilamadi: %w", shutdownErr)
	}
	s.havuz.Close()
	return shutdownErr
}

// APIAcikAnahtar, access token dogrulamasi icin API tarafinin kullanacagi
// ham RSA acik anahtarini dondurur. Anahtar.AcikAnahtar()'dan bilincli
// olarak farkli: o JWKS yayini icin op.Key sarmalayicisi doner, bu ise
// dogrudan *rsa.PublicKey.
func (s *Sunucu) APIAcikAnahtar() *rsa.PublicKey {
	return &s.anahtar.ozel.PublicKey
}
