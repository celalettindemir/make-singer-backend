package kimlik

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// Issuer bossa OP hic baslamamali: bos issuer ile uretilen discovery
// belgesi kullanilamaz ve hata ancak ilk giris denemesinde gorunur.
func TestIssuerBossaBaslamaz(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = ""
	if _, err := Start(context.Background(), cfg, nil, "development"); err == nil {
		t.Error("bos issuer ile baslatildi")
	}
}

// Sirlar eksikse baslamamali; sessizce zayif varsayilana dusmek
// uretimde fark edilmez.
func TestEksikSirlarlaBaslamaz(t *testing.T) {
	temel := func() *config.AuthConfig {
		c := testAuthCfg()
		c.DBURL = "postgres://yok/yok"
		c.SigningKeyPEM = "gecersiz"
		c.CryptoKey = "kisa"
		return c
	}
	if _, err := Start(context.Background(), temel(), nil, "development"); err == nil {
		t.Error("gecersiz sirlarla baslatildi")
	}
}

// http:// issuer + yerel host (localhost) + production olmayan ortam:
// guvensiz moda izin var, yani Start http/https kontrolunu GECER. Sirlar
// (SigningKeyPEM vb.) testAuthCfg'de eksik oldugu icin Start yine de
// hata doner, ama bu hata https ile ILGILI OLMAMALI — aksi halde http
// kontrolunun kendisi engelliyor demektir.
func TestHTTPIssuerYerelGelistirmedeGuvensizModaGecer(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = "http://localhost:8001"
	_, err := Start(context.Background(), cfg, nil, "development")
	if err == nil {
		t.Fatal("sirlar eksikken baslatildi (beklenmiyordu)")
	}
	if strings.Contains(strings.ToLower(err.Error()), "https") {
		t.Errorf("http+yerel+development icin https hatasi donmemeli, ama dondu: %v", err)
	}
}

// http:// issuer ama host yerel degil (uretim benzeri bir alan adi):
// guvensiz moda izin yok, https zorunlu hatasi donmeli ve Start hicbir
// baglanti acmadan durmali.
func TestHTTPIssuerUzakHostReddedilir(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = "http://auth.example.com"
	_, err := Start(context.Background(), cfg, nil, "development")
	if err == nil {
		t.Fatal("uzak host'lu http issuer ile baslatildi")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "https") {
		t.Errorf("https zorunlu hatasi beklenirdi, gelen: %v", err)
	}
}

// http:// issuer + production ortami: host localhost olsa bile izin
// yok. Uretimde AUTH_ISSUER'a yanlislikla http:// yazilirsa TLS'siz OIDC
// sessizce ayaga kalkmamali.
func TestHTTPIssuerProductiondaReddedilir(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Issuer = "http://localhost:8001"
	_, err := Start(context.Background(), cfg, nil, "production")
	if err == nil {
		t.Fatal("production ortaminda http issuer ile baslatildi")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "https") {
		t.Errorf("https zorunlu hatasi beklenirdi, gelen: %v", err)
	}
}

// ---- I2: gecersiz/0 sure degerleri ----

// viper, COZULEMEYEN bir sure degerinde varsayilana DUSMEZ; sessizce 0s
// verir (orn. AUTH_ACCESS_TTL="15min", AUTH_REFRESH_TTL="60d" —
// dokumanlardaki "15 dakika"/"60 gun" ifadelerinin dogal yazimlari).
// 0 TTL yikicidir: AccessTTL=0 ile access kaydi hic yazilmaz ve jeton
// exp=now ile uretilir (her /api/* 401), RefreshTTL=0 ile her refresh
// kaydi dogdugu an olur (tum kullanicilar aninda disari atilir). Start
// bunlari HIC DOGRULAMIYOR ve hatasiz donuyordu.
func TestGecersizTTLIleBaslamaz(t *testing.T) {
	durumlar := []struct {
		ad       string
		degistir func(*config.AuthConfig)
		anahtar  string
	}{
		{"access_ttl 0", func(c *config.AuthConfig) { c.AccessTTL = 0 }, "access_ttl"},
		{"access_ttl negatif", func(c *config.AuthConfig) { c.AccessTTL = -time.Minute }, "access_ttl"},
		{"refresh_ttl 0", func(c *config.AuthConfig) { c.RefreshTTL = 0 }, "refresh_ttl"},
		{"refresh_ttl negatif", func(c *config.AuthConfig) { c.RefreshTTL = -time.Hour }, "refresh_ttl"},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			cfg := testAuthCfg()
			d.degistir(cfg)
			_, err := Start(context.Background(), cfg, nil, "development")
			if err == nil {
				t.Fatal("gecersiz TTL ile baslatildi")
			}
			// Testin ADININ SOYLEDIGI SEBEPTEN reddedildigini dogrula:
			// testAuthCfg'de sirlar da eksik, yani "bir hata dondu"
			// kontrolu vacuous olurdu.
			if !strings.Contains(err.Error(), d.anahtar) {
				t.Errorf("hata %q icermeli, gelen: %v", d.anahtar, err)
			}
		})
	}
}

// ---- I3: bos ClientID / RedirectURIs ----

// ClientID bos oldugunda middleware.NewOPAuthMiddleware kurulumHatasi'na
// duser ve HER /api/* istegini 401 reddeder; /health ise hala
// "kimlik": true donuyordu (tam kesinti + yanlis saglik sinyali).
// RedirectURIs bos oldugunda her /authorize reddedilir, hic kullanici
// giris yapamaz. config.yaml bunun yasak oldugunu YAZIYORDU ama kod
// zorlamiyordu.
func TestBosIstemciYapilandirmasiIleBaslamaz(t *testing.T) {
	durumlar := []struct {
		ad       string
		degistir func(*config.AuthConfig)
		anahtar  string
	}{
		{"client_id bos", func(c *config.AuthConfig) { c.ClientID = "" }, "client_id"},
		{"redirect_uris nil", func(c *config.AuthConfig) { c.RedirectURIs = nil }, "redirect_uris"},
		{"redirect_uris bos dilim", func(c *config.AuthConfig) { c.RedirectURIs = []string{} }, "redirect_uris"},
		{"redirect_uris bos girdi", func(c *config.AuthConfig) { c.RedirectURIs = []string{"  "} }, "redirect_uris"},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			cfg := testAuthCfg()
			d.degistir(cfg)
			_, err := Start(context.Background(), cfg, nil, "development")
			if err == nil {
				t.Fatal("bos istemci yapilandirmasi ile baslatildi")
			}
			if !strings.Contains(err.Error(), d.anahtar) {
				t.Errorf("hata %q icermeli, gelen: %v", d.anahtar, err)
			}
		})
	}
}

// Gecerli bir yapilandirma bu dogrulamalarin HICBIRINE takilmamali:
// aksi halde yukaridaki testler "her sey reddediliyor" diye de gecerdi.
func TestGecerliYapilandirmaDogrulamayiGecer(t *testing.T) {
	if err := yapilandirmaDogrula(testAuthCfg()); err != nil {
		t.Errorf("gecerli yapilandirma reddedildi: %v", err)
	}
}

// Apple federasyonu uc alanin (EkipID/AnahtarID/AnahtarP8) hepsi
// olmadan calisamaz: client_secret her degisimde bu uctan uretilir
// (bkz. K5). Eksik olan BASLANGICTA (startup) hata vermeli, kullanicinin
// Apple ile giris denedigi anda kesfedilen bir sessiz bozukluk degil.
func TestAppleFederasyonuEksikAlanlarlaBaslamaz(t *testing.T) {
	durumlar := []struct {
		ad     string
		federe config.FederasyonIstemci
	}{
		{"ekip id eksik", config.FederasyonIstemci{Ad: "apple", IstemciID: "tr.makesinger.giris", AppleAnahtarID: "AB12", AppleAnahtarP8: "pem-verisi"}},
		{"anahtar id eksik", config.FederasyonIstemci{Ad: "apple", IstemciID: "tr.makesinger.giris", AppleEkipID: "TEAM1", AppleAnahtarP8: "pem-verisi"}},
		{"anahtar p8 eksik", config.FederasyonIstemci{Ad: "apple", IstemciID: "tr.makesinger.giris", AppleEkipID: "TEAM1", AppleAnahtarID: "AB12"}},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			cfg := testAuthCfg()
			cfg.Federasyon = []config.FederasyonIstemci{d.federe}
			if err := yapilandirmaDogrula(cfg); err == nil {
				t.Fatal("eksik apple alanlariyla dogrulama gecti")
			}
		})
	}
}

// Apple'in uc alani da doluysa VE .p8 GERCEKTEN ayristirilabiliyorsa
// dogrulama gecmeli (vacuous reddetme kontrolu).
//
// Eskiden burada AppleAnahtarP8 olarak "pem-verisi" gibi bir yer tutucu
// yetiyordu; bu test o yer tutucuyu kabul ederek I1'in actigi kapiyi
// PINLIYORDU. Artik gercek bir anahtar uretiliyor.
func TestAppleFederasyonuTamAlanlarlaGecer(t *testing.T) {
	p8, _ := testP8Uret(t)
	cfg := testAuthCfg()
	cfg.Federasyon = []config.FederasyonIstemci{{
		Ad: "apple", IstemciID: "tr.makesinger.giris",
		AppleEkipID: "TEAM1", AppleAnahtarID: "AB12", AppleAnahtarP8: p8,
	}}
	if err := yapilandirmaDogrula(cfg); err != nil {
		t.Errorf("tam apple yapilandirmasi reddedildi: %v", err)
	}
}

// I1: alanlarin "bos olmamasi" YETMEZ. K8s'te PEM sirlarinin en sik
// gercek hatasi, anahtarin tek satirda ve gercek yeni satirlar yerine
// literal \n ile mount edilmesidir. Boyle bir deger bos DEGILDIR, yani
// eski kontrolu gecer; pod Ready olur, "Apple ile giris" butonu gorunur
// ve hata ancak kullanici Apple'da Face ID + iki adimli onayi
// BITIRDIKTEN sonra anlamsiz bir 403 oturum sayfasi olarak cikar.
// Baslangicta yakalanmak ZORUNDA.
func TestAppleP8BaslangictaAyristirilir(t *testing.T) {
	gercek, _ := testP8Uret(t)
	// Gercek dagitim hatasinin birebir girdisi: ayni anahtar, yeni
	// satirlari literal \n dizisine cevrilmis halde.
	literalKacisli := strings.ReplaceAll(gercek, "\n", `\n`)

	durumlar := []struct {
		ad string
		p8 string
	}{
		{"literal \\n ile tek satira dusmus p8", literalKacisli},
		{"PEM olmayan yer tutucu", "pem-verisi"},
		{"PEM govdesi bozuk", "-----BEGIN PRIVATE KEY-----\nbu-base64-degil\n-----END PRIVATE KEY-----\n"},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			cfg := testAuthCfg()
			cfg.Federasyon = []config.FederasyonIstemci{{
				Ad: "apple", IstemciID: "tr.makesinger.giris",
				AppleEkipID: "TEAM1", AppleAnahtarID: "AB12", AppleAnahtarP8: d.p8,
			}}
			err := yapilandirmaDogrula(cfg)
			if err == nil {
				t.Fatal("ayristirilamayan .p8 ile dogrulama GECTI; baslangicta reddedilmeliydi")
			}
			if !strings.Contains(err.Error(), "AUTH_FED_APPLE_KEY_P8") {
				t.Errorf("hata operatore ne yapacagini soylemiyor: %v", err)
			}
			// Hata mesaji anahtar ICERIGINI sizdirmamali: ne ham deger,
			// ne base64 govdesi loglara/stderr'e dusmeli.
			if strings.Contains(err.Error(), d.p8) {
				t.Errorf("hata mesaji anahtar icerigini sizdiriyor: %v", err)
			}
		})
	}
}

// I2: Google'in tek zorunlu sirri client_secret'tir. Bos iken token
// degisimi 401 invalid_client ile duser ve kullanici 403 gorur; pod
// Ready, metrikler temiz, alarm yok. Apple icin fail-closed baslangic
// kontrolu varken Google icin olmamasi acik bir asimetriydi.
func TestGoogleFederasyonuSecretsizBaslamaz(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Federasyon = []config.FederasyonIstemci{{
		Ad: "google", IstemciID: "123.apps.googleusercontent.com",
	}}
	err := yapilandirmaDogrula(cfg)
	if err == nil {
		t.Fatal("client_secret bos iken dogrulama GECTI")
	}
	if !strings.Contains(err.Error(), "AUTH_FED_GOOGLE_CLIENT_SECRET") {
		t.Errorf("hata operatore ne yapacagini soylemiyor: %v", err)
	}
}

// Vacuous reddetme kontrolu: secret doluysa Google gecmeli. Ayrica
// kuralin DOGRU tarafini pinler: "client_id de bos" durumu bir hata
// DEGILDIR -- o saglayici federasyonIstemcileriOlustur'da listeye hic
// girmez, yani yapilandirmaDogrula'ya hic ulasmaz ve bos bir federasyon
// listesi gecerlidir.
func TestGoogleFederasyonuSecretVarsaGecer(t *testing.T) {
	cfg := testAuthCfg()
	cfg.Federasyon = []config.FederasyonIstemci{{
		Ad: "google", IstemciID: "123.apps.googleusercontent.com", IstemciSir: "gizli",
	}}
	if err := yapilandirmaDogrula(cfg); err != nil {
		t.Errorf("tam google yapilandirmasi reddedildi: %v", err)
	}

	cfg.Federasyon = nil
	if err := yapilandirmaDogrula(cfg); err != nil {
		t.Errorf("federasyon KAPALI yapilandirma reddedildi: %v", err)
	}
}

// ---- I3: /health GERCEK hazirligi yansitsin ----

// Hazir(), "Start basariyla dondu mu" degil "dinleyici su an hizmet
// veriyor mu" sorusunu yanitlar. nil alici icin de guvenli olmali:
// /health, OP hic kurulmadiginda da bunu cagiriyor.
func TestHazirDinleyiciDurumunuYansitir(t *testing.T) {
	var yok *Sunucu
	if yok.Hazir() {
		t.Error("nil Sunucu hazir gorundu")
	}

	s := &Sunucu{}
	if s.Hazir() {
		t.Error("baslatilmamis Sunucu hazir gorundu")
	}
	s.hazir.Store(true)
	if !s.Hazir() {
		t.Error("hazir=true iken Hazir() false dondu")
	}
	// Dinleyici cokerse (veya kapanirsa) /health bunu gormeli.
	s.hazir.Store(false)
	if s.Hazir() {
		t.Error("dinleyici durduktan sonra hala hazir gorunuyor")
	}
}

// Faz 1'in fail-closed kurali: hiz limiti KURULAMAZSA Start hic
// basarili olmamali. Limitsiz bir giris ucuyla ayaga kalkmak hem
// sinirsiz brute-force hem de ana API'yi dusuren bir CPU DoS yuzeyi
// demektir (bkz. hizlimit.go).
//
// NEDEN BU KADAR KURULUM: hiz limiti Start icinde veritabani ve
// migration'dan SONRA kurulur. Yapilandirmanin geri kalani eksik
// birakilirsa Start zaten daha once duser ve test hiz limiti yolunu HIC
// olcmez. Bu yuzden burada gercek Postgres ve Redis kullanilir;
// yoksa test atlanir (bkz. testRedis, testPostgres).
func TestHizLimitiKurulamazsaStartBasarisiz(t *testing.T) {
	rdb := testRedis(t)
	dsn := os.Getenv("KIMLIK_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable"
	}

	temel := func() *config.AuthConfig {
		c := testAuthCfg()
		c.DBURL = dsn
		c.SigningKeyPEM = testPEM(t)
		c.CryptoKey = testCryptoAnahtari
		c.Port = "0"
		c.LoginIPPerMin = 10
		c.LoginEmailPerHour = 10
		c.SignupIPPerHour = 10
		return c
	}

	// Once SAGLIK kontrolu: ayni yapilandirma limit degerleri GECERLI
	// iken Start'i hiz limitine KADAR goturebiliyor olmali. Aksi halde
	// asagidaki iddia, limitle ilgisiz bir hatayi dogrulamis olurdu.
	sunucu, err := Start(context.Background(), temel(), rdb, "development")
	if err != nil {
		if strings.Contains(err.Error(), "veritabanina baglanilamadi") {
			t.Skip("yerel Postgres yok, atlaniyor")
		}
		t.Fatalf("gecerli yapilandirma hiz limitine gelemeden dustu: %v", err)
	}
	ctx, iptal := context.WithTimeout(context.Background(), 3*time.Second)
	defer iptal()
	if kapatmaHata := sunucu.Kapat(ctx); kapatmaHata != nil {
		t.Fatalf("Kapat: %v", kapatmaHata)
	}

	// Simdi YALNIZCA limit degerini bozuyoruz.
	bozuk := temel()
	bozuk.LoginIPPerMin = 0
	s, err := Start(context.Background(), bozuk, rdb, "development")
	if err == nil {
		if s != nil {
			_ = s.Kapat(context.Background())
		}
		t.Fatal("hiz limiti kurulamadigi halde Start basarili oldu")
	}
	if s != nil {
		t.Error("Start hata dondu ama Sunucu da dondurdu")
	}
	if !strings.Contains(err.Error(), "hiz limiti kurulamadi") {
		t.Errorf("hata hiz limitiyle ilgili degil: %v", err)
	}
}
