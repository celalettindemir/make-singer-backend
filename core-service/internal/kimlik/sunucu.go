package kimlik

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
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

	mux := muxKur(saglayici, kullaniciDepo, istekDepo, cerezGuvenli)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Burada log.Fatal cagirmiyoruz: bu goroutine ana akistan
			// ayrik, panik yerine sessizce durur. NOT: /health yalnizca
			// kimlikSunucu != nil bakiyor (yani Start basariyla dondu
			// mu) — dinleyici SONRADAN burada coker (orn. port
			// cakismasi), /health bunu YAKALAMAZ ve hala "kimlik":true
			// doner. Gercek dinleyici sagligini izlemek Faz 1
			// kapsaminda degil.
			_ = err
		}
	}()

	return &Sunucu{srv: srv, havuz: havuz, anahtar: anahtar}, nil
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
	sayfalar.Bagla(mux, araci)

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

	// Kok yol en sona baglanir: kutuphanenin diger uclari
	// (/oauth/token, /userinfo, /keys, /end_session, discovery)
	// buradan gecer.
	mux.Handle("/", saglayici)

	return mux
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
