package kimlik

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"

	"github.com/makeasinger/api/internal/auth"
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

	// kullanicilar, API tarafindaki hesap silme ucu icin tutulur
	// (bkz. HesapSil).
	kullanicilar UserStore

	// hazir, dinleyicinin GERCEKTEN hizmet verip vermedigini tutar.
	// /health eskiden yalnizca "kimlikSunucu != nil" bakiyordu, yani
	// Start dondukten SONRA dinleyicinin cokmesini (orn. kabul
	// dongusunun hata ile durmasi) hic yakalamiyor ve hala
	// "kimlik": true donuyordu: tam kesinti + yanlis saglik sinyali.
	hazir atomic.Bool

	// fedIptal, yukari akis dogrulayicilarinin JWKS arka plan yenileme
	// ctx'ini iptal eder. SUNUCUNUN omrune baglidir (K7): Kapat onu
	// cagirir, boylece surec yasarken yenileme yasar.
	fedIptal context.CancelFunc

	// dogrulayicilar, kapanista Close edilmek uzere tutulur. Federasyon
	// yapilandirilmamissa BOS olur.
	dogrulayicilar map[string]*auth.UpstreamVerifier
}

// Hazir, OP dinleyicisinin su an hizmet verdigini soyler. /health bunu
// kullanir; nil alici (OP hic kurulmadi) icin de guvenle cagrilabilir.
func (s *Sunucu) Hazir() bool {
	return s != nil && s.hazir.Load()
}

// Start, kendi OpenID Provider'imizi ayaga kaldirir ve ikinci bir HTTP
// dinleyicisinde (cfg.Port) discovery belgesini, JWKS'i ve OIDC
// uclarini yayinlamaya baslar. Issuer bos ise hic baslamaz: bos issuer
// ile uretilen discovery belgesi kullanilamaz (main zaten bos issuer ile
// baslamayi reddeder). env, cfg.Server.Env'dir (SERVER_ENV); issuer http:// ise
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

	saglayici, err := op.NewProvider(opCfg, depo, op.StaticIssuer(cfg.Issuer), opOpts...)
	if err != nil {
		havuz.Close()
		return nil, fmt.Errorf("OpenID Provider kurulamadi: %w", err)
	}

	// Cerezin Secure bayragi issuer'a gore: https'te ACIK, http://
	// yerel gelistirmede KAPALI (aksi halde tarayici cerezi hic
	// saklamaz ve yerel akis kirilir).
	cerezGuvenli := !strings.HasPrefix(cfg.Issuer, "http://")

	// Yukari akis dogrulayicilari (Faz 2). ctx iptal zinciri BILEREK
	// KESILIR (context.WithoutCancel): Start'a verilen ctx bir baslatma
	// zaman asimi olabilir ve o dolunca JWKS'in saatlik arka plan
	// yenilemesi de olurdu. Iptal Kapat'a baglanir, yani yenileme
	// sunucunun omru kadar yasar (K7; gerekcesi
	// auth.NewYukariAkisDogrulayici yorumunda).
	fedCtx, fedIptal := context.WithCancel(context.WithoutCancel(ctx))
	akislar, dogrulayicilar, err := federasyonKur(fedCtx, cfg)
	if err != nil {
		fedIptal()
		havuz.Close()
		return nil, err
	}

	federeDepo := NewPostgresFedereDepo(havuz)

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
		fedIptal()
		havuz.Close()
		return nil, fmt.Errorf("hiz limiti kurulamadi: %w", err)
	}

	mux := muxKur(
		saglayici, kullaniciDepo, istekDepo, federeDepo,
		akislar, dogrulayicilar, cfg.Issuer, cerezGuvenli, hizLimit,
	)

	// Dinleyici Start ICINDE, SENKRON acilir. Eskiden ListenAndServe bir
	// goroutine icinde cagriliyordu; port cakismasi gibi bir hata Start
	// DONDUKTEN SONRA sessizce olusuyor, /health bunu yakalamiyordu.
	// Simdi boyle bir hata Start'i basarisiz kilar ve main.go log.Fatalf
	// ile durur: CrashLoopBackOff dogru ve gorunur sinyaldir.
	dinleyici, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		fedIptal()
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

	sunucu := &Sunucu{
		srv: srv, havuz: havuz, anahtar: anahtar, kullanicilar: kullaniciDepo,
		fedIptal: fedIptal, dogrulayicilar: dogrulayicilar,
	}
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
	// Apple ucu, alanlarin UCU de dolu olmadan calisamaz: client_secret
	// her token degisiminde bu uctan ES256 ile uretilir (bkz. K5,
	// YukariAkis.AppleEkipID/AppleAnahtarID/AppleAnahtarP8). Bu BASTAN
	// baslamama hatasi olmali, kullanicinin Apple ile giris denedigi anda
	// kesfedilen bir sessiz bozukluk degil (Faz 1'in "sessiz sifir"
	// sinifi sorunu burada tekrarlanmiyor).
	for _, f := range cfg.Federation {
		if f.Name == saglayiciApple {
			if f.AppleTeamID == "" || f.AppleKeyID == "" || f.AppleKeyP8 == "" {
				return fmt.Errorf("apple federasyonu yapilandirmasi eksik: AUTH_FED_APPLE_TEAM_ID, AUTH_FED_APPLE_KEY_ID ve AUTH_FED_APPLE_KEY_P8 ucu de zorunlu")
			}
			// "Bos degil" YETMEZ: yukaridaki yorumun yasakladigi sessiz
			// bozukluk tam olarak burada aciliyordu. PEM sirlarinin en sik
			// gercek dagitim hatasi, anahtarin tek satirda ve gercek yeni
			// satirlar yerine literal \n ile mount edilmesidir; boyle bir
			// deger BOS DEGILDIR, yani eski kontrolu GECER, ama
			// pem.Decode nil doner. Sonuc: pod Ready, buton gorunur ve
			// hata ancak kullanici Apple'da Face ID + iki adimli onayi
			// bitirdikten SONRA, anlamsiz bir oturum sayfasi olarak
			// ortaya cikar.
			//
			// Anahtar GERCEKTEN ayristirilir ve bunu appleIstemciSir
			// yapar: .p8 ayristirma mantigi orada TEK bir noktada durur,
			// burada kopyalanmasi iki yerin ayri ayri sasmasi demek olur.
			// Uretilen sir ATILIR; aranan sey yalnizca hatanin kendisi.
			if _, err := appleIstemciSir(f.AppleTeamID, f.AppleKeyID, f.ClientID, f.AppleKeyP8, time.Now()); err != nil {
				// Sarmalanan hatalar anahtar ICERIGI tasimaz (bkz.
				// appleIstemciSir): yalnizca hata sinifi ve anahtar tipi.
				// Mesaj operatore NE yapacagini soyler.
				return fmt.Errorf("apple .p8 anahtari kullanilamaz: AUTH_FED_APPLE_KEY_P8 PKCS#8 PEM bloklu bir ECDSA (P-256) ozel anahtari olmali; degeri GERCEK yeni satirlarla mount edin (tek satirda literal \\n dizisi PEM olarak cozulemez): %w", err)
			}
		}
		// Google'in tek zorunlu sirri client_secret'tir ve hicbir yerde
		// denetlenmiyordu: JetonDegis onu form.Set("client_secret", sir)
		// ile BOS gonderir, Google token ucu 401 invalid_client doner ve
		// kullanici 403 gorur. Pod Ready, metrikler temiz, alarm yok --
		// Apple icin fail-closed baslangic kontrolu varken Google icin
		// olmamasi acik bir asimetriydi.
		//
		// Kural BILEREK "client_id dolu ama secret bos" uzerine kurulu:
		// client_id de bos ise saglayici federasyonIstemcileriOlustur'da
		// listeye HIC girmez (bkz. oradaki sessiz sifir kurali) ve bu bir
		// hata DEGILDIR, "federasyon kapali" demektir.
		if f.Name == saglayiciGoogle && f.ClientSecret == "" {
			return fmt.Errorf("google federasyonu yapilandirmasi eksik: AUTH_FED_GOOGLE_CLIENT_ID verildiyse AUTH_FED_GOOGLE_CLIENT_SECRET de zorunlu (bos iken token degisimi 401 invalid_client ile duser)")
		}
	}
	return nil
}

// yukariAkisSabitleri, Apple ve Google'in adreslerini ve kapsamlarini
// tasir.
//
// Yukari akis adresleri SABITTIR ve koddan gelir; ortamdan YALNIZCA
// kimlik bilgileri okunur. Her giriste discovery cekmek gereksiz bir ag
// turu ve fazladan bir hata kaynagidir; adresleri ortam degiskenine
// birakmak ise yanlis yapilandirilmis bir issuer/JWKS ciftiyle (yani
// baska bir tarafa guvenerek) ayaga kalkabilmek demektir.
var yukariAkisSabitleri = map[string]YukariAkis{
	saglayiciGoogle: {
		Ad:           saglayiciGoogle,
		Issuer:       "https://accounts.google.com",
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		JWKSURL:      "https://www.googleapis.com/oauth2/v3/certs",
		Kapsamlar:    []string{"openid", "email", "profile"},
		PKCEDestekli: true,
		FormPost:     false,
	},
	saglayiciApple: {
		Ad:        saglayiciApple,
		Issuer:    "https://appleid.apple.com",
		AuthURL:   "https://appleid.apple.com/auth/authorize",
		TokenURL:  "https://appleid.apple.com/auth/token",
		JWKSURL:   "https://appleid.apple.com/auth/keys",
		Kapsamlar: []string{"openid", "email", "name"},
		// K4: Apple'in PKCE destegi dokumante degil.
		PKCEDestekli: false,
		// name/email istenince Apple form_post ile doner (bkz. K2).
		FormPost: true,
	},
}

// federasyonKur, yapilandirmadaki saglayicilari koddaki sabit adreslerle
// birlestirir ve her biri icin bir yukari akis dogrulayicisi acar.
//
// Bilinmeyen bir saglayici adi HATA doner: sessizce atlamak, kullanicinin
// basmadigi bir butonun eksikligini kimsenin fark etmemesi demektir
// (Faz 1'in "sessiz sifir" sinifi).
//
// ctx, dogrulayicilarin JWKS arka plan yenilemesinin yasam suresidir ve
// CAGIRANIN sunucu omrune bagli ctx'i olmalidir (bkz. Start).
func federasyonKur(
	ctx context.Context, cfg *config.AuthConfig,
) (map[string]*YukariAkis, map[string]*auth.UpstreamVerifier, error) {
	if len(cfg.Federation) == 0 {
		return nil, nil, nil
	}
	akislar := make(map[string]*YukariAkis, len(cfg.Federation))
	dogrulayicilar := make(map[string]*auth.UpstreamVerifier, len(cfg.Federation))
	// Yarim kurulmus bir kume geride birakilmaz: ilk hatada o ana kadar
	// acilan dogrulayicilar kapatilir.
	temizle := func() {
		for _, d := range dogrulayicilar {
			_ = d.Close()
		}
	}
	for _, f := range cfg.Federation {
		sabit, ok := yukariAkisSabitleri[f.Name]
		if !ok {
			temizle()
			return nil, nil, fmt.Errorf("bilinmeyen federasyon saglayicisi: %q (desteklenenler: %s, %s)", f.Name, saglayiciApple, saglayiciGoogle)
		}
		akis := sabit // kopya: sabit tablo DEGISTIRILMEZ
		akis.IstemciID = f.ClientID
		akis.IstemciSir = f.ClientSecret
		akis.AppleEkipID = f.AppleTeamID
		akis.AppleAnahtarID = f.AppleKeyID
		akis.AppleAnahtarP8 = f.AppleKeyP8

		// audience = BIZIM istemci kimligimiz: saglayicinin baska bir
		// uygulama icin verdigi id_token bize kabul edilmemeli.
		dogrulayici, err := auth.NewUpstreamVerifier(ctx, akis.JWKSURL, akis.Issuer, akis.IstemciID)
		if err != nil {
			temizle()
			return nil, nil, fmt.Errorf("%s dogrulayicisi kurulamadi: %w", f.Name, err)
		}
		akislar[f.Name] = &akis
		dogrulayicilar[f.Name] = dogrulayici
	}
	return akislar, dogrulayicilar, nil
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
// FEDERASYON (Faz 2): akislar BOS ise federasyon uclari HIC BAGLANMAZ ve
// giris sayfasinda buton gorunmez. Yarim bir yuzey birakmak -- buton yok
// ama uc cagrilabilir, ya da buton var ama uc yok -- iki yonde de
// kullaniciyi hataya goturur. Baglandiklarinda yol oncelikleri yukaridaki
// kurali izler: "/federasyon/{saglayici}/baslat",
// "/federasyon/{saglayici}/geri" ve "/baglama" tam yol desenleridir ve
// "/" subtree desenini yener.
func muxKur(
	saglayici *op.Provider,
	kullaniciDepo UserStore,
	istekDepo *IstekDepo,
	federeDepo FedereDepo,
	akislar map[string]*YukariAkis,
	dogrulayicilar map[string]*auth.UpstreamVerifier,
	issuer string,
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

	if len(akislar) > 0 {
		adlar := make([]string, 0, len(akislar))
		for ad := range akislar {
			adlar = append(adlar, ad)
		}
		// Buton sirasi DETERMINISTIK olmali: map yinelemesi Go'da
		// bilerek rasgeledir, siralamazsak butonlar her sayfa
		// yuklemesinde yer degistirir.
		sort.Strings(adlar)
		sayfalar.FederasyonSaglayicilari(adlar)

		// istekDepo UC arayuzu birden karsilar (FederasyonDepo =
		// DurumDepo + BeklemeDepo, FedereIstekDepo ve OturumDepo);
		// hepsi ayni akisin kisa omurlu Redis durumudur ve ayri ayri
		// kablolamak iki yerde ayri bozulabilen bir kurulum olurdu.
		fedSayfalar := NewFedereSayfalar(
			akislar,
			dogrulayicilar,
			istekDepo,
			federeDepo,
			kullaniciDepo,
			istekDepo,
			istekDepo,
			op.AuthCallbackURL(saglayici),
			issuer,
			cerezGuvenli,
		)
		// Ikisi de POST handler'larini araci ile sarar (bkz. Bagla ve
		// BaglamaBagla); hizLimit nil GECILMEZ, uretimde Start limit
		// kurulamazsa hic baslamaz.
		fedSayfalar.Bagla(mux, araci, hizLimit)
		fedSayfalar.BaglamaBagla(mux, araci, hizLimit)
	}

	sayfalar.Bagla(mux, araci, hizLimit)

	yolAuthorize := saglayici.AuthorizationEndpoint().Relative()

	// /authorize: kutuphanenin AuthorizeValidator uzanti noktasiyla
	// S256 PKCE zorunlu kilinir (bkz. pkce.go). op.Authorize'i kendimiz
	// cagiriyoruz cunku kutuphanenin kendi router'i validator'u degil
	// saglayicinin kendisini kullanir.
	//
	// AuthorizeSar: cerez baglamasi YALNIZCA burada, istegi BASLATAN
	// tarayici icin kurulur (bkz. Sayfalar.AuthorizeSar). GET /giris ve
	// GET /kayit artik baglama MINTLEMEZ.
	zorlayici := &pkceZorlayici{Provider: saglayici}
	mux.Handle(yolAuthorize, araci.Handler(sayfalar.AuthorizeSar(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			op.Authorize(w, r, zorlayici)
		}))))

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
	// JWKS yenileme goroutine'leri surec yasadigi surece yasar; kapanista
	// ACIKCA durdurulur, aksi halde kapatilmis bir sunucunun arkasinda
	// saatlik ag cagrilari surer.
	if s.fedIptal != nil {
		s.fedIptal()
	}
	for ad, d := range s.dogrulayicilar {
		if err := d.Close(); err != nil {
			log.Printf("%s dogrulayicisi kapatilamadi: %v", ad, err)
		}
	}
	return shutdownErr
}

// APIAcikAnahtar, access token dogrulamasi icin API tarafinin kullanacagi
// ham RSA acik anahtarini dondurur. Anahtar.AcikAnahtar()'dan bilincli
// olarak farkli: o JWKS yayini icin op.Key sarmalayicisi doner, bu ise
// dogrudan *rsa.PublicKey.
func (s *Sunucu) APIAcikAnahtar() *rsa.PublicKey {
	return &s.anahtar.ozel.PublicKey
}

// HesapSil, kullanicinin kimlik kaydini siler. Federe eslesmeler ve
// refresh aileleri ON DELETE CASCADE ile gider; yeni access token
// uretilemez. Elde kalan access token'lar icin API tarafindaki
// silinmis kullanici engeli devreye girer.
func (s *Sunucu) HesapSil(ctx context.Context, userID string) error {
	return s.kullanicilar.Sil(ctx, userID)
}
