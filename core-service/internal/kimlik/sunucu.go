package kimlik

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
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
// kalmalidir.
func Start(ctx context.Context, cfg *config.AuthConfig, rdb *redis.Client) (*Sunucu, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("issuer bos: kimlik saglayicisi baslatilamaz")
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
		CryptoKey:             cryptoAnahtar,
		CodeMethodS256:        true,  // PKCE S256 zorunlu
		AuthMethodPost:        false, // public client, secret yok
		GrantTypeRefreshToken: true,
		SupportedUILocales:    []language.Tag{language.Turkish, language.English},
		SupportedScopes: []string{
			oidc.ScopeOpenID, oidc.ScopeProfile, oidc.ScopeEmail, oidc.ScopeOfflineAccess,
		},
	}

	var opOpts []op.Option
	if strings.HasPrefix(cfg.Issuer, "http://") {
		// Kutuphane varsayilan olarak https disinda issuer kabul etmez.
		// Yerel gelistirme ve Docker ile elle dogrulama http kullanir;
		// uretimde Issuer https oldugu icin bu dal hic devreye girmez.
		opOpts = append(opOpts, op.WithAllowInsecure())
	}

	saglayici, err := op.NewOpenIDProvider(cfg.Issuer, opCfg, depo, opOpts...)
	if err != nil {
		havuz.Close()
		return nil, fmt.Errorf("OpenID Provider kurulamadi: %w", err)
	}

	// /giris ve /kayit sayfa baglamasi ile op.NewIssuerInterceptor kurulumu
	// sonraki gorevin isi (Sayfalar tipi orada dogar). Bu asamada mux
	// yalnizca saglayiciyi kok yola baglar; discovery ve JWKS bununla
	// calisir, /giris ise henuz 404 doner ve bu beklenen bir durumdur.
	mux := http.NewServeMux()
	mux.Handle("/", saglayici)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Burada log.Fatal cagirmiyoruz: bu goroutine ana akistan
			// ayrik, panik yerine sessizce durur; cagiran tarafin
			// /health uzerinden fark etmesi beklenir.
			_ = err
		}
	}()

	return &Sunucu{srv: srv, havuz: havuz, anahtar: anahtar}, nil
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
		// sonucunu degistirmez ama sessizce yutulmamali.
		if _, err := baglanti.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationKilitAnahtari); err != nil {
			_ = err
		}
	}()

	return Migrate(ctx, havuz)
}

// Kapat, HTTP dinleyicisini nazikce durdurur ve veritabani havuzunu
// kapatir.
func (s *Sunucu) Kapat(ctx context.Context) error {
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("dinleyici kapatilamadi: %w", err)
	}
	s.havuz.Close()
	return nil
}

// APIAcikAnahtar, access token dogrulamasi icin API tarafinin kullanacagi
// ham RSA acik anahtarini dondurur. Anahtar.AcikAnahtar()'dan bilincli
// olarak farkli: o JWKS yayini icin op.Key sarmalayicisi doner, bu ise
// dogrudan *rsa.PublicKey.
func (s *Sunucu) APIAcikAnahtar() *rsa.PublicKey {
	return &s.anahtar.ozel.PublicKey
}
