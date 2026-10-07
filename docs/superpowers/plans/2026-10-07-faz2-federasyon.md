# Faz 2 — Apple/Google federasyonu ve hesap bağlama Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Kendi OpenID Provider'ımızın giriş sayfasına Apple ve Google ile giriş eklemek; biz onlara karşı RP oluruz, dönen `id_token` sağlayıcının JWKS'i ile doğrulanır ve `(sağlayıcı, sub)` çiftiyle kullanıcı bulunur veya oluşturulur.

**Architecture:** Faz 1'in `internal/kimlik` paketine yukarı akış (upstream) RP tarafı eklenir. Yeni uçlar OP'nin 8001 dinleyicisinde yaşar: `/federasyon/{saglayici}/baslat` yetkilendirmeyi başlatır, `/federasyon/{saglayici}/geri` dönüşü karşılar, `/baglama` çakışmada mevcut hesabın şifresini bir kez sorar. Kalıcı eşleşme Postgres'te yeni bir `federe_kimlikler` tablosunda, akışın ara durumu Redis'te tek kullanımlık `state` kaydında tutulur. Mobil uygulama bu akıştan hiç haberdar değildir ve Apple/Google SDK'sı taşımaz.

**Tech Stack:** Go 1.25, `github.com/zitadel/oidc/v3 v3.51.8` (`pkg/op`, değiştirilmez/kopyalanmaz), `github.com/MicahParks/keyfunc/v3` + `github.com/golang-jwt/jwt/v5` (yukarı akış `id_token` doğrulaması), `pgx/v5` + `pgxpool`, `go-redis`, `crypto/ecdsa` (Apple `client_secret` JWT'si), `net/http` + `html/template`.

**Spec:** `docs/superpowers/specs/2026-09-29-kendi-oidc-saglayicimiz-design.md` (özellikle `### Federasyon ve hesap bağlama`, `### Veri yerleşimi`, `### Token modeli`)

## Global Constraints

Bu bölüm her görevin gereksinimlerine örtük olarak dahildir.

- **E-posta üzerinden otomatik bağlama YAPILMAZ.** Sağlayıcı `email_verified: true` dese bile aynı e-postaya sahip mevcut bir hesaba sessizce bağlanmaz. Spec'teki gerekçe somut bir saldırıdır: e-posta doğrulaması yapmadığımız için saldırgan kurbanın adresiyle şifreli bir hesap açabilir; kurban Google ile girip otomatik bağlanırsa şifresi saldırganda olan bir hesaba düşer.
- **Apple'ın gizli aktarma adresleri (`@privaterelay.appleid.com`) hiçbir koşulda bağlama için güvenilir sayılmaz.**
- Kütüphane kodu (`zitadel/oidc`) **değiştirilmez veya kopyalanmaz**; yalnızca kendi uzantı noktalarından geçilir. Sürüm `v3.51.8`'de sabittir.
- Kalıcı veri **yalnızca Postgres'e** yazılır. Redis `--maxmemory-policy allkeys-lru` ile, volume'suz, AOF/RDB kapalı çalışır (`makesinger-redis.yaml:20-38`); oraya yalnızca kısa ömürlü akış durumu konur.
- Hiçbir log satırına **e-posta, şifre, `authRequestID`, `state`, `code`, `id_token` veya çerez değeri** yazılmaz. Faz 1'de bu bir Critical'dı ve `sayfa.go`'da iki kez düzeltildi.
- Kullanıcıya dönen hata metinleri **hesabın var olup olmadığını sızdırmaz**; `hataGirisBasarisiz = "E-posta veya sifre hatali."` tek metindir.
- Yeni tüm uçlar hız limitine bağlanır; limit kurulamazsa süreç **hiç başlamaz** (Faz 1'in fail-closed kuralı).
- Kaynak kodda ve yorumlarda Türkçe karakter kullanılmaz (mevcut dosyaların kuralı); plan ve dokümanlar Türkçe yazılır.
- Her görev kendi testleriyle biter ve testler **ağ gerektirmez**; yukarı akış sağlayıcısı `httptest` ile taklit edilir.

---

## Bağlayıcı kararlar

Bunlar spec'in cevaplamadığı ama kodu belirleyen kararlardır. Gerekçeleri burada duruyor ki görev uygulayıcısı yeniden tartışmasın.

**K1 — Aynı e-posta, şifresiz hesap: açıklayan 409 ile engellenir.** Spec çakışmada "mevcut hesabın şifresi bir kez sorulur" diyor, ama kullanıcı Google ile kaydolduysa sorulacak şifre yoktur ve `users` tablosundaki `UNIQUE (lower(email))` (`migrations/001_kullanici.sql`) ikinci bir hesabı imkânsız kılar. Karar: bağlama yok, yeni hesap yok; "Bu e-posta Google ile kayıtlı, Google ile giriş yapın" diyen bir 409 sayfası çıkar. Spec'in otomatik-bağlama yasağıyla tutarlı ve yeni saldırı yüzeyi getirmiyor. Bedeli: kullanıcı iki sağlayıcıyı tek hesapta birleştiremez.

**K2 — Apple `form_post` ile döner ve `SameSite=Lax` çerezi o POST'ta TARAYICI TARAFINDAN GÖNDERİLMEZ.** Apple'dan `name`/`email` scope'u istendiğinde dönüş `response_mode=form_post`, yani başka bir origin'den bize POST gelir. Faz 1'in oturum çerezi `SameSite: http.SameSiteLaxMode` (`oturum.go`) olduğu için bu POST'ta gönderilmez ve Faz 1'in çerez bağlaması kontrolü federasyon dönüşünde ÇALIŞMAZ. Ayrıca `sayfa.go`'daki `caprazSiteIstek` guard'ı `Sec-Fetch-Site: cross-site` isteklerini reddediyor; federasyon dönüşü meşru biçimde cross-site'tır.

Karar: federasyon akışı **kendi çerezini** kullanır — `kimlik_fed`, `HttpOnly`, `Secure`, `SameSite=None`, `Path=/federasyon`. Değeri `/federasyon/{saglayici}/baslat`'ta üretilir ve Redis'teki `state` kaydının içine yazılır; dönüşte çerez ile kayıttaki değer `subtle.ConstantTimeCompare` ile karşılaştırılır. Böylece Faz 1'in C1 düzeltmesinin sağladığı "istek, onu başlatan tarayıcıya bağlı" güvencesi cross-site POST'ta da korunur. Oturum çerezi (`kimlik_oturum`) **değiştirilmez**; `SameSite=None`'a çevirmek Faz 1'in login-CSRF korumasını zayıflatırdı.

**K3 — `caprazSiteIstek` guard'ı federasyon dönüşüne UYGULANMAZ.** Dönüş tanımı gereği cross-site'tır. Yerine K2'deki çerez + tek kullanımlık `state` + `nonce` üçlüsü geçer. Bu muafiyet yalnızca `/federasyon/{saglayici}/geri` yolunadır ve kodda gerekçesiyle yazılır.

**K4 — PKCE yalnızca Google'a gönderilir.** Google `code_challenge`/`S256`'yı dokümante ediyor; Apple'ın desteği dokümante değil ve bilinmeyen parametreyle akışı kırma riski var. Sağlayıcı yapılandırmasına `PKCEDestekli bool` konur. Replay koruması her iki sağlayıcıda `nonce`, CSRF koruması `state` + K2 çerezi ile sağlanır — yani PKCE'nin yokluğu Apple'da tek koruma kaybı değildir.

**K5 — Apple `client_secret`'ı her değişimde yeniden üretilir, önbelleğe alınmaz.** Apple `client_secret` olarak ES256 ile imzalanmış, ömrü en fazla 6 ay olan bir JWT bekler. Üretmek mikrosaniyeler sürer; önbellekleme ve yenileme makinesi kurmak, süresi dolmuş bir sırla sessizce kırılan bir akış riskini bedavaya satın almak olurdu. `exp` 5 dakika verilir.

**K6 — `amr` claim'i istek başına hesaplanır.** `istek.go:69` bugün `GetAMR()` için sabit `[]string{"pwd"}` dönüyor. Google ile giren bir kullanıcının token'ında "pwd" yazması yanlış bir beyandır. `AuthIstek`'e `AMR []string` alanı eklenir: şifreyle giriş `["pwd"]`, federasyonla giriş `["fed"]`. Sağlayıcı adı `amr`'ye konmaz (RFC 8176 kayıtlı değerler listesi tutar); hangi sağlayıcı olduğu `federe_kimlikler` tablosunda zaten duruyor.

**K7 — `jwks_verifier.go`'nun periyodik anahtar yenilemesi bugün ÖLÜ.** `NewJWKSVerifier` (`internal/auth/jwks_verifier.go:43-46`) 30 saniyelik bir `context.WithTimeout` kurup `defer cancel()` ile hemen iptal ediyor ve `keyfunc.NewDefaultCtx`'e o ctx'i veriyor.

**DÜZELTME (Görev 2'de ölçüldü):** Bu kararın ilk hali "yani rotasyondan sonra bütün girişler sessizce kırılır" diyordu. **Yanlıştı.** Sabitlenmiş kütüphane kaynağı iki yolu ayırıyor:

- `jwkset@v0.5.17/http.go:89` kendi doküman yorumunda: *"a context that can end the refresh goroutine"* — verdiğimiz ctx YALNIZCA saatlik periyodik yenilemeyi yönetiyor (`storage.go:278-295`, `case <-options.Ctx.Done(): return`).
- Rotasyonun gerçekten dayandığı mekanizma başka: `http.go:83`'te belgelenen "bilinmeyen `kid` okunmaya çalışılırsa uzak kaynağı yenile, 5 dakika hız sınırıyla" yolu. O yol bizim ctx'imizden bağımsız.

Yani iptal edilmiş ctx **rotasyonu kırmıyor**. Gerçek bedeli daha mütevazı ve hâlâ gerçek: (a) kümeden **çıkarılan** bir anahtar bellekte süresiz kalır, yani sağlayıcının emekliye ayırdığı (belki ele geçtiği için ayırdığı) bir anahtarı süreç yeniden başlayana kadar kabul etmeye devam ederiz; (b) bilinmeyen-kid yolu 5 dakikada bir ile sınırlı olduğundan, periyodik yenileme ölüyken o pencerede gelen ikinci bir yeni kid doğrulanamaz. Düzeltme bu yüzden korunur ama ciddiyeti "bütün girişler kırılır" değil.

Bunun bir sonucu daha var: **rotasyon testi K7 için bir regresyon testi DEĞİLDİR.** Ctx hatası yeniden konduğunda test geçmeye devam eder, çünkü ölçtüğü şey bilinmeyen-kid yolu. Test yine değerli — rotasyonun fiilen çalıştığını kanıtlıyor — ama hangi korumayı ölçtüğü konusunda yoruma güvenilmemeli. Periyodik yenilemeyi saniye altı bir birim testiyle gözlemlemek mümkün değil; kurucunun imzasını genişletmek veya kütüphanenin dışa açık olmayan iç yapılarına uzanmak bu kadar mütevazı bir sorun için kapsam kayması olurdu.

**K8 — Apple'ın `email_verified` claim'i string veya bool gelebilir.** Apple bu claim'i bazı akışlarda `"true"` (string) olarak gönderir. `bool` olarak çözümlemeye çalışan kod sessizce hata verir ve giriş kırılır. Özel bir `EsnekBool` tipi yazılır ve ikisini de kabul eder.

---

## Test yazma kuralı

Aşağıdaki görevlerde bazı testler **gövdesi yorum olarak** verilmiştir (girdiler, beklenen durum kodu ve beklenen hata adıyla). Bunlar birer belirtimdir, bitmiş kod değil: uygulayıcı her birini **gerçek Go koduna çevirmek zorundadır**. Yorumdaki girdiler ve beklentiler bağlayıcıdır, serbestçe yorumlanamaz.

Bir testin yük taşıdığının tek kanıtı mutasyondur: korumayı kaldırdığında test **düşmeli**. Faz 1'de iki test, ölçmeyi iddia ettikleri düzeltme hiç yokken de geçiyordu (`TestYanlisAudReddedilir` ve 14a'nın saldırı testi) — ikisi de sonradan düzeltildi. Bu yüzden her görevin bir mutasyon adımı var ve **düşmeyen bir mutasyon, kodun değil testin düzeltilmesi gerektiği anlamına gelir.**

Pozitif testler de boş olabilir: bir saldırı testinin anlamlı olması için saldırının önkoşulunun gerçekten kurulduğu da assert edilmelidir (örnek: aşağıdaki `TestSaldiriEpostayaDayaliHesapDevralmaEngellenir`, reddi ölçmeden önce saldırganın hesabının gerçekten açıldığını doğrular).

---

## Dosya yapısı

**Yeni dosyalar (`core-service/internal/kimlik/`):**

| Dosya | Sorumluluk |
|---|---|
| `migrations/003_federe_kimlik.sql` | `federe_kimlikler` tablosu |
| `federe_depo.go` | `(saglayici, sub)` eşleşmesi: okuma, bağlama, şifresiz kullanıcı oluşturma |
| `federe_depo_sahte.go` | Aynı arayüzün bellek içi uygulaması (testler ağsız koşsun) |
| `yukari_akis.go` | Sağlayıcı yapılandırması, authorize URL kurulumu, `state`/`nonce` kaydı |
| `yukari_akis_apple.go` | Apple'a özgü: ES256 `client_secret` JWT'si, `form_post` |
| `yukari_akis_jeton.go` | `code` → `id_token` değişimi (HTTP istemcisi) |
| `federe_sayfa.go` | `/federasyon/{saglayici}/baslat`, `/federasyon/{saglayici}/geri` |
| `baglama_sayfa.go` | `/baglama` — şifre bir kez sorulur; ve 409 çakışma sayfası |
| `sablonlar/baglama.html` | Bağlama formu |
| `sablonlar/cakisma.html` | K1'in 409 sayfası |

**Değişen dosyalar:**

| Dosya | Değişiklik |
|---|---|
| `internal/auth/jwks_verifier.go` | Yönü değişir: Zitadel'e değil Apple/Google'a bakar; alg sabitlenir, ctx uzun ömürlü olur, `nonce` doğrulanır (K7, K8) |
| `internal/kimlik/istek.go` | `AuthIstek.AMR` alanı; `GetAMR()` sabit dönmeyi bırakır (K6) |
| `internal/kimlik/sunucu.go` | `muxKur` yeni uçları ve yeni depoyu bağlar |
| `internal/kimlik/sayfa.go` | Giriş sayfasına sağlayıcı butonları için veri alanı |
| `internal/kimlik/sablonlar/giris.html` | Apple/Google butonları |
| `internal/kimlik/hizlimit.go` | Federasyon uçları için sayaç |
| `internal/config/config.go` | `AuthConfig` içinde federasyon yapılandırması |
| `core-service/.env.example` | Yeni `AUTH_FED_*` değişkenleri |
| `CLAUDE.md` | Auth bölümüne federasyon akışı |

---

### Task 1: `federe_kimlikler` tablosu ve deposu

**Files:**
- Create: `core-service/internal/kimlik/migrations/003_federe_kimlik.sql`
- Create: `core-service/internal/kimlik/federe_depo.go`
- Create: `core-service/internal/kimlik/federe_depo_sahte.go`
- Test: `core-service/internal/kimlik/federe_depo_sozlesme_test.go`

**Interfaces:**
- Consumes: `UserStore`, `User`, `ErrKullaniciYok`, `epostaNormalize` (`kullanici_depo.go`); `*pgxpool.Pool`; göç mekanizması `veritabani.go` (`go:embed migrations/*.sql`, `pg_advisory_lock`).
- Produces:
  - `type FedereKimlik struct { Saglayici, Sub, UserID string; CreatedAt time.Time }`
  - `type FedereDepo interface { KimlikleKullanici(ctx context.Context, saglayici, sub string) (*User, error); Bagla(ctx context.Context, saglayici, sub, userID string) error; FedereKullaniciOlustur(ctx context.Context, saglayici, sub, email, ad string, epostaDogrulandi bool) (*User, error); SaglayicilarByUserID(ctx context.Context, userID string) ([]string, error) }`
  - `var ErrKimlikYok = errors.New("federe kimlik bulunamadi")`
  - `var ErrKimlikKullanimda = errors.New("federe kimlik baska hesaba bagli")`
  - `func NewPostgresFedereDepo(havuz *pgxpool.Pool) *PostgresFedereDepo`
  - `type SahteFedereDepo struct { ... }` + `func NewSahteFedereDepo(kullanici *SahteUserStore) *SahteFedereDepo`

- [ ] **Step 1: Göçü yaz**

`core-service/internal/kimlik/migrations/003_federe_kimlik.sql`:

```sql
-- Federe kimlik eslesmeleri: (saglayici, saglayicidaki sub) -> bizim user_id.
--
-- Birincil anahtar (saglayici, sub) CIFTIDIR: ayni sub degeri iki ayri
-- saglayicida ayni kisi demek degildir, yani tek basina sub tekil degildir.
CREATE TABLE IF NOT EXISTS federe_kimlikler (
    saglayici  text NOT NULL,
    sub        text NOT NULL,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (saglayici, sub)
);

-- Hesap silme ve "bu hesap hangi saglayicilara bagli" sorgusu user_id'ye
-- gore arar; FK kendiliginden indeks kurmadigi icin acikca ekliyoruz.
CREATE INDEX IF NOT EXISTS federe_kimlikler_user_idx ON federe_kimlikler (user_id);

-- Bir kullanici ayni saglayiciya IKI KEZ baglanamaz: aksi halde
-- "Google'la giris yap" dedigimiz hesapta iki ayri Google sub'i olur ve
-- hangisinin gecerli oldugu belirsizlesir.
CREATE UNIQUE INDEX IF NOT EXISTS federe_kimlikler_user_saglayici_key
    ON federe_kimlikler (user_id, saglayici);
```

- [ ] **Step 2: Sözleşme testini yaz (önce düşmeli)**

`core-service/internal/kimlik/federe_depo_sozlesme_test.go`. Faz 1'deki `kullanici_depo_sozlesme_test.go` ile aynı biçim: tek test gövdesi, hem sahte hem Postgres uygulamasına koşar. Postgres yoksa `t.Skip`.

```go
package kimlik

import (
	"context"
	"errors"
	"testing"
)

// federeDepoKur, ayni sozlesmeyi iki uygulamaya da kosturur.
type federeKurulum struct {
	ad       string
	depo     FedereDepo
	kullanici UserStore
}

func TestFedereDepoSozlesmesi(t *testing.T) {
	for _, k := range federeDepoKurulumlari(t) {
		t.Run(k.ad, func(t *testing.T) {
			ctx := context.Background()

			// Bilinmeyen kimlik ErrKimlikYok doner, nil kullanici degil.
			if _, err := k.depo.KimlikleKullanici(ctx, "google", "yok-123"); !errors.Is(err, ErrKimlikYok) {
				t.Fatalf("bilinmeyen kimlik icin err = %v, beklenen ErrKimlikYok", err)
			}

			// Federe kullanici olusturma: sifre hash'i BOS olmali, yani
			// SifreDogru her sifre icin false donmeli.
			ku, err := k.depo.FedereKullaniciOlustur(ctx, "google", "sub-1", "a@ornek.com", "Ali", true)
			if err != nil {
				t.Fatalf("federe kullanici olusturulamadi: %v", err)
			}
			if ku.PasswordHash != "" {
				t.Error("federe hesaba sifre hash'i yazilmis")
			}
			if ku.SifreDogru("herhangi-bir-sifre-123") {
				t.Error("sifresiz hesaba sifreyle girilebiliyor")
			}
			if !ku.EmailVerified {
				t.Error("epostaDogrulandi=true verildi ama EmailVerified false")
			}

			// Olusturulan kimlik artik bulunabilir ve AYNI kullaniciyi doner.
			bulunan, err := k.depo.KimlikleKullanici(ctx, "google", "sub-1")
			if err != nil {
				t.Fatalf("kimlik okunamadi: %v", err)
			}
			if bulunan.ID != ku.ID {
				t.Errorf("kullanici id = %q, beklenen %q", bulunan.ID, ku.ID)
			}

			// Saglayici ayrimi: ayni sub degeri baska saglayicida AYNI kisi
			// degildir.
			if _, err := k.depo.KimlikleKullanici(ctx, "apple", "sub-1"); !errors.Is(err, ErrKimlikYok) {
				t.Errorf("apple/sub-1 icin err = %v, beklenen ErrKimlikYok", err)
			}

			// Mevcut (sifreli) hesaba baglama.
			sifreli, err := k.kullanici.Create(ctx, "b@ornek.com", "Veli", "cok-uzun-bir-sifre")
			if err != nil {
				t.Fatalf("sifreli hesap olusturulamadi: %v", err)
			}
			if err := k.depo.Bagla(ctx, "apple", "apple-sub-9", sifreli.ID); err != nil {
				t.Fatalf("bagla: %v", err)
			}
			bagli, err := k.depo.KimlikleKullanici(ctx, "apple", "apple-sub-9")
			if err != nil {
				t.Fatalf("baglanan kimlik okunamadi: %v", err)
			}
			if bagli.ID != sifreli.ID {
				t.Error("bagli kimlik yanlis kullaniciyi donuyor")
			}

			// AYNI (saglayici, sub) BASKA bir hesaba baglanamaz: aksi halde
			// bir Google hesabi iki bizim hesabimiza birden baglanir ve
			// giriste hangisine dusecegi belirsizlesir.
			if err := k.depo.Bagla(ctx, "apple", "apple-sub-9", ku.ID); !errors.Is(err, ErrKimlikKullanimda) {
				t.Errorf("ikinci bagla err = %v, beklenen ErrKimlikKullanimda", err)
			}

			// Bir kullanici ayni saglayiciya iki kez baglanamaz.
			if err := k.depo.Bagla(ctx, "apple", "apple-sub-FARKLI", sifreli.ID); !errors.Is(err, ErrKimlikKullanimda) {
				t.Errorf("ayni saglayiciya ikinci bagla err = %v, beklenen ErrKimlikKullanimda", err)
			}

			// SaglayicilarByUserID, K1'in 409 sayfasinin metnini besler.
			sag, err := k.depo.SaglayicilarByUserID(ctx, sifreli.ID)
			if err != nil {
				t.Fatalf("saglayicilar okunamadi: %v", err)
			}
			if len(sag) != 1 || sag[0] != "apple" {
				t.Errorf("saglayicilar = %v, beklenen [apple]", sag)
			}
		})
	}
}
```

`federeDepoKurulumlari` yardımcısı aynı dosyada: sahte uygulamayı her zaman ekler, Postgres uygulamasını ise `kullanici_depo_sozlesme_test.go:18`'deki `testPostgresUserStore` ile **birebir aynı kalıpla** kurar — `KIMLIK_TEST_DB_URL`, yoksa varsayılan `postgres://postgres:kimlik-test-yerel@localhost:55432/postgres?sslmode=disable`, `Baglan` + `Migrate`, bağlanamazsa `t.Skip`, `t.Cleanup(havuz.Close)`. Her koşuda `federe_kimlikler` ve `users` temizlenir.

- [ ] **Step 3: Testi koş, düşmesini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run TestFedereDepoSozlesmesi -count=1`
Expected: FAIL — `undefined: FedereDepo`, `undefined: ErrKimlikYok`.

- [ ] **Step 4: Sahte depoyu yaz**

`federe_depo_sahte.go`: `map[string]string` ile `saglayici+"\x00"+sub -> userID`, ve `SahteUserStore`'a federe kullanıcı ekleyen bir yol. Faz 1'in sahte depoları gibi **derin kopya** döndürür (çağıran döndürülen `*User`'ı değiştirirse depo bozulmasın).

- [ ] **Step 5: Postgres deposunu yaz**

`federe_depo.go`. İki nokta kritik:

```go
// FedereKullaniciOlustur, sifresi OLMAYAN bir kullanici ile federe
// kimligini TEK ISLEMDE yazar. Islem zorunlu: iki ayri Exec arasinda
// hata olursa ortada sifresi de federe kimligi de olmayan, yani HIC
// GIRILEMEYEN bir hesap kalirdi -- ve e-posta tekil oldugu icin
// kullanici bir daha kaydolamazdi.
func (d *PostgresFedereDepo) FedereKullaniciOlustur(
	ctx context.Context, saglayici, sub, email, ad string, epostaDogrulandi bool,
) (*User, error) {
	islem, err := d.havuz.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("islem acilamadi: %w", err)
	}
	defer func() { _ = islem.Rollback(ctx) }()

	k := &User{
		ID:            uuid.NewString(),
		Email:         epostaNormalize(email),
		EmailVerified: epostaDogrulandi,
		Name:          strings.TrimSpace(ad),
		CreatedAt:     time.Now().UTC(),
	}
	// password_hash ACIKCA NULL: SifreDogru bos hash'te her zaman false
	// doner, yani bu hesaba sifreyle girilemez.
	if _, err := islem.Exec(ctx,
		`INSERT INTO users (id, email, email_verified, name, password_hash, created_at)
		 VALUES ($1, $2, $3, $4, NULL, $5)`,
		k.ID, k.Email, k.EmailVerified, k.Name, k.CreatedAt); err != nil {
		var pgHata *pgconn.PgError
		if errors.As(err, &pgHata) && pgHata.Code == "23505" {
			return nil, ErrEpostaKullanimda
		}
		return nil, fmt.Errorf("federe kullanici olusturulamadi: %w", err)
	}
	if _, err := islem.Exec(ctx,
		`INSERT INTO federe_kimlikler (saglayici, sub, user_id) VALUES ($1, $2, $3)`,
		saglayici, sub, k.ID); err != nil {
		var pgHata *pgconn.PgError
		if errors.As(err, &pgHata) && pgHata.Code == "23505" {
			return nil, ErrKimlikKullanimda
		}
		return nil, fmt.Errorf("federe kimlik yazilamadi: %w", err)
	}
	if err := islem.Commit(ctx); err != nil {
		return nil, fmt.Errorf("islem tamamlanamadi: %w", err)
	}
	return k, nil
}
```

`Bagla` iki unique index'i de `23505` olarak görür ve ikisinde de `ErrKimlikKullanimda` döner. `KimlikleKullanici` tek sorguda `JOIN` yapar ve `pgx.ErrNoRows` için `ErrKimlikYok` döner.

Dosyanın sonuna Faz 1'in kuralı:

```go
var (
	_ FedereDepo = (*PostgresFedereDepo)(nil)
	_ FedereDepo = (*SahteFedereDepo)(nil)
)
```

- [ ] **Step 6: Testleri koş**

Run: `cd core-service && go test ./internal/kimlik/ -run TestFedereDepoSozlesmesi -count=1 -v`
Expected: PASS. Postgres'li alt test atlanırsa, Postgres'i ayağa kaldırıp yeniden koş ve **atlanmadığını** gör (Faz 1'de atlanan testler gerçek bir açığı gizlemişti):

```bash
# Sifre, depodaki varsayilan DSN ile AYNI olmak zorunda; farkli olursa
# Baglan basarisiz olur ve test SESSIZCE ATLANIR.
docker run -d --name kimlik-test-pg -e POSTGRES_PASSWORD=kimlik-test-yerel -p 55432:5432 postgres:16-alpine
go test ./internal/kimlik/ -run TestFedereDepoSozlesmesi -count=1 -v
docker rm -f kimlik-test-pg
```

- [ ] **Step 7: Mutasyonla kanıtla**

Göçteki `federe_kimlikler_user_saglayici_key` index'ini geçici olarak sil ve testin düştüğünü gör; sonra geri koy. Aynısını `FedereKullaniciOlustur`'un `islem` sarmalını kaldırarak yapma — onun yerine `INSERT INTO federe_kimlikler` satırını geçici olarak hatalı bir sağlayıcı adıyla yaz ve `KimlikleKullanici`'nın düştüğünü gör. Her iki mutasyonu da ledger'a yaz.

- [ ] **Step 8: Commit**

```bash
git add core-service/internal/kimlik/migrations/003_federe_kimlik.sql \
        core-service/internal/kimlik/federe_depo.go \
        core-service/internal/kimlik/federe_depo_sahte.go \
        core-service/internal/kimlik/federe_depo_sozlesme_test.go
git commit -m "feat(kimlik): federe kimlik tablosu ve deposu"
```

---

### Task 2: JWKS doğrulayıcının yönünü çevir

Spec: "`jwks_verifier.go` silinmez, yönü değişir: Apple/Google `id_token`'larını federasyon sırasında doğrular."

**Files:**
- Modify: `core-service/internal/auth/jwks_verifier.go` (tamamı)
- Test: `core-service/internal/auth/jwks_verifier_test.go` (yeni)

**Interfaces:**
- Consumes: `github.com/MicahParks/keyfunc/v3`, `github.com/golang-jwt/jwt/v5`.
- Produces:
  - `type EsnekBool bool` + `func (e *EsnekBool) UnmarshalJSON([]byte) error`
  - `type Claims struct { UserID string "json:\"sub\""; Email string; EmailVerified EsnekBool; Name string; Nonce string "json:\"nonce\""; jwt.RegisteredClaims }`
  - `func NewJWKSVerifier(ctx context.Context, jwksURL, issuer, audience string) (*JWKSVerifier, error)`
  - `func (v *JWKSVerifier) DogrulaNonce(tokenString, nonce string) (*Claims, error)`
  - `func (v *JWKSVerifier) Close() error`

**Neden ctx parametresi:** K7. Yenileme goroutine'i çağıranın ctx'ine bağlanır; OP'nin yaşam süresi boyunca açık kalan bir ctx verilir.

- [ ] **Step 1: `EsnekBool` testini yaz (K8)**

```go
func TestEsnekBoolStringVeBoolKabulEder(t *testing.T) {
	for _, d := range []struct {
		ad      string
		girdi   string
		beklenen bool
	}{
		{"bool true", `{"v":true}`, true},
		{"bool false", `{"v":false}`, false},
		{"string true", `{"v":"true"}`, true},   // Apple boyle gonderebilir
		{"string false", `{"v":"false"}`, false},
	} {
		t.Run(d.ad, func(t *testing.T) {
			var s struct{ V EsnekBool }
			if err := json.Unmarshal([]byte(d.girdi), &s); err != nil {
				t.Fatalf("cozumlenemedi: %v", err)
			}
			if bool(s.V) != d.beklenen {
				t.Errorf("= %v, beklenen %v", bool(s.V), d.beklenen)
			}
		})
	}
}

// Apple'in string gonderme ihtimali bool'u ELDEN CIKARMAMALI: bozuk bir
// deger sessizce false'a dusmemeli, HATA vermeli -- aksi halde
// "email_verified: bozuk" gelen bir token dogrulanmis sayilmaz ama
// akis da neden kirildigini soylemez.
func TestEsnekBoolBozukDegerHataVerir(t *testing.T) {
	var s struct{ V EsnekBool }
	if err := json.Unmarshal([]byte(`{"v":"belki"}`), &s); err == nil {
		t.Error("bozuk deger kabul edildi")
	}
	if err := json.Unmarshal([]byte(`{"v":3}`), &s); err == nil {
		t.Error("sayi kabul edildi")
	}
}
```

- [ ] **Step 2: Alg sabitleme ve nonce testlerini yaz**

Test dosyası kendi RSA anahtarını üretir, `httptest` ile bir JWKS ucu yayınlar ve token imzalar:

```go
// sahteJWKS, verilen anahtarlari yayinlayan bir JWKS ucu acar ve URL'ini
// doner. anahtarlar degistirilirse SONRAKI istek yeni kumeyi gorur --
// rotasyon testi bunu kullanir.
func sahteJWKS(t *testing.T, anahtarlar func() []*rsa.PrivateKey) string

func TestAlgSabitlenir(t *testing.T) {
	// "none" ve HMAC ile imzalanmis tokenlar REDDEDILMELI. jwt/v5'in
	// anahtar tipi kontrolu bunu zaten yakalayabilir; yine de ACIKCA
	// sabitliyoruz, cunku korumanin kutuphanenin bir yan etkisine
	// dayanmasi onu her kutuphane yukseltmesinde kirilabilir kilar.
}

func TestNonceEslesmezseReddedilir(t *testing.T) {
	// Dogru imzali, dogru iss/aud'lu ama nonce'u BASKA bir token
	// DogrulaNonce'tan hata almali: aksi halde saldirgan kendi
	// yetkilendirmesinden aldigi gecerli bir id_token'i kurbanin
	// akisina enjekte edebilir (token replay).
}

func TestNonceBosOlamaz(t *testing.T) {
	// Hem token'daki hem beklenen nonce bos ise "esit" sayilmamali:
	// nonce'u hic gondermeyen bir saglayici yanitinin replay korumasi
	// yoktur ve bunu sessizce kabul etmek korumayi kaldirir.
}

func TestIssuerVeAudienceZorunlu(t *testing.T) {
	// Baska bir iss ile imzalanmis token reddedilir.
	// aud bizim client_id'mizi icermiyorsa reddedilir -- yani baska bir
	// uygulamaya verilmis bir Google id_token'i bize giris saglamaz.
}

func TestSuresiDolmusTokenReddedilir(t *testing.T) {}
```

- [ ] **Step 3: Anahtar rotasyonu testini yaz (K7'nin kanıtı)**

```go
// Google imzalama anahtarlarini duzenli dondurur. Yenileme olu ise bu
// test, rotasyondan sonraki ilk tokende DUSER -- ve uretimde bu, haftalar
// sonra butun girislerin sessizce kirilmasi demektir.
func TestAnahtarRotasyonuTakipEdilir(t *testing.T) {
	eski, _ := rsa.GenerateKey(rand.Reader, 2048)
	yeni, _ := rsa.GenerateKey(rand.Reader, 2048)

	kume := []*rsa.PrivateKey{eski}
	var kilit sync.Mutex
	url := sahteJWKS(t, func() []*rsa.PrivateKey {
		kilit.Lock()
		defer kilit.Unlock()
		return kume
	})

	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	v, err := NewJWKSVerifier(ctx, url, "https://ornek", "istemci")
	if err != nil {
		t.Fatalf("dogrulayici kurulamadi: %v", err)
	}
	defer v.Close()

	// Eski anahtarla imzali token gecer.
	if _, err := v.DogrulaNonce(tokenUret(t, eski, "https://ornek", "istemci", "n1"), "n1"); err != nil {
		t.Fatalf("eski anahtarli token reddedildi: %v", err)
	}

	// Saglayici anahtari dondurur.
	kilit.Lock()
	kume = []*rsa.PrivateKey{yeni}
	kilit.Unlock()

	// Yeni anahtarla imzali token da gecmeli. keyfunc bilinmeyen bir kid
	// gordugunde kumeyi yeniden ceker; yenileme ctx'i iptal edilmis olsa
	// bu ADIM DUSER.
	if _, err := v.DogrulaNonce(tokenUret(t, yeni, "https://ornek", "istemci", "n2"), "n2"); err != nil {
		t.Fatalf("rotasyon sonrasi token reddedildi (yenileme olu mu?): %v", err)
	}
}
```

- [ ] **Step 4: Testleri koş, düşmesini gör**

Run: `cd core-service && go test ./internal/auth/ -run "TestEsnekBool|TestAlg|TestNonce|TestIssuer|TestSuresi|TestAnahtarRotasyonu" -count=1`
Expected: FAIL — `NewJWKSVerifier` mevcut imzası `(*config.ZitadelConfig)`, `DogrulaNonce` ve `EsnekBool` yok.

- [ ] **Step 5: Doğrulayıcıyı yeniden yaz**

```go
// NewJWKSVerifier, bir YUKARI AKIS saglayicisinin (Apple, Google)
// id_token'larini dogrulayan bir dogrulayici kurar.
//
// ctx, arka plan anahtar yenilemesinin yasam suresidir ve CAGIRANIN
// omru kadar yasamalidir. Eskiden burada 30 saniyelik bir timeout ctx'i
// kurulup "defer cancel()" ile hemen iptal ediliyordu; yani anahtar
// kumesi bir daha HIC yenilenmiyordu. Zitadel'de fark edilmedi cunku
// anahtari degismiyordu. Google anahtarlarini dondurur: olu yenileme,
// rotasyondan sonra butun girislerin sessizce kirilmasi demektir.
// (TestAnahtarRotasyonuTakipEdilir bunu olcer.)
func NewJWKSVerifier(ctx context.Context, jwksURL, issuer, audience string) (*JWKSVerifier, error) {
	if jwksURL == "" || issuer == "" || audience == "" {
		return nil, fmt.Errorf("jwksURL, issuer ve audience zorunlu")
	}
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("JWKS kurulamadi: %w", err)
	}
	return &JWKSVerifier{jwks: jwks, issuer: issuer, audience: audience}, nil
}

// DogrulaNonce, id_token'i dogrular ve nonce'un BEKLENEN degerle
// birebir ayni oldugunu da zorunlu kilar.
//
// Nonce olmadan: saldirgan kendi Google yetkilendirmesinden aldigi
// gecerli bir id_token'i kurbanin yarim kalmis akisina enjekte eder ve
// kurbanin tarayicisinda saldirganin hesabina giris olur. Imza, iss ve
// aud bu tokende de DOGRUDUR; onu reddeden tek sey nonce'tur.
func (v *JWKSVerifier) DogrulaNonce(tokenString, nonce string) (*Claims, error) {
	if nonce == "" {
		return nil, fmt.Errorf("nonce bos olamaz")
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, v.jwks.Keyfunc,
		// Alg ACIKCA sabitlenir: korumanin kutuphanenin anahtar tipi
		// kontrolune dayanmasi onu her yukseltmede kirilabilir kilar.
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, fmt.Errorf("id_token dogrulanamadi: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("id_token claim'leri gecersiz")
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return nil, fmt.Errorf("nonce eslesmedi")
	}
	if strings.TrimSpace(claims.UserID) == "" {
		return nil, fmt.Errorf("sub bos")
	}
	return claims, nil
}
```

`EsnekBool`:

```go
// EsnekBool, JSON'da hem true/false hem "true"/"false" olarak gelebilen
// bir bool'dur. Apple email_verified'i bazi akislarda STRING gonderir;
// bool'a cozumlemeye calisan kod orada sessizce hata verir ve giris
// kirilir.
type EsnekBool bool

func (e *EsnekBool) UnmarshalJSON(b []byte) error {
	var bl bool
	if err := json.Unmarshal(b, &bl); err == nil {
		*e = EsnekBool(bl)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("email_verified ne bool ne string")
	}
	switch s {
	case "true":
		*e = true
	case "false":
		*e = false
	default:
		return fmt.Errorf("email_verified beklenmeyen string")
	}
	return nil
}
```

`discoverJWKSURL` ve `contains` **silinir**: JWKS URL'i artık yapılandırmadan gelir (Apple ve Google'ın jwks_uri'si sabittir ve her girişte discovery çekmek gereksiz bir ağ turu ve bir hata kaynağıdır). Eski `NewJWKSVerifier(cfg *config.ZitadelConfig)` imzası kalkar; `TokenVerifier` arayüzü `Validate` yerine `DogrulaNonce` tutar.

- [ ] **Step 6: Eski çağıranları bul ve düzelt**

Run: `cd core-service && grep -rn "NewJWKSVerifier\|TokenVerifier\|\.Validate(" --include=*.go . | grep -v _test.go`
Her çağıran ya federasyona bağlanır ya da kaldırılır. **Derleme kırılmış halde bırakılmaz.**

- [ ] **Step 7: Testleri koş**

Run: `cd core-service && go test ./internal/... -count=1`
Expected: tüm paketler PASS.

- [ ] **Step 8: Mutasyonla kanıtla**

Üç mutasyon, her biri ayrı ayrı: (a) `jwt.WithValidMethods` satırını kaldır → `TestAlgSabitlenir` düşmeli; (b) `subtle.ConstantTimeCompare` bloğunu kaldır → `TestNonceEslesmezseReddedilir` düşmeli; (c) `NewJWKSVerifier` içindeki ctx'i `ctx, cancel := context.WithTimeout(ctx, 30*time.Second); defer cancel()` ile eski haline döndür → `TestAnahtarRotasyonuTakipEdilir` düşmeli. Üçünü de geri al ve sonuçları ledger'a yaz. (c) düşmezse testin rotasyonu gerçekten zorlamadığı anlamına gelir; o zaman **test düzeltilir**, kod değil.

- [ ] **Step 9: Commit**

```bash
git add core-service/internal/auth/jwks_verifier.go core-service/internal/auth/jwks_verifier_test.go
git commit -m "refactor(auth): JWKS dogrulayicisini yukari akis saglayicilarina cevir"
```

---

### Task 3: Yukarı akış yapılandırması ve akış başlatma

**Files:**
- Create: `core-service/internal/kimlik/yukari_akis.go`
- Test: `core-service/internal/kimlik/yukari_akis_test.go`
- Modify: `core-service/internal/config/config.go` (`AuthConfig`)
- Modify: `core-service/.env.example`

**Interfaces:**
- Consumes: `*redis.Client`, `IstekDepo` (Redis anahtar biçimi için örnek), `auth.JWKSVerifier` (Task 2).
- Produces:
  - ```go
    type YukariAkis struct {
        Ad, IstemciID, IstemciSir         string
        AuthURL, TokenURL, JWKSURL, Issuer string
        Kapsamlar                          []string
        PKCEDestekli                       bool
        FormPost                           bool
        // Apple'a ozgu (K5). Google icin bos kalir.
        AppleEkipID, AppleAnahtarID, AppleAnahtarP8 string
    }
    ```
    Apple alanlari bu yapida DURMAK ZORUNDA: Gorev 4'teki `JetonDegis`, `client_secret`'i her degisimde bu uc alandan uretir.
  - `type FedereDurum struct { AuthRequestID, Nonce, Saglayici, Cerez, KodDogrulayici string }`
  - `type DurumDepo interface { DurumYaz(ctx context.Context, state string, d FedereDurum) error; DurumAl(ctx context.Context, state string) (FedereDurum, error) }` — `OturumDepo` gibi, `IstekDepo` tarafından karşılanır.
  - `var ErrDurumYok = errors.New("federasyon durumu bulunamadi")`
  - `func (y *YukariAkis) AuthorizeURL(state, nonce, kodDogrulayici, geriURL string) string`
  - `func kodDogrulayiciUret() (dogrulayici, meydanOkuma string, err error)`

- [ ] **Step 1: Durum deposunun tek kullanımlık olduğunu test et**

```go
// Durum kaydi TEK KULLANIMLIKTIR. Iki kez okunabilirse saldirgan
// yakaladigi bir callback URL'ini tekrar oynatabilir.
func TestDurumTekKullanimlik(t *testing.T) {
	rdb := testRedis(t)
	d := NewIstekDepo(rdb)
	ctx := context.Background()

	istenen := FedereDurum{
		AuthRequestID:  "istek-1",
		Nonce:          "nonce-1",
		Saglayici:      "google",
		Cerez:          "cerez-degeri",
		KodDogrulayici: "dogrulayici-1",
	}
	if err := d.DurumYaz(ctx, "state-1", istenen); err != nil {
		t.Fatalf("durum yazilamadi: %v", err)
	}
	okunan, err := d.DurumAl(ctx, "state-1")
	if err != nil {
		t.Fatalf("durum okunamadi: %v", err)
	}
	if okunan != istenen {
		t.Errorf("okunan = %+v, beklenen %+v", okunan, istenen)
	}
	if _, err := d.DurumAl(ctx, "state-1"); !errors.Is(err, ErrDurumYok) {
		t.Errorf("ikinci okuma err = %v, beklenen ErrDurumYok", err)
	}
}

func TestBilinmeyenDurumErrDurumYok(t *testing.T) {
	rdb := testRedis(t)
	d := NewIstekDepo(rdb)
	if _, err := d.DurumAl(context.Background(), "hic-yazilmadi"); !errors.Is(err, ErrDurumYok) {
		t.Errorf("err = %v, beklenen ErrDurumYok", err)
	}
}
```

- [ ] **Step 2: AuthorizeURL testini yaz**

```go
func TestAuthorizeURLGoogle(t *testing.T) {
	y := &YukariAkis{
		Ad:           "google",
		IstemciID:    "istemci-123",
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		Kapsamlar:    []string{"openid", "email", "profile"},
		PKCEDestekli: true,
	}
	u, err := url.Parse(y.AuthorizeURL("st", "nn", "dogrulayici-en-az-43-karakter-olmali-ki-gecerli-olsun", "https://biz/federasyon/google/geri"))
	if err != nil {
		t.Fatalf("URL cozumlenemedi: %v", err)
	}
	q := u.Query()
	for alan, beklenen := range map[string]string{
		"client_id":             "istemci-123",
		"response_type":         "code",
		"scope":                 "openid email profile",
		"state":                 "st",
		"nonce":                 "nn",
		"redirect_uri":          "https://biz/federasyon/google/geri",
		"code_challenge_method": "S256",
	} {
		if q.Get(alan) != beklenen {
			t.Errorf("%s = %q, beklenen %q", alan, q.Get(alan), beklenen)
		}
	}
	// Dogrulayicinin KENDISI degil, S256 ozeti gonderilir.
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge bos")
	}
	if strings.Contains(u.String(), "dogrulayici-en-az-43") {
		t.Error("kod dogrulayicisi URL'de duz metin gonderilmis")
	}
}

// K4: Apple'in PKCE destegi dokumante degil; bilinmeyen parametre
// gondermek akisi kirma riskidir.
func TestAuthorizeURLApplePKCEGondermez(t *testing.T) {
	y := &YukariAkis{
		Ad: "apple", IstemciID: "tr.makesinger.giris",
		AuthURL: "https://appleid.apple.com/auth/authorize",
		Kapsamlar: []string{"openid", "email", "name"},
		PKCEDestekli: false, FormPost: true,
	}
	u, _ := url.Parse(y.AuthorizeURL("st", "nn", "dogrulayici", "https://biz/federasyon/apple/geri"))
	q := u.Query()
	if q.Get("code_challenge") != "" || q.Get("code_challenge_method") != "" {
		t.Error("Apple'a PKCE parametresi gonderilmis")
	}
	// name/email scope'u istenince Apple form_post ile doner; bunu
	// ACIKCA istiyoruz, cunku geri donus handler'i POST'u beklemek
	// zorunda (bkz. K2).
	if q.Get("response_mode") != "form_post" {
		t.Errorf("response_mode = %q, beklenen form_post", q.Get("response_mode"))
	}
}
```

- [ ] **Step 3: Testleri koş, düşmesini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestDurum|TestBilinmeyenDurum|TestAuthorizeURL" -count=1`
Expected: FAIL — `undefined: YukariAkis`, `undefined: FedereDurum`.

- [ ] **Step 4: `yukari_akis.go`'yu yaz**

```go
// federasyonDurumTTL, kullanicinin yukari akis saglayicisinda gecirdigi
// sureye yetecek kadar uzun, calinmis bir state'in ise degerini hizla
// yitirmesine yetecek kadar kisa olmali. 10 dakika: Apple'in iki
// adimli onayi dahil bol bol yeterli.
const federasyonDurumTTL = 10 * time.Minute

func durumAnahtar(state string) string { return "kimlik:fed:" + state }

// DurumYaz / DurumAl: GETDEL ile TEK KULLANIMLIK. Kod degisimiyle ayni
// desen (bkz. IstekDepo.KodlaOku).
func (d *IstekDepo) DurumAl(ctx context.Context, state string) (FedereDurum, error) {
	veri, err := d.rdb.GetDel(ctx, durumAnahtar(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return FedereDurum{}, ErrDurumYok
	}
	...
}

// AuthorizeURL, yukari akis saglayicisinin authorize adresini kurar.
// redirect_uri BIZIM geri donus adresimizdir ve saglayici tarafinda
// ayniyla kayitli olmak zorundadir.
func (y *YukariAkis) AuthorizeURL(state, nonce, kodDogrulayici, geriURL string) string {
	q := url.Values{}
	q.Set("client_id", y.IstemciID)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(y.Kapsamlar, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("redirect_uri", geriURL)
	if y.FormPost {
		q.Set("response_mode", "form_post")
	}
	if y.PKCEDestekli {
		ozet := sha256.Sum256([]byte(kodDogrulayici))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(ozet[:]))
		q.Set("code_challenge_method", "S256")
	}
	return y.AuthURL + "?" + q.Encode()
}
```

- [ ] **Step 5: Yapılandırmayı ekle**

`internal/config/config.go`, `AuthConfig` içine:

```go
	// Federasyon, yukari akis saglayicilaridir. Bos ise federasyon
	// uclari HIC BAGLANMAZ ve giris sayfasinda buton gorunmez.
	Federasyon []FederasyonIstemci
```

```go
// FederasyonIstemci, bir yukari akis saglayicisinin yapilandirmasidir.
// Ad "google" veya "apple" olmalidir; URL'ler sabittir ve koddan
// gelir, ortamdan yalnizca kimlik bilgileri okunur.
type FederasyonIstemci struct {
	Ad        string // "google" | "apple"
	IstemciID string
	// Google icin duz client_secret. Apple icin BOS BIRAKILIR: Apple'in
	// client_secret'i her istekte ES256 ile uretilir (bkz. K5).
	IstemciSir string
	// Apple icin zorunlu: .p8 ozel anahtari (PEM), Team ID ve Key ID.
	AppleEkipID    string
	AppleAnahtarID string
	AppleAnahtarP8 string
}
```

Viper okuması: `AUTH_FED_GOOGLE_CLIENT_ID`, `AUTH_FED_GOOGLE_CLIENT_SECRET`, `AUTH_FED_APPLE_CLIENT_ID`, `AUTH_FED_APPLE_TEAM_ID`, `AUTH_FED_APPLE_KEY_ID`, `AUTH_FED_APPLE_KEY_P8`. Bir sağlayıcının **client_id'si boşsa o sağlayıcı listeye hiç eklenmez** — yarı yapılandırılmış bir sağlayıcıyla başlamak, kullanıcının butona basıp hata görmesi demektir.

Yapılandırma eksikse **sessizce sıfıra düşmez**: `Ad == "apple"` ise `AppleEkipID`, `AppleAnahtarID`, `AppleAnahtarP8` üçü de dolu olmak zorundadır, yoksa `Start` hata döner. (Faz 1'de "sessiz sıfır" sınıfı bir sorun olarak not edilmişti; burada baştan kapatıyoruz.)

- [ ] **Step 6: `.env.example`'ı güncelle**

```
# --- Federasyon (Faz 2). Bos ise Apple/Google butonlari gorunmez. ---
# Google: https://console.cloud.google.com -> OAuth 2.0 Client ID (Web application)
# Yetkili yonlendirme adresi: https://makesinger-auth.celalettindemir.dev/federasyon/google/geri
AUTH_FED_GOOGLE_CLIENT_ID=
AUTH_FED_GOOGLE_CLIENT_SECRET=
# Apple: Services ID + Sign in with Apple anahtari (.p8)
# Return URL: https://makesinger-auth.celalettindemir.dev/federasyon/apple/geri
AUTH_FED_APPLE_CLIENT_ID=
AUTH_FED_APPLE_TEAM_ID=
AUTH_FED_APPLE_KEY_ID=
AUTH_FED_APPLE_KEY_P8=
```

- [ ] **Step 7: Testleri koş**

Run: `cd core-service && go test ./internal/kimlik/ ./internal/config/ -count=1`
Expected: PASS. Redis gerektiren testler **atlanmamalı**; `redis-cli ping` ile Redis'in ayakta olduğunu doğrula.

- [ ] **Step 8: Commit**

```bash
git add core-service/internal/kimlik/yukari_akis.go core-service/internal/kimlik/yukari_akis_test.go \
        core-service/internal/config/config.go core-service/.env.example
git commit -m "feat(kimlik): yukari akis yapilandirmasi ve tek kullanimlik durum kaydi"
```

---

### Task 4: Kod → `id_token` değişimi (Google ve Apple)

**Files:**
- Create: `core-service/internal/kimlik/yukari_akis_jeton.go`
- Create: `core-service/internal/kimlik/yukari_akis_apple.go`
- Test: `core-service/internal/kimlik/yukari_akis_jeton_test.go`
- Test: `core-service/internal/kimlik/yukari_akis_apple_test.go`

**Interfaces:**
- Consumes: `YukariAkis` (Task 3).
- Produces:
  - `func (y *YukariAkis) JetonDegis(ctx context.Context, istemci *http.Client, kod, kodDogrulayici, geriURL string) (idToken string, err error)`
  - `func appleIstemciSir(ekipID, anahtarID, istemciID, p8PEM string, simdi time.Time) (string, error)`
  - `var ErrYukariAkisYanit = errors.New("yukari akis saglayicisi beklenmeyen yanit verdi")`

- [ ] **Step 1: Değişim testlerini yaz**

```go
// Sahte token ucu: istegi yakalar ki gonderilen alanlari dogrulayabilelim.
func TestJetonDegisGoogleAlanlariGonderir(t *testing.T) {
	var alinan url.Values
	sunucu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("metot = %s, beklenen POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		_ = r.ParseForm()
		alinan = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"sahte.jwt.imza"}`))
	}))
	defer sunucu.Close()

	y := &YukariAkis{Ad: "google", IstemciID: "ist", IstemciSir: "sir", TokenURL: sunucu.URL, PKCEDestekli: true}
	idToken, err := y.JetonDegis(context.Background(), sunucu.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
	if err != nil {
		t.Fatalf("degisim basarisiz: %v", err)
	}
	if idToken != "sahte.jwt.imza" {
		t.Errorf("id_token = %q", idToken)
	}
	for alan, beklenen := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "kod-1",
		"redirect_uri":  "https://biz/geri",
		"client_id":     "ist",
		"client_secret": "sir",
		"code_verifier": "dogrulayici-1",
	} {
		if alinan.Get(alan) != beklenen {
			t.Errorf("%s = %q, beklenen %q", alan, alinan.Get(alan), beklenen)
		}
	}
}

// PKCE desteklemeyen saglayiciya code_verifier GONDERILMEMELI (K4).
func TestJetonDegisPKCEsizSaglayiciyaDogrulayiciGondermez(t *testing.T) { /* alinan.Get("code_verifier") == "" */ }

// id_token DONMEZSE akis durmali. Bazi saglayicilar hata durumunda
// HTTP 200 ile {"error":...} doner; govdeye bakmadan 200'u basari
// saymak, bos bir id_token'i dogrulamaya gondermek demektir.
func TestJetonDegisIdTokenYokHataVerir(t *testing.T) {
	// {"access_token":"x"} dondur -> ErrYukariAkisYanit beklenir
	// {"error":"invalid_grant"} + 200 dondur -> ErrYukariAkisYanit beklenir
}

func TestJetonDegisHTTPHatasiYutulmaz(t *testing.T) {
	// 500 ve 400 -> hata doner, idToken bos
}

// Yukari akis cevap vermezse akis SONSUZA KADAR BEKLEMEMELI: zaman
// asimi olmayan bir istek, giris ucunu tutan goroutine'leri biriktirir.
func TestJetonDegisZamanAsimiUygular(t *testing.T) {
	// Sunucu 2 saniye bekler; 200 ms timeout'lu ctx ile cagir ->
	// context deadline hatasi beklenir.
}
```

- [ ] **Step 2: Apple `client_secret` testlerini yaz**

```go
// Apple client_secret'i ES256 ile imzalanmis bir JWT'dir. Imzayi
// .p8'den uretilen ozel anahtarin ACIK esiyle dogrulayarak alanlari
// kontrol ediyoruz -- yani testi gecmek icin dogru imzalamak sart.
func TestAppleIstemciSirAlanlari(t *testing.T) {
	p8 := testP8Uret(t) // ecdsa P-256 -> PKCS#8 PEM
	simdi := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	sir, err := appleIstemciSir("EKIP123", "ANAHTAR9", "tr.makesinger.giris", p8, simdi)
	if err != nil {
		t.Fatalf("client_secret uretilemedi: %v", err)
	}

	jeton, err := jwt.Parse(sir, func(t *jwt.Token) (any, error) { return testP8AcikAnahtar, nil },
		jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		t.Fatalf("client_secret dogrulanamadi: %v", err)
	}
	c := jeton.Claims.(jwt.MapClaims)
	if c["iss"] != "EKIP123" { t.Errorf("iss = %v", c["iss"]) }
	if c["sub"] != "tr.makesinger.giris" { t.Errorf("sub = %v", c["sub"]) }
	if c["aud"] != "https://appleid.apple.com" { t.Errorf("aud = %v", c["aud"]) }
	if jeton.Header["kid"] != "ANAHTAR9" { t.Errorf("kid = %v", jeton.Header["kid"]) }
	if jeton.Header["alg"] != "ES256" { t.Errorf("alg = %v", jeton.Header["alg"]) }
}

// K5: omur 5 dakika. Apple ustten 6 ay siniri koyuyor; biz her
// degisimde yeniden urettigimiz icin kisa tutmak serbest ve dogrudur.
func TestAppleIstemciSirOmru(t *testing.T) {
	// exp - iat == 5*60 ve iat == simdi
}

// RSA bir .p8 ile ES256 imzalanamaz; sessizce gecmemeli.
func TestAppleIstemciSirYanlisAnahtarTipiReddeder(t *testing.T) {}

func TestAppleIstemciSirBozukPEMReddeder(t *testing.T) {}
```

- [ ] **Step 3: Testleri koş, düşmesini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestJetonDegis|TestAppleIstemciSir" -count=1`
Expected: FAIL — `undefined: JetonDegis`, `undefined: appleIstemciSir`.

- [ ] **Step 4: `yukari_akis_jeton.go`'yu yaz**

```go
// yukariAkisZamanAsimi, yukari akis token ucuna uygulanan ust sinir.
// Zaman asimi olmadan cevap vermeyen bir saglayici, giris ucunu tutan
// goroutine'leri biriktirir.
const yukariAkisZamanAsimi = 10 * time.Second

func (y *YukariAkis) JetonDegis(
	ctx context.Context, istemci *http.Client, kod, kodDogrulayici, geriURL string,
) (string, error) {
	ctx, iptal := context.WithTimeout(ctx, yukariAkisZamanAsimi)
	defer iptal()

	sir := y.IstemciSir
	if y.Ad == saglayiciApple {
		var err error
		// K5: her degisimde yeniden uretilir, onbellege alinmaz.
		sir, err = appleIstemciSir(y.AppleEkipID, y.AppleAnahtarID, y.IstemciID, y.AppleAnahtarP8, time.Now())
		if err != nil {
			return "", fmt.Errorf("apple client_secret uretilemedi: %w", err)
		}
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", kod)
	form.Set("redirect_uri", geriURL)
	form.Set("client_id", y.IstemciID)
	form.Set("client_secret", sir)
	if y.PKCEDestekli {
		form.Set("code_verifier", kodDogrulayici)
	}

	istek, err := http.NewRequestWithContext(ctx, http.MethodPost, y.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("istek kurulamadi: %w", err)
	}
	istek.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	yanit, err := istemci.Do(istek)
	if err != nil {
		return "", fmt.Errorf("yukari akisa ulasilamadi: %w", err)
	}
	defer func() { _ = yanit.Body.Close() }()

	// Govde SINIRLI okunur: kotu niyetli ya da bozuk bir yanit
	// bellegi doldurmasin.
	govde, err := io.ReadAll(io.LimitReader(yanit.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("yanit okunamadi: %w", err)
	}
	if yanit.StatusCode != http.StatusOK {
		// HATA GOVDESI LOGLANMAZ ve KULLANICIYA GOSTERILMEZ: icinde
		// kod veya sir parcalari olabilir. Yalnizca durum kodu.
		return "", fmt.Errorf("%w: durum %d", ErrYukariAkisYanit, yanit.StatusCode)
	}
	var cozulen struct {
		IDToken string `json:"id_token"`
		Hata    string `json:"error"`
	}
	if err := json.Unmarshal(govde, &cozulen); err != nil {
		return "", fmt.Errorf("%w: govde cozumlenemedi", ErrYukariAkisYanit)
	}
	// 200 + {"error":...} GERCEK bir durumdur; govdeye bakmadan 200'u
	// basari saymak bos bir id_token'i dogrulamaya gondermek olur.
	if cozulen.Hata != "" || cozulen.IDToken == "" {
		return "", fmt.Errorf("%w: id_token yok", ErrYukariAkisYanit)
	}
	return cozulen.IDToken, nil
}
```

- [ ] **Step 5: `yukari_akis_apple.go`'yu yaz**

```go
const saglayiciApple = "apple"
const saglayiciGoogle = "google"

// appleIstemciSirOmru: K5. Her degisimde yeniden uretildigi icin kisa
// tutulur; Apple ustten 6 ay siniri koyar.
const appleIstemciSirOmru = 5 * time.Minute

// appleIstemciSir, Apple'in client_secret olarak bekledigi ES256 JWT'yi
// uretir. simdi parametre: test zamani sabitleyebilsin.
func appleIstemciSir(ekipID, anahtarID, istemciID, p8PEM string, simdi time.Time) (string, error) {
	blok, _ := pem.Decode([]byte(p8PEM))
	if blok == nil {
		return "", fmt.Errorf("apple .p8 anahtari PEM olarak cozumlenemedi")
	}
	ham, err := x509.ParsePKCS8PrivateKey(blok.Bytes)
	if err != nil {
		return "", fmt.Errorf("apple .p8 anahtari okunamadi: %w", err)
	}
	anahtar, ok := ham.(*ecdsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("apple anahtari ECDSA degil (%T); ES256 imzalanamaz", ham)
	}
	jeton := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": ekipID,
		"iat": simdi.Unix(),
		"exp": simdi.Add(appleIstemciSirOmru).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": istemciID,
	})
	jeton.Header["kid"] = anahtarID
	return jeton.SignedString(anahtar)
}
```

- [ ] **Step 6: Testleri koş**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestJetonDegis|TestAppleIstemciSir" -count=1 -v`
Expected: PASS, hiçbiri SKIP değil.

- [ ] **Step 7: Mutasyonla kanıtla**

(a) `cozulen.Hata != "" || cozulen.IDToken == ""` kontrolünü kaldır → `TestJetonDegisIdTokenYokHataVerir` düşmeli. (b) `if y.PKCEDestekli` koşulunu kaldırıp `code_verifier`'ı her zaman gönder → `TestJetonDegisPKCEsizSaglayiciyaDogrulayiciGondermez` düşmeli. (c) `jeton.Header["kid"]` satırını kaldır → `TestAppleIstemciSirAlanlari` düşmeli. Üçünü geri al, sonuçları ledger'a yaz.

- [ ] **Step 8: Commit**

```bash
git add core-service/internal/kimlik/yukari_akis_jeton.go core-service/internal/kimlik/yukari_akis_apple.go \
        core-service/internal/kimlik/yukari_akis_jeton_test.go core-service/internal/kimlik/yukari_akis_apple_test.go
git commit -m "feat(kimlik): kod-id_token degisimi ve Apple ES256 client_secret"
```

---

### Task 5: Federasyon uçları — başlat ve geri dönüş

**Files:**
- Create: `core-service/internal/kimlik/federe_sayfa.go`
- Test: `core-service/internal/kimlik/federe_sayfa_test.go`

**Interfaces:**
- Consumes: `YukariAkis`, `DurumDepo`, `FedereDepo`, `UserStore`, `IstekTamamlayici`, `auth.JWKSVerifier.DogrulaNonce`, `rasgeleJeton()` (`oturum.go`).
- Produces:
  - `type FedereSayfalar struct { ... }`
  - `func NewFedereSayfalar(akislar map[string]*YukariAkis, dogrulayicilar map[string]*auth.JWKSVerifier, durum DurumDepo, federe FedereDepo, kullanici UserStore, istekler IstekTamamlayici, geriCagirma func(context.Context, string) string, issuer string, cerezGuvenli bool) *FedereSayfalar`
  - `func (f *FedereSayfalar) Bagla(mux *http.ServeMux, araci *op.IssuerInterceptor, hiz *HizLimit)`
  - `const cerezAdFederasyon = "kimlik_fed"`
  - `func (f *FedereSayfalar) Baslat(w http.ResponseWriter, r *http.Request)`
  - `func (f *FedereSayfalar) Geri(w http.ResponseWriter, r *http.Request)`
  - `const hataIstekGecersiz = "Giris istegi gecersiz, lutfen uygulamadan tekrar deneyin."`
  - `func (f *FedereSayfalar) hataSayfasi(w http.ResponseWriter, kod int, metin string)`

**Yollar:** `GET /federasyon/{saglayici}/baslat`, `GET /federasyon/{saglayici}/geri`, `POST /federasyon/{saglayici}/geri` (Apple `form_post`, bkz. K2).

- [ ] **Step 1: Başlatma testlerini yaz**

```go
// Baslatma, istegi BASLATAN tarayiciya baglanmali: Faz 1'in C1
// (hesap devralma) acigini federasyon tarafindan yeniden acmamak icin.
func TestBaslatCerezKoyarVeDurumYazar(t *testing.T) {
	// GET /federasyon/google/baslat?authRequestID=<gecerli istek>
	// -> 302, Location yukari akis authorize URL'i
	// -> Set-Cookie: kimlik_fed; HttpOnly; Secure; SameSite=None; Path=/federasyon
	// -> Redis'te state kaydi var ve icinde Cerez = cerez degeri
}

// K2: Apple'in form_post dönüşü CROSS-SITE'tir ve SameSite=Lax cerez
// o POST'ta GONDERILMEZ. Bu yuzden federasyon cerezi None olmali --
// ama HttpOnly ve Secure'dan VAZGECILMEZ.
func TestFederasyonCereziSameSiteNone(t *testing.T) {
	// cerez.SameSite == http.SameSiteNoneMode
	// cerez.HttpOnly == true, cerez.Secure == true (issuer https ise)
	// cerez.Path == "/federasyon"  (oturum cerezini EZMEMELI)
}

// Oturum cerezi DEGISMEMELI: SameSite=None'a cevirmek Faz 1'in
// login-CSRF korumasini zayiflatirdi.
func TestBaslatOturumCerezineDokunmaz(t *testing.T) {
	// Yanitta kimlik_oturum adli bir Set-Cookie OLMAMALI
}

func TestBaslatBilinmeyenSaglayici404(t *testing.T) {}
func TestBaslatGecersizAuthRequestIDReddedilir(t *testing.T) {}

// Yapilandirilmamis saglayici butonu hic gorunmedigi icin buraya
// gelinmemeli; gelinirse 404, "500" degil.
func TestBaslatYapilandirilmamisSaglayici404(t *testing.T) {}
```

- [ ] **Step 2: Geri dönüş testlerini yaz**

```go
// Mutlu yol: bilinen (saglayici, sub) -> o kullaniciya giris.
func TestGeriBilinenKimlikGirisYapar(t *testing.T) {
	// sahte yukari akis OP'si (httptest) ile:
	//  - state ve cerez dogru
	//  - id_token dogru imzali, iss/aud/nonce dogru
	// Beklenen: 302 /authorize/callback?id=<authRequestID>
	//           ve istek Subject = mevcut kullanicinin ID'si
}

// Yeni kimlik -> hesap olusturulur ve giris olur.
func TestGeriYeniKimlikHesapAcar(t *testing.T) {
	// users'ta yeni kayit, password_hash NULL,
	// federe_kimlikler'de (saglayici, sub) -> yeni user_id
}

// --- SALDIRI TESTLERI ---

// state yok veya bilinmiyor: akis durmali. Bu, callback'i tahmin edip
// kurbanin istegini tamamlamaya calisan saldirganin ilk denemesidir.
func TestGeriBilinmeyenStateReddedilir(t *testing.T) {}

// state TEK KULLANIMLIK: ayni callback iki kez oynatilamaz.
func TestGeriStateTekrariReddedilir(t *testing.T) {}

// K2'nin kanitlanmasi: state DOGRU ama cerez YOK/YANLIS ise
// reddedilmeli. Aksi halde saldirgan kendi yetkilendirmesinden aldigi
// gecerli bir state+code ciftini kurbanin tarayicisinda acarak
// kurbanin akisini kendi hesabina tamamlatabilir.
func TestGeriCerezEslesmezseReddedilir(t *testing.T) {
	// 1) Saldirgan /federasyon/google/baslat ile gecerli bir state alir
	//    (cerezi SALDIRGANIN tarayicisinda).
	// 2) Ayni state ile /federasyon/google/geri'ye CEREZSIZ gelinir.
	// Beklenen: 403, istek TAMAMLANMAZ (Subject bos kalir).
}

// nonce: Task 2'deki DogrulaNonce'un akista gercekten cagrildiginin
// kaniti. Yukari akis BASKA bir nonce'lu id_token dondururse akis
// durmali.
func TestGeriNonceEslesmezseReddedilir(t *testing.T) {}

// Imzasi gecersiz veya baska bir anahtarla imzalanmis id_token
// reddedilmeli.
func TestGeriGecersizImzaReddedilir(t *testing.T) {}

// Yukari akis aud'u BASKA bir istemciye verilmis bir id_token
// dondururse reddedilmeli (baska uygulamanin tokeniyle giris).
func TestGeriYanlisAudienceReddedilir(t *testing.T) {}

// authRequestID BASKA bir kullaniciya zaten tamamlanmissa
// ErrIstekZatenTamamlandi yolundan reddedilmeli (Faz 1'in kurali
// federasyonda da gecerli).
func TestGeriTamamlanmisIstekBaskaKullaniciyaReddedilir(t *testing.T) {}

// Hicbir log satirinda state, kod, id_token, cerez veya e-posta
// GECMEMELI. Faz 1'de bu bir Critical'di.
func TestGeriLoglarSizdirmaz(t *testing.T) {
	// log.SetOutput(&tampon) ile butun yollari (mutlu + hatali) kostur,
	// tamponda state/kod/eposta/cerez degerlerini ARA.
}
```

- [ ] **Step 3: Testleri koş, düşmesini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestBaslat|TestGeri|TestFederasyonCerezi" -count=1`
Expected: FAIL — `undefined: FedereSayfalar`.

- [ ] **Step 4: `federe_sayfa.go`'yu yaz**

Akışın iskeleti:

```go
func (f *FedereSayfalar) Baslat(w http.ResponseWriter, r *http.Request) {
	akis, ok := f.akislar[r.PathValue("saglayici")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("authRequestID")
	// Istek GERCEKTEN var olmali: var olmayan bir id ile yukari akisa
	// gitmek, donuste tamamlanacak hicbir sey olmadigi icin
	// kullaniciyi anlamsiz bir hataya goturur.
	if _, err := f.istekOku(r.Context(), id); err != nil {
		f.hataSayfasi(w, http.StatusBadRequest, hataIstekGecersiz)
		return
	}

	state, err := rasgeleJeton()
	nonce, err2 := rasgeleJeton()
	cerezDeger, err3 := rasgeleJeton()
	// HATALAR YUTULMAZ: zayif/bos bir state veya nonce korumayi
	// fiilen kaldirir.
	if err != nil || err2 != nil || err3 != nil {
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	dogrulayici, _, derr := kodDogrulayiciUret()
	if derr != nil { /* ayni sekilde durdur */ }

	if err := f.durum.DurumYaz(r.Context(), state, FedereDurum{
		AuthRequestID: id, Nonce: nonce, Saglayici: akis.Ad,
		Cerez: cerezDeger, KodDogrulayici: dogrulayici,
	}); err != nil {
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	http.SetCookie(w, f.federasyonCerezi(cerezDeger))
	http.Redirect(w, r, akis.AuthorizeURL(state, nonce, dogrulayici, f.geriURL(akis.Ad)), http.StatusFound)
}

// federasyonCerezi: K2. SameSite=None ZORUNLU cunku Apple form_post
// ile CROSS-SITE POST yapar ve Lax cerez o POST'ta gonderilmez.
// HttpOnly ve Secure'dan vazgecilmez; Path federasyon altina
// SINIRLANIR ki oturum cerezinin yuzeyini genisletmesin.
func (f *FedereSayfalar) federasyonCerezi(deger string) *http.Cookie {
	return &http.Cookie{
		Name:     cerezAdFederasyon,
		Value:    deger,
		Path:     "/federasyon",
		HttpOnly: true,
		Secure:   f.cerezGuvenli,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   int(federasyonDurumTTL.Seconds()),
	}
}

func (f *FedereSayfalar) Geri(w http.ResponseWriter, r *http.Request) {
	// K3: caprazSiteIstek guard'i BURAYA UYGULANMAZ -- donus tanimi
	// geregi cross-site'tir. Yerine state (tek kullanimlik) + cerez
	// (K2) + nonce ucgeni gecer.
	state, kod := f.stateVeKod(r) // GET'te query, POST'ta form
	d, err := f.durum.DurumAl(r.Context(), state) // GETDEL: tek kullanimlik
	if err != nil {
		f.hataSayfasi(w, http.StatusForbidden, hataOturum)
		return
	}
	cerez, err := r.Cookie(cerezAdFederasyon)
	if err != nil || subtle.ConstantTimeCompare([]byte(cerez.Value), []byte(d.Cerez)) != 1 {
		// Bu dal, baska bir tarayicida baslatilmis bir akisin bu
		// tarayicida tamamlanmasini engeller (hesap devralma).
		f.hataSayfasi(w, http.StatusForbidden, hataOturum)
		return
	}
	akis := f.akislar[d.Saglayici] // state'teki saglayici YOLDAKINE degil KAYDA gore
	...
	idToken, err := akis.JetonDegis(r.Context(), f.istemci, kod, d.KodDogrulayici, f.geriURL(akis.Ad))
	...
	iddia, err := f.dogrulayicilar[akis.Ad].DogrulaNonce(idToken, d.Nonce)
	...
	// kullaniciCoz UC deger doner (bkz. Gorev 6): kullanici, cakisma, hata.
	// Cakisma nil DEGILSE giris YAPILMAZ -- baglama ya da 409 sayfasina gidilir.
	ku, cakisma, err := f.kullaniciCoz(r.Context(), akis.Ad, iddia)
	if err != nil {
		f.hataSayfasi(w, http.StatusInternalServerError, hataSunucu)
		return
	}
	if cakisma != nil {
		f.cakismaYonlendir(w, r, d.AuthRequestID, cakisma) // Gorev 6
		return
	}
	if err := f.istekler.TamamlandiIsaretle(r.Context(), d.AuthRequestID, ku.ID); err != nil { ... }
	http.Redirect(w, r, f.geriCagirma(r.Context(), d.AuthRequestID), http.StatusFound)
}
```

**Sağlayıcı yoldan değil kayıttan okunur:** `d.Saglayici`, `r.PathValue` değil. Aksi halde saldırgan Google'da başlatılmış bir akışı `/federasyon/apple/geri`'ye getirip doğrulayıcı seçimini karıştırabilir.

Log satırları yalnızca sınıf bilgisi taşır, değer taşımaz:

```go
log.Printf("federasyon: durum dogrulanamadi (%s)", akisAdiGuvenli)
log.Printf("federasyon: id_token dogrulanamadi: %v", err) // err icinde token GECMEZ
```

- [ ] **Step 5: Testleri koş**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestBaslat|TestGeri|TestFederasyonCerezi" -count=1 -v`
Expected: PASS, hiçbiri SKIP değil.

- [ ] **Step 6: Mutasyonla kanıtla — en önemli adım**

Her mutasyon ayrı ayrı, ardından geri al:

| Mutasyon | Düşmesi gereken test |
|---|---|
| Çerez karşılaştırma bloğunu kaldır | `TestGeriCerezEslesmezseReddedilir` |
| `DurumAl`'ı `GETDEL` yerine `GET` yap | `TestGeriStateTekrariReddedilir` |
| `DogrulaNonce` yerine nonce'suz doğrulama çağır | `TestGeriNonceEslesmezseReddedilir` |
| `d.Saglayici` yerine `r.PathValue("saglayici")` kullan | Sağlayıcı karıştırma testi (Step 2'ye ekle) |
| `SameSite: http.SameSiteNoneMode` → `LaxMode` | `TestFederasyonCereziSameSiteNone` |

Beşinin de sonucunu ledger'a yaz. **Düşmeyen bir mutasyon, testin o korumayı ölçmediği anlamına gelir; o durumda test düzeltilir.**

- [ ] **Step 7: Commit**

```bash
git add core-service/internal/kimlik/federe_sayfa.go core-service/internal/kimlik/federe_sayfa_test.go
git commit -m "feat(kimlik): federasyon baslat ve geri donus uclari"
```

---

### Task 6: Çakışma yolları — bağlama sayfası, 409 sayfası, Apple gizli aktarma

Spec'in en çok gerekçelendirdiği kısım burası: **e-posta üzerinden otomatik bağlama yapılmaz.**

**Files:**
- Create: `core-service/internal/kimlik/baglama_sayfa.go`
- Create: `core-service/internal/kimlik/sablonlar/baglama.html`
- Create: `core-service/internal/kimlik/sablonlar/cakisma.html`
- Modify: `core-service/internal/kimlik/federe_sayfa.go` (`kullaniciCoz`)
- Test: `core-service/internal/kimlik/baglama_sayfa_test.go`

**Interfaces:**
- Consumes: `FedereDepo`, `UserStore`, `User.SifreDogru`, `IstekTamamlayici`, `OturumDepo`, `csrfEsit` (`oturum.go`), `hataGirisBasarisiz` (`sayfa.go`).
- Produces:
  - `func (f *FedereSayfalar) kullaniciCoz(ctx context.Context, saglayici string, iddia *auth.Claims) (*User, *Cakisma, error)`
  - `type Cakisma struct { BeklemeID string; Eposta string; MevcutSaglayicilar []string; SifreliHesap bool }`
  - `func (f *FedereSayfalar) BaglamaBagla(mux *http.ServeMux, araci *op.IssuerInterceptor, hiz *HizLimit)`
  - `func (f *FedereSayfalar) baglamaGet(w http.ResponseWriter, r *http.Request)` / `baglamaPost(...)`
  - `func (f *FedereSayfalar) cakismaYonlendir(w http.ResponseWriter, r *http.Request, authRequestID string, c *Cakisma)`

**Ayri bir tip ACILMAZ:** `/baglama`, federasyon akisinin ortasindaki bir adimdir ve `FedereSayfalar`'in zaten tuttugu depolarin (`FedereDepo`, `UserStore`, `IstekTamamlayici`, `DurumDepo`) aynisina ihtiyac duyar. Ikinci bir tip, ayni bagimliliklari ikinci kez kablolamak ve iki yerde ayri ayri bozulabilen bir kurulum demek olurdu.
  - Yollar: `GET /baglama`, `POST /baglama`
  - `const gizliAktarmaSoneki = "@privaterelay.appleid.com"`

**Çözüm sırası (`kullaniciCoz`):** sıra güvenliğin kendisidir, değiştirilmemeli.

1. `(saglayici, sub)` ile ara → bulunursa **giriş**. (Bağlanmış hesap her zaman önce gelir; e-postası sonradan değişmiş olabilir.)
2. Bulunamadı. E-posta `@privaterelay.appleid.com` ile bitiyorsa → **çakışma kontrolü hiç yapılmaz**, doğrudan yeni hesap. (Spec: gizli aktarma adresi hiçbir koşulda bağlama için güvenilir değildir.)
3. E-posta ile mevcut hesap ara. Yoksa → **yeni hesap**.
4. Var ve **şifresi var** → bağlama sayfası (şifre bir kez sorulur).
5. Var ve **şifresi yok** (K1) → **409 çakışma sayfası**, bağlama yok, yeni hesap yok.

- [ ] **Step 1: Çözüm sırası testlerini yaz**

```go
// 1. Bagli kimlik HER ZAMAN once gelir: kullanicinin Google'daki
//    e-postasi degismis olabilir ve o zaman e-postaya gore arama
//    BASKA bir hesabi (ya da hic) bulur.
func TestCozumBagliKimlikEpostadanOnceGelir(t *testing.T) {
	// (google, sub-1) -> kullanici A (eposta a@x.com)
	// id_token: sub=sub-1, email=YENI@x.com (B'ye ait)
	// Beklenen: A'ya giris, cakisma YOK
}

// 2. Apple gizli aktarma adresi ASLA baglama icin kullanilmaz.
//    Bu, spec'in acikca yasakladigi tek durumdur.
func TestGizliAktarmaAdresiBaglamaTetiklemez(t *testing.T) {
	// users'ta "abc@privaterelay.appleid.com" ile SIFRELI bir hesap var
	// (saldirgan onu acmis olabilir).
	// Apple'dan ayni adresle yeni bir sub gelir.
	// Beklenen: baglama sayfasi DEGIL, yeni hesap... (bkz. Step 2)
}

// 4. Sifreli hesap: baglama sayfasi.
func TestEpostaCakismasiSifreliHesapBaglamayaGoturur(t *testing.T) {
	// Beklenen: Cakisma{SifreliHesap: true}, kullanici nil
}

// 5. K1: sifresiz hesap -> 409, baglama YOK, yeni hesap YOK.
func TestEpostaCakismasiSifresizHesapEngellenir(t *testing.T) {
	// users'ta Google ile acilmis sifresiz hesap var.
	// Apple'dan ayni e-postayla gelinir.
	// Beklenen: Cakisma{SifreliHesap: false, MevcutSaglayicilar: ["google"]}
	//           kullanici nil, YENI KAYIT OLUSMAZ
}

// Spec'in somut saldirisi: saldirgan kurbanin adresiyle sifreli hesap
// acar; kurban Google ile girer. OTOMATIK BAGLANMAMALI.
func TestSaldirganinOnceActigiHesabaSessizceBaglanmaz(t *testing.T) {
	// users: kurban@x.com, sifre = SALDIRGANIN sifresi
	// Google: email=kurban@x.com, email_verified=true, yeni sub
	// Beklenen: kullanici nil (giris OLMAZ), Cakisma doner.
	// email_verified=true OLSA BILE baglanmamali -- testin asil noktasi.
}
```

- [ ] **Step 2: Gizli aktarma + e-posta tekilliği çakışmasına karar ver ve test et**

Gizli aktarma adresi 2. adımda doğrudan yeni hesaba gider, ama `UNIQUE (lower(email))` yüzünden adres zaten varsa `INSERT` `ErrEpostaKullanimda` döner. Bu, kullanıcı hesabını silip tekrar kaydolduğunda (Apple relay adresi aynı kalır) gerçekten olabilir.

**Karar:** bu durumda da **409 çakışma sayfası** gösterilir — asla bağlama. Gerekçe: o satırın gerçekten aynı kişiye ait olduğunu kanıtlayamayız, ve spec gizli aktarma adresini bağlama için güvenilmez ilan etmiştir.

```go
func TestGizliAktarmaAdresiZatenVarsa409(t *testing.T) {
	// users'ta "abc@privaterelay.appleid.com" var (sub BASKA).
	// Apple yeni bir sub ile ayni adresi getirir.
	// Beklenen: 409 cakisma sayfasi; baglama YOK, yeni hesap YOK.
}
```

- [ ] **Step 3: Bağlama sayfası testlerini yaz**

```go
// Baglama sayfasi Faz 1'in giris sayfasiyla AYNI korumalari tasir:
// CSRF, oturum baglamasi, hiz limiti, var-yok sizdirmayan hata metni.

func TestBaglamaGetBaglamasizReddedilir(t *testing.T) {
	// Faz 1'in C1 dersi: form, baglama YOKSA HIC RENDER EDILMEZ.
	// Beklenen: 403 ve govdede <form> GECMEZ.
}

func TestBaglamaPostCSRFYanlisReddedilir(t *testing.T) {}

func TestBaglamaPostDogruSifreBaglarVeGirisYapar(t *testing.T) {
	// federe_kimlikler'e (saglayici, sub) -> mevcut user_id yazilir,
	// istek tamamlanir, 302 /authorize/callback
}

func TestBaglamaPostYanlisSifreBaglamaz(t *testing.T) {
	// Beklenen: 401, federe_kimlikler BOS kalir, hata metni
	// hataGirisBasarisiz ("E-posta veya sifre hatali.")
}

// Baglama beklemesi TEK KULLANIMLIK olmali: dogru sifre girildikten
// sonra ayni BeklemeID ile ikinci bir baglama yapilamaz.
func TestBaglamaBeklemesiTekKullanimlik(t *testing.T) {}

// Saldirgan BASKA birinin beklemesini kendi tarayicisinda
// tamamlayamamali.
func TestBaglamaBaskaTarayicidaTamamlanamaz(t *testing.T) {}

// Sifre denemesi SINIRSIZ olmamali: baglama sayfasi, sifre deneme
// yuzeyinin IKINCISIDIR ve Faz 1'in giris limitinden ayri bir uctur.
func TestBaglamaHizLimitiUygulanir(t *testing.T) {}

// Sifresiz hesap baglama sayfasina HIC gelmemeli; gelirse 409.
func TestBaglamaSifresizHesapIcinAcilmaz(t *testing.T) {}
```

- [ ] **Step 4: Testleri koş, düşmesini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestCozum|TestGizliAktarma|TestEpostaCakismasi|TestSaldirganin|TestBaglama" -count=1`
Expected: FAIL — `undefined: kullaniciCoz`, `undefined: Cakisma`.

- [ ] **Step 5: `kullaniciCoz`'u yaz**

```go
// kullaniciCoz, dogrulanmis bir yukari akis id_token'indan bizim
// kullanicimizi bulur.
//
// SIRA GUVENLIGIN KENDISIDIR:
//  1) (saglayici, sub) -- bagli hesap her zaman once. Kullanicinin
//     saglayicidaki e-postasi degisebilir; e-postaya gore aramak onu
//     BASKA bir hesaba goturebilirdi.
//  2) Gizli aktarma adresi -> cakisma kontrolu YOK (spec yasagi).
//  3) E-posta ile hesap yok -> yeni hesap.
//  4) Hesap var ve SIFRELI -> baglama sayfasi (sifre bir kez).
//  5) Hesap var ve SIFRESIZ -> 409 (K1).
//
// 4 ve 5'te OTOMATIK BAGLAMA YAPILMAZ, email_verified true olsa bile.
// Spec'teki saldiri: e-posta dogrulamasi yapmadigimiz icin saldirgan
// kurbanin adresiyle sifreli bir hesap acabilir; kurban Google ile
// girip otomatik baglanirsa SIFRESI SALDIRGANDA olan bir hesaba duser.
func (f *FedereSayfalar) kullaniciCoz(
	ctx context.Context, saglayici string, iddia *auth.Claims,
) (*User, *Cakisma, error) {
	ku, err := f.federe.KimlikleKullanici(ctx, saglayici, iddia.UserID)
	if err == nil {
		return ku, nil, nil
	}
	if !errors.Is(err, ErrKimlikYok) {
		return nil, nil, err
	}

	eposta := epostaNormalize(iddia.Email)
	gizliAktarma := strings.HasSuffix(strings.ToLower(eposta), gizliAktarmaSoneki)

	if !gizliAktarma {
		mevcut, err := f.kullanici.ByEmail(ctx, eposta)
		switch {
		case err == nil && mevcut.PasswordHash != "":
			c, err := f.cakismaKur(ctx, mevcut, eposta, saglayici, iddia)
			return nil, c, err
		case err == nil:
			sag, _ := f.federe.SaglayicilarByUserID(ctx, mevcut.ID)
			return nil, &Cakisma{Eposta: eposta, MevcutSaglayicilar: sag, SifreliHesap: false}, nil
		case !errors.Is(err, ErrKullaniciYok):
			return nil, nil, err
		}
	}

	yeni, err := f.federe.FedereKullaniciOlustur(
		ctx, saglayici, iddia.UserID, eposta, iddia.Name, bool(iddia.EmailVerified))
	if errors.Is(err, ErrEpostaKullanimda) {
		// Gizli aktarma adresi zaten kayitli (Step 2'nin karari) ya da
		// arada bir yaris olustu. Her iki halde de BAGLAMA YOK.
		return nil, &Cakisma{Eposta: eposta, SifreliHesap: false}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return yeni, nil, nil
}
```

`cakismaKur`, bekleyen bağlamayı Redis'e yazar (`kimlik:baglama:<BeklemeID>`, TTL 10 dakika, tek kullanımlık) ve içinde `authRequestID`, `userID`, `saglayici`, `sub`, federasyon çerez değeri ve yeni bir CSRF jetonu tutar.

- [ ] **Step 6: Bağlama sayfasını ve şablonları yaz**

`baglama.html`, `giris.html`'in biçimini izler; **e-posta alanı salt okunur ve sabittir** (kullanıcı başka bir hesabın şifresini denemeye yönlendirilmesin):

```html
  <h1>Hesabini bagla</h1>
  <p>{{.Eposta}} adresiyle zaten bir hesabin var. Baglamak icin mevcut sifreni gir.</p>
  {{if .Hata}}<p class="hata">{{.Hata}}</p>{{end}}
  <form method="post" action="/baglama">
    <input type="hidden" name="beklemeID" value="{{.BeklemeID}}">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <label for="sifre">Mevcut sifren</label>
    <input type="password" id="sifre" name="sifre" required autofocus>
    <button type="submit">Bagla ve giris yap</button>
  </form>
```

`cakisma.html` (K1, 409):

```html
  <h1>Bu e-posta baska bir yolla kayitli</h1>
  <p>{{.Eposta}} adresi {{.SaglayiciMetni}} ile kayitli. Lutfen {{.SaglayiciMetni}} ile giris yap.</p>
  <p class="alt"><a href="/giris?authRequestID={{.AuthRequestID}}">Giris sayfasina don</a></p>
```

Şablona `Cakisma` **doğrudan geçilmez** — kendi görünüm yapısı vardır:

```go
// cakismaVeri, cakisma.html'in bekledigi alanlardir. Cakisma yapisi
// dogrudan gecilmez: sablon, kullaniciya GOSTERILECEK metni ister,
// saglayici listesini degil.
type cakismaVeri struct {
	Eposta         string
	SaglayiciMetni string // "Google", "Apple" veya "baska bir yontem"
	AuthRequestID  string
}

// saglayiciMetni, listeyi kullaniciya gosterilecek tek bir ifadeye
// cevirir. Liste BOS olabilir (ornegin gizli aktarma adresi zaten
// kayitliysa); o zaman saglayici adi UYDURULMAZ.
func saglayiciMetni(sag []string) string {
	switch {
	case len(sag) == 0:
		return "baska bir yontem"
	case len(sag) == 1:
		return saglayiciEtiket(sag[0]) // "google" -> "Google"
	default:
		return strings.Join(saglayiciEtiketler(sag), " veya ")
	}
}
```

- [ ] **Step 7: Testleri koş**

Run: `cd core-service && go test ./internal/kimlik/ -count=1 -v 2>&1 | grep -cE "^(=== RUN|--- SKIP)"`
Beklenen: SKIP sayısı **0**. Sonra tüm paket: `go test ./internal/kimlik/ -count=1`.

- [ ] **Step 8: Mutasyonla kanıtla**

| Mutasyon | Düşmesi gereken test |
|---|---|
| 4. adımı atla: e-posta eşleşince doğrudan `Bagla` çağır (otomatik bağlama) | `TestSaldirganinOnceActigiHesabaSessizceBaglanmaz` |
| `gizliAktarma` kontrolünü kaldır | `TestGizliAktarmaAdresiBaglamaTetiklemez` |
| 1. ve 3. adımı yer değiştir (e-posta önce) | `TestCozumBagliKimlikEpostadanOnceGelir` |
| K1 dalını yeni hesap açmaya çevir | `TestEpostaCakismasiSifresizHesapEngellenir` |
| `SifreDogru` kontrolünü kaldır | `TestBaglamaPostYanlisSifreBaglamaz` |
| Bekleme kaydını `GETDEL` yerine `GET` ile oku | `TestBaglamaBeklemesiTekKullanimlik` |

Altısını da ledger'a yaz.

- [ ] **Step 9: Commit**

```bash
git add core-service/internal/kimlik/baglama_sayfa.go core-service/internal/kimlik/baglama_sayfa_test.go \
        core-service/internal/kimlik/federe_sayfa.go core-service/internal/kimlik/sablonlar/baglama.html \
        core-service/internal/kimlik/sablonlar/cakisma.html
git commit -m "feat(kimlik): hesap baglama sayfasi ve cakisma kurallari"
```

---

### Task 7: `amr` claim'i istek başına hesaplanır

K6. Bugün `istek.go:69` sabit `[]string{"pwd"}` dönüyor; Google ile giren kullanıcının token'ında "pwd" yazması yanlış bir beyandır.

**Files:**
- Modify: `core-service/internal/kimlik/istek.go:69` ve `AuthIstek`
- Modify: `core-service/internal/kimlik/istek.go` (`TamamlandiIsaretle`)
- Modify: `core-service/internal/kimlik/sayfa.go` (şifreyle giriş yolu)
- Modify: `core-service/internal/kimlik/federe_sayfa.go`, `baglama_sayfa.go`
- Test: `core-service/internal/kimlik/istek_test.go`

**Interfaces:**
- Produces:
  - `AuthIstek.AMR []string`
  - `func (a *AuthIstek) GetAMR() []string` — alanı döner; alan boşsa `[]string{"pwd"}` **DEĞİL**, boş dilim döner.
  - `func (d *IstekDepo) TamamlandiIsaretle(ctx context.Context, id, userID string, amr []string) error` — imza değişir.
  - `const amrSifre = "pwd"`, `const amrFederasyon = "fed"`
  - `IstekTamamlayici` arayüzü de aynı imzayı alır.

- [ ] **Step 1: Testleri yaz**

```go
func TestAMRSifreyleGiristePwd(t *testing.T) {
	// TamamlandiIsaretle(..., []string{amrSifre}) sonrasi
	// GetAMR() == ["pwd"]
}

// K6: Google ile giren kullanicinin tokeninda "pwd" YAZMAMALI.
func TestAMRFederasyondaPwdDegil(t *testing.T) {
	// TamamlandiIsaretle(..., []string{amrFederasyon}) sonrasi
	// GetAMR() == ["fed"] ve "pwd" ICERMEMELI
}

// Varsayilan sessizce "pwd" OLMAMALI: amr vermeyi unutan bir cagiran
// yanlis bir beyan uretmemeli, bos uretmeli.
func TestAMRVarsayilanPwdDegil(t *testing.T) {
	istek := &AuthIstek{}
	if len(istek.GetAMR()) != 0 {
		t.Errorf("GetAMR() = %v, beklenen bos", istek.GetAMR())
	}
}

// amr Postgres'teki refresh_tokens.amr kolonuna da dogru dusmeli:
// yenileme sonrasi token'da hala dogru amr olmali.
func TestAMRRefreshYenilemesindeKorunur(t *testing.T) {}
```

- [ ] **Step 2: Testleri koş, düşmesini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run TestAMR -count=1`
Expected: FAIL — `AuthIstek` içinde `AMR` alanı yok; `TestAMRVarsayilanPwdDegil` sabit `["pwd"]` yüzünden düşer.

- [ ] **Step 3: Değişikliği uygula**

```go
// GetAMR, kullanicinin NASIL dogrulandigini soyler ve istek basina
// hesaplanir. Eskiden sabit []string{"pwd"} donuyordu; Google ile
// giren bir kullanicinin tokeninda "pwd" yazmasi YANLIS BIR BEYANDIR.
//
// Alan bossa "pwd" varsayilmaz: amr vermeyi unutan bir cagirani
// sessizce yanlis beyana cevirmek, hatayi gizlemenin en pahali
// bicimidir.
func (a *AuthIstek) GetAMR() []string {
	if len(a.AMR) == 0 {
		return []string{}
	}
	return a.AMR
}
```

`TamamlandiIsaretle` `amr` parametresi alır ve kaydeder. Mevcut çağıranlar (`sayfa.go`'daki `girisPost` ve `kayitPost`) `[]string{amrSifre}` geçer; federasyon ve bağlama yolları `[]string{amrFederasyon}` geçer.

**Dikkat:** `TamamlandiIsaretle`'nin "aynı kullanıcı için ikinci çağrı NO-OP" davranışı korunur; `amr` de o durumda **ezilmez** (ilk gerçek giriş yöntemi doğru olandır).

- [ ] **Step 4: Testleri koş**

Run: `cd core-service && go test ./internal/... -count=1`
Expected: tüm paketler PASS. Derleme hataları `IstekTamamlayici` uygulayıcılarını da zorlayacak — hepsi güncellenir.

- [ ] **Step 5: Mutasyonla kanıtla**

`GetAMR`'yi eski sabit haline döndür → `TestAMRFederasyondaPwdDegil` ve `TestAMRVarsayilanPwdDegil` düşmeli. Geri al, ledger'a yaz.

- [ ] **Step 6: Commit**

```bash
git add core-service/internal/kimlik/
git commit -m "fix(kimlik): amr claim'ini istek basina hesapla"
```

---

### Task 8: Kablolama — butonlar, mux, hız limiti, doküman

**Files:**
- Modify: `core-service/internal/kimlik/sunucu.go` (`Start`, `muxKur`)
- Modify: `core-service/internal/kimlik/sayfa.go` (`girisVeri`)
- Modify: `core-service/internal/kimlik/sablonlar/giris.html`
- Modify: `core-service/internal/kimlik/hizlimit.go`
- Modify: `CLAUDE.md`
- Test: `core-service/internal/kimlik/sunucu_test.go`, `sayfa_test.go`

**Interfaces:**
- Consumes: Görev 1-7'nin tamamı.
- Produces: `muxKur(saglayici, kullaniciDepo, istekDepo, federeDepo, akislar, dogrulayicilar, cerezGuvenli, hizLimit)` — imza genişler.

- [ ] **Step 1: Testleri yaz**

```go
// Butonlar YALNIZCA yapilandirilmis saglayicilar icin cikar: olmayan
// bir saglayicinin butonuna basan kullanici hata goruyor.
func TestGirisSayfasiYalnizcaYapilandirilmisSaglayicilariGosterir(t *testing.T) {
	// akislar = {google} -> govdede "google" GECER, "apple" GECMEZ
}

// Buton baglantisi authRequestID TASIMALI: tasimazsa donuste
// tamamlanacak istek bulunamaz.
func TestGirisSayfasiButonuAuthRequestIDTasir(t *testing.T) {}

// Federasyon hic yapilandirilmamissa hicbir buton ve hicbir uc
// OLMAMALI (404), ve giris sayfasi bozulmadan calismali.
func TestFederasyonsuzKurulumUclariBaglamaz(t *testing.T) {}

// Hiz limiti: /federasyon/*/baslat ucu IP basina sayilmali ve
// giris/kayit sayaclarindan AYRI anahtarda olmali (aksi halde
// federasyon denemeleri sifre deneme butcesini tuketir).
func TestFederasyonBaslatHizLimitiAyriSayilir(t *testing.T) {}

// Faz 1'in fail-closed kurali: hiz limiti kurulamazsa Start HIC
// basarili olmamali.
func TestHizLimitiKurulamazsaStartBasarisiz(t *testing.T) {}

// Yarim yapilandirilmis Apple (ekip/anahtar eksik) ile Start
// BASLAMAMALI -- sessiz sifir sinifi.
func TestEksikAppleYapilandirmasiStartiDurdurur(t *testing.T) {}
```

- [ ] **Step 2: Testleri koş, düşmesini gör**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestGirisSayfasiYalnizca|TestGirisSayfasiButonu|TestFederasyonsuz|TestFederasyonBaslat|TestHizLimitiKurulamazsa|TestEksikApple" -count=1`
Expected: FAIL.

- [ ] **Step 3: `girisVeri`'yi ve şablonu genişlet**

```go
type girisVeri struct {
	// ... mevcut alanlar
	// Saglayicilar, giris sayfasinda buton gosterilecek YUKARI AKIS
	// saglayicilaridir. Bos ise hic buton cikmaz.
	Saglayicilar []SaglayiciButon
}

type SaglayiciButon struct {
	Ad      string // "google" | "apple"
	Etiket  string // "Google ile giris yap"
	Yol     string // /federasyon/google/baslat?authRequestID=...
}
```

`giris.html`'e, formun **altına**:

```html
  {{if .Saglayicilar}}
  <div class="federasyon">
    <p class="ayirici">veya</p>
    {{range .Saglayicilar}}
    <a class="fed-buton" href="{{.Yol}}">{{.Etiket}}</a>
    {{end}}
  </div>
  {{end}}
```

`Yol` Go tarafında `url.Values` ile kurulur (şablonda elle string birleştirme yapılmaz; `authRequestID` kaçışsız girerse bağlantı bozulur).

- [ ] **Step 4: `muxKur`'u genişlet**

Yol öncelikleri Faz 1'in kuralını izler (Go 1.22+ `ServeMux` spesiflik kuralı): `"/federasyon/{saglayici}/baslat"`, `"/federasyon/{saglayici}/geri"`, `"/baglama"` tam yol desenleridir ve `"/"` subtree desenini yener. `"/"` **en sona** bağlı kalır.

POST handler'ları `op.NewIssuerInterceptor` ile sarılır (Faz 1'de sarmadan bağlamak akışı yönlendirme aşamasında kırıyordu).

`Start`, her yapılandırılmış sağlayıcı için bir `auth.JWKSVerifier` kurar ve **ctx'i sunucunun ömrüne bağlar** (K7). Sağlayıcı sabitleri koddan gelir:

```go
// Yukari akis adresleri SABITTIR ve koddan gelir; ortamdan yalnizca
// kimlik bilgileri okunur. Her giriste discovery cekmek gereksiz bir
// ag turu ve fazladan bir hata kaynagidir.
var yukariAkisSabitleri = map[string]YukariAkis{
	saglayiciGoogle: {
		Ad: saglayiciGoogle,
		Issuer:    "https://accounts.google.com",
		AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:  "https://oauth2.googleapis.com/token",
		JWKSURL:   "https://www.googleapis.com/oauth2/v3/certs",
		Kapsamlar: []string{"openid", "email", "profile"},
		PKCEDestekli: true,
		FormPost:     false,
	},
	saglayiciApple: {
		Ad: saglayiciApple,
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
```

- [ ] **Step 5: Hız limitini ekle**

`hizlimit.go`'ya `FederasyonSar(next http.Handler) http.Handler` — `SayfaGetSar` deseniyle, **ayrı sayaç adı** (`"fed"`). Gerekçe kodda: federasyon denemelerinin şifre deneme bütçesini tüketmesi, meşru kullanıcının kendi akışını kilitlemesine yol açar.

- [ ] **Step 6: Testleri koş**

Run: `cd core-service && go test ./internal/... -count=1 && go vet ./... && "$(go env GOPATH)/bin/golangci-lint" run ./...`
Expected: tüm testler PASS, vet temiz, lint `0 issues`.

- [ ] **Step 7: `CLAUDE.md`'yi güncelle**

`### Auth` bölümüne, mevcut metni bozmadan:

```markdown
**Federation (Phase 2):** The hosted login page offers Apple and Google.
The provider acts as an RP: the upstream `id_token` is verified against the
provider's JWKS (`internal/auth/jwks_verifier.go`, RS256 pinned, `nonce`
required) and the user is located by `(provider, sub)` in the
`federe_kimlikler` table. Email is never used to link accounts
automatically — on conflict the user is asked for the existing account's
password once (`/baglama`), or told to use the original provider (409) when
that account has no password. Apple private relay addresses are never
trusted for linking. The federation callback uses a dedicated
`SameSite=None` cookie because Apple returns via a cross-site `form_post`.
Mobile carries no Apple/Google SDK and is unaware of this flow.
```

`### Key layers` tablosundaki `middleware/` satırı değişmez; `auth/` için bir satır eklenir.

- [ ] **Step 8: Commit**

```bash
git add core-service/internal/kimlik/ CLAUDE.md
git commit -m "feat(kimlik): federasyonu giris sayfasina ve mux'a bagla"
```

---

### Task 9: Uçtan uca akış ve saldırı testleri

Faz 1'in en pahalı dersi buradaydı: görev başına incelemeler, **katmanlarımızla kütüphanenin çağrı sırası arasındaki uyuşmazlığı yapısal olarak yakalayamaz**. Refresh ailesi iptalini kaçıran Critical tam oradaydı. Bu görev, akışı baştan sona gerçek bir dinleyici üzerinde koşturur.

**Files:**
- Create: `core-service/internal/kimlik/federe_akis_test.go`
- Create: `core-service/internal/kimlik/sahte_yukari_akis_test.go` (test yardımcısı)

**Interfaces:**
- Consumes: `muxKur` (Task 8), tüm görevlerin çıktıları.
- Produces:
  - `func sahteYukariAkisOP(t *testing.T, ayar sahteOPAyar) *sahteOP` — kendi RSA anahtarı, kendi JWKS ucu, `/authorize` ve `/token` uçları olan bir `httptest` sunucusu. `ayar` ile her alanı bozabilir.
  - ```go
    // sahteOPAyar, yukari akis saglayicisinin her yanitini BOZABILMEK
    // icin vardir: saldiri testleri tam olarak bir alani degistirip
    // geri kalanini dogru tutmak zorunda, yoksa testin neyi olctugu
    // belirsizlesir.
    type sahteOPAyar struct {
        Eposta           string
        EpostaDogrulandi bool
        // EpostaDogrulandiString bos degilse email_verified STRING
        // olarak gonderilir (Apple'in K8'deki davranisi).
        EpostaDogrulandiString string
        Ad                     string
        Sub                    string
        // Asagidakiler BOZMA anahtarlaridir; bos/false ise dogru deger
        // kullanilir.
        YanlisNonce     string
        YanlisAudience  string
        YanlisIssuer    string
        YanlisImzaAnahtari bool
        TokenDurumKodu  int    // 0 ise 200
        TokenGovdesi    string // bos ise normal {"id_token":...}
        FormPost        bool   // Apple kipi: donuste POST
    }
    ```
  - `func akisOrtamiKur(t *testing.T, ayar sahteOPAyar) *akisOrtami` — sahte yukarı akışı, gerçek bir `muxKur` dinleyicisini, Redis'i ve (varsa) Postgres'i birlikte kurar.
  - `akisOrtami` üzerinde: `KayitOl(t, eposta, ad, sifre)`, `SifreyleGiris(t, eposta, sifre) string` (access token, başarısızsa `""`), `FedereEslesmeSayisi(t) int`, `YeniTarayici(t) *tarayici`.
  - `tarayici` üzerinde (her biri kendi **çerez kavanozuyla**): `AuthorizeBaslat(t) string` (authRequestID döner), `FederasyonAkisiniKostur(t, saglayici, istekID) *yanitKaydi`, `IstekTamamlandiMi(t, istekID) bool`, `KodDegis(t, istekID) (string, error)`, `BaglamaGonder(t, govde, sifre) *yanitKaydi`.
  - `yanitKaydi` üzerinde: `Durum int`, `Govde() string`, `Konum() string`, `Cerezler() []*http.Cookie`.

Çerez kavanozu zorunludur: `SameSite` ve `Path` davranışı akışın parçasıdır (K2) ve elle kurulan `http.Request`'lerle ölçülemez. `tarayici`, `http.Client{Jar: ...}` kullanır ve yönlendirmeleri **takip etmez** (`CheckRedirect` ile durur) — ara adımların durum kodları ve `Location` başlıkları testin ölçtüğü şeydir.

- [ ] **Step 1: Sahte yukarı akış OP'sini yaz**

Gerçek bir tarayıcı akışını taklit eder: `/authorize`'a gelen isteği kaydeder, verilen `redirect_uri`'ye `code` ve `state` ile döner (Apple kipinde `form_post` ile POST eder), `/token`'da `code`'u `id_token`'a çevirir.

- [ ] **Step 2: Mutlu yol testini yaz**

```go
// Ucan uca: /authorize -> /giris -> Google butonu ->
// /federasyon/google/baslat -> sahte Google -> /federasyon/google/geri
// -> /authorize/callback -> kod -> /oauth/token -> access + id + refresh
//
// Gercek bir net/http dinleyicisi ve CEREZ KAVANOZU ile kosar; cerez
// davranisi (Path, SameSite) akisin parcasidir ve elle kurulan
// isteklerle olculemez.
func TestUctanUcaGoogleGirisi(t *testing.T) {
	// Sonda: access_token RS256, aud = AUTH_CLIENT_ID, amr = ["fed"],
	//        id_token'da email ve sub dolu,
	//        refresh_token donmus ve bir kez donebilir.
}

func TestUctanUcaAppleGirisiFormPost(t *testing.T) {
	// Ayni akis, ama donus CROSS-SITE POST.
	// Bu test K2'nin canli kanitidir: SameSite=Lax bir cerezle
	// GECMEZ, SameSite=None ile GECER.
}

// Ikinci giris AYNI hesaba dusmeli: (saglayici, sub) eslesmesi kalici.
func TestUctanUcaIkinciGirisAyniHesap(t *testing.T) {}

// Apple ikinci giriste email claim'i GONDERMEZ. Bu, hesabi
// kaybetmeye yol acmamali -- eslesme sub uzerinden.
func TestUctanUcaAppleIkinciGiristeEpostaGelmez(t *testing.T) {}
```

- [ ] **Step 3: Saldırı testlerini yaz**

```go
// Saldirgan kendi Google yetkilendirmesinden gecerli bir state+code
// cifti alir ve kurbanin tarayicisinda callback'i acar. Kurbanin
// akisi saldirganin hesabina TAMAMLANMAMALI.
func TestSaldiriBaskaTarayicidaBaslatilanAkisTamamlanamaz(t *testing.T) {}

// Saldirgan kurbanin state'ini ele gecirmis olsa bile, cerez
// olmadan tamamlayamamali (K2).
func TestSaldiriCerezsizCallbackReddedilir(t *testing.T) {}

// Ayni callback iki kez oynatilamaz (state GETDEL).
func TestSaldiriCallbackTekrariReddedilir(t *testing.T) {}

// Yukari akis BASKA bir uygulamaya verilmis bir id_token dondururse
// (aud yanlis) giris OLMAMALI.
func TestSaldiriBaskaUygulamaninTokeniyleGirilemez(t *testing.T) {}

// nonce'u baska olan bir id_token enjekte edilemez.
func TestSaldiriNonceEnjeksiyonuReddedilir(t *testing.T) {}

// Spec'in ana saldirisi, UCTAN UCA: saldirgan kurbanin adresiyle
// sifreli hesap acar, kurban Google ile girer. Kurban SALDIRGANIN
// hesabina DUSMEMELI; baglama sayfasi cikmali ve sifre istenmeli.
//
// Bu test, yorumla belirtilen digerlerinin KALIBIDIR: onkosulun
// gercekten kuruldugu once assert edilir, sonra red olculur.
func TestSaldiriEpostayaDayaliHesapDevralmaEngellenir(t *testing.T) {
	ortam := akisOrtamiKur(t, sahteOPAyar{
		Eposta:          "kurban@ornek.com",
		EpostaDogrulandi: true, // email_verified=true OLSA BILE baglanmamali
	})

	// 1) Saldirgan kurbanin adresiyle SIFRELI bir hesap acar.
	const saldirganSifresi = "saldirganin-sifresi-123"
	ortam.KayitOl(t, "kurban@ornek.com", "Saldirgan", saldirganSifresi)

	// POZITIF KONTROL: hesap GERCEKTEN acildi mi? Acilmadiysa 4.
	// adimdaki red hicbir sey olcmez ve test BOS olur.
	if jeton := ortam.SifreyleGiris(t, "kurban@ornek.com", saldirganSifresi); jeton == "" {
		t.Fatal("onkosul kurulamadi: saldirganin hesabi acilmamis")
	}

	// 2) Kurban giris sayfasindan Google'a gider.
	oturum := ortam.YeniTarayici(t)
	istekID := oturum.AuthorizeBaslat(t)
	yanit := oturum.FederasyonAkisiniKostur(t, "google", istekID)

	// 3) Baglama sayfasi cikmali: sifre SORULMALI.
	govde := yanit.Govde()
	if !strings.Contains(govde, `name="beklemeID"`) {
		t.Fatalf("baglama formu cikmadi; durum %d", yanit.Durum)
	}
	if !strings.Contains(govde, `type="password"`) {
		t.Error("baglama sayfasi sifre sormuyor")
	}

	// 4) GIRIS OLMAMALI: istek tamamlanmadigi icin kod uretilmez ve
	//    access_token alinamaz.
	if oturum.IstekTamamlandiMi(t, istekID) {
		t.Fatal("federasyon, sifre sorulmadan istegi tamamladi (hesap devralma)")
	}
	if _, err := oturum.KodDegis(t, istekID); err == nil {
		t.Fatal("kod degisimi basarili oldu; giris yapilmis demektir")
	}

	// 5) Kurban saldirganin sifresini bilmedigi icin akis BURADA durur:
	//    yanlis sifreyle baglama REDDEDILMELI ve eslesme YAZILMAMALI.
	red := oturum.BaglamaGonder(t, govde, "kurbanin-kendi-sifresi-123")
	if red.Durum != http.StatusUnauthorized {
		t.Errorf("yanlis sifreyle baglama durumu = %d, beklenen 401", red.Durum)
	}
	if ortam.FedereEslesmeSayisi(t) != 0 {
		t.Error("yanlis sifreye ragmen federe eslesme yazilmis")
	}
}

// PKCE: federasyon yolu Faz 1'in S256 zorunlulugunu ATLAMAMALI.
// /authorize'a PKCE'siz gelinip federasyon butonuna basilamaz.
func TestSaldiriFederasyonPKCEyiAtlatmaz(t *testing.T) {}
```

- [ ] **Step 4: Testleri koş**

Run: `cd core-service && go test ./internal/kimlik/ -run "TestUctanUca|TestSaldiri" -count=1 -v`
Expected: PASS, **SKIP yok**. Redis zorunlu; `testRedis(t)` atlarsa Redis'i ayağa kaldırıp yeniden koş.

- [ ] **Step 5: `TestSaldiriEpostayaDayaliHesapDevralmaEngellenir`'in boş olmadığını kanıtla**

Faz 1'de iki test, düzeltmeyi test etmediği halde geçmişti (`TestYanlisAudReddedilir` ve 14a'nın saldırı testi). Bu test de aynı tuzağa düşebilir: saldırganın hesabı gerçekten açılmadıysa test hiçbir şey ölçmez.

Bu yüzden testin kendisi önce **pozitif** tarafı doğrular: 1. adımdan sonra saldırganın `/giris` ile kendi şifresiyle girebildiğini assert eder. Hesap gerçekten varsa, 3. adımdaki red anlamlıdır.

Sonra `kullaniciCoz`'un 4. adımını otomatik bağlamaya çevir → bu test **düşmeli**. Düşmezse test boştur ve düzeltilir.

- [ ] **Step 6: CI'da Redis'in federasyon testlerini gerçekten koşturduğunu doğrula**

Faz 1'de hesap devralma testi, bütün PKCE testleri ve discovery testleri CI'da **atlanıyordu** — süit canlı devralmayı hiç sınamamıştı. `.github/workflows/core-service.yml` Redis servisi zaten var (Faz 1'de eklendi); bu adım yalnızca **doğrulamadır**:

```bash
gh run view <run-id> --log | grep -c "^.*--- SKIP"
```
Beklenen: 0 — yeni testlerden hiçbiri CI'da atlanmıyor.

- [ ] **Step 7: Commit**

```bash
git add core-service/internal/kimlik/federe_akis_test.go core-service/internal/kimlik/sahte_yukari_akis_test.go
git commit -m "test(kimlik): federasyon ucan uca ve saldiri testleri"
```

---

### Task 10: Sevkiyat hazırlığı

Kullanıcı kararı: **Apple/Google kimlik bilgileri henüz yok.** Bu görev sırları ve manifestleri hazırlar, canlı doğrulama kimlik bilgileri gelince ayrı bir adımda yapılır.

**Files:**
- Modify: `k3s-gitops/apps/makesinger/makesinger-auth-kimlik.decrypted.yaml` → yeni alanlar, sonra SOPS ile şifrele
- Modify: `k3s-gitops/apps/makesinger/makesinger-backend.yaml` → yeni `AUTH_FED_*` ortam değişkenleri
- Modify: `core-service/.env.example` (Task 3'te yazıldı, burada doğrulanır)

- [ ] **Step 1: Sağlayıcı taraflı kurulum listesini yaz**

Plan dosyasının kendisine değil, ledger'a ve kullanıcıya verilecek liste:

**Google** (console.cloud.google.com → APIs & Services → Credentials → OAuth 2.0 Client ID, tür **Web application**):
- Authorized redirect URI: `https://makesinger-auth.celalettindemir.dev/federasyon/google/geri`
- Çıktı: Client ID + Client secret

**Apple** (developer.apple.com → Certificates, Identifiers & Profiles):
- Bir **Services ID** (örn. `tr.makesinger.giris`), "Sign in with Apple" etkin
- Return URL: `https://makesinger-auth.celalettindemir.dev/federasyon/apple/geri`
- Domain: `makesinger-auth.celalettindemir.dev`
- Bir **Sign in with Apple anahtarı** (.p8 indirilir, **bir kez** indirilebilir)
- Çıktı: Services ID, Team ID, Key ID, .p8 dosyası

- [ ] **Step 2: Sırları hazırla**

`makesinger-auth-kimlik.decrypted.yaml`'a beş alan eklenir (`google_client_id`, `google_client_secret`, `apple_client_id`, `apple_team_id`, `apple_key_id`, `apple_key_p8`). Kimlik bilgileri gelene kadar **alanlar hiç eklenmez** — boş string eklemek, Task 3'ün "client_id boşsa sağlayıcı listeye hiç eklenmez" kuralıyla sessizce uyumlu ama yanıltıcıdır; alan yoksa niyet açıktır.

`.decrypted.yaml` dosyaları `.gitignore`'da (`*.decrypted.yaml`); şifreleme `.githooks/pre-commit` ile zorlanıyor. Commit öncesi:

```bash
cd ~/Documents/k3s-gitops
sops --encrypt apps/makesinger/makesinger-auth-kimlik.decrypted.yaml \
  > apps/makesinger/makesinger-auth-kimlik.sops.yaml
grep -c "ENC\[AES256_GCM" apps/makesinger/makesinger-auth-kimlik.sops.yaml  # > 0 olmali
grep -nE "BEGIN PRIVATE KEY|client_secret: [^E]" apps/makesinger/makesinger-auth-kimlik.sops.yaml  # BOS olmali
```

- [ ] **Step 3: Deployment'a ortam değişkenlerini ekle**

`makesinger-backend.yaml`, mevcut `AUTH_*` bloğunun yanına, hepsi `secretKeyRef` ile (hiçbiri düz metin değil):

```yaml
            - name: AUTH_FED_GOOGLE_CLIENT_ID
              valueFrom:
                secretKeyRef: { name: makesinger-auth-kimlik, key: google_client_id, optional: true }
            - name: AUTH_FED_GOOGLE_CLIENT_SECRET
              valueFrom:
                secretKeyRef: { name: makesinger-auth-kimlik, key: google_client_secret, optional: true }
            - name: AUTH_FED_APPLE_CLIENT_ID
              valueFrom:
                secretKeyRef: { name: makesinger-auth-kimlik, key: apple_client_id, optional: true }
            - name: AUTH_FED_APPLE_TEAM_ID
              valueFrom:
                secretKeyRef: { name: makesinger-auth-kimlik, key: apple_team_id, optional: true }
            - name: AUTH_FED_APPLE_KEY_ID
              valueFrom:
                secretKeyRef: { name: makesinger-auth-kimlik, key: apple_key_id, optional: true }
            - name: AUTH_FED_APPLE_KEY_P8
              valueFrom:
                secretKeyRef: { name: makesinger-auth-kimlik, key: apple_key_p8, optional: true }
```

`optional: true` **bilinçlidir**: anahtarlar henüz yokken pod başlamalı ve federasyon kapalı kalmalı. Faz 1'in fail-closed kuralıyla çelişmiyor — orada OP'nin **kendisi** yapılandırılmamışsa süreç ölüyordu; burada federasyon isteğe bağlı bir eklentidir ve yarım yapılandırma Task 3'te `Start`'ı durduruyor.

- [ ] **Step 4: Manifestleri derle**

```bash
cd ~/Documents/k3s-gitops
kubectl kustomize apps/makesinger > /dev/null && echo "makesinger build OK"
```

- [ ] **Step 5: Kümeye dokunmadan dur**

Bu noktada commit **hazır ama push edilmez**. Push, yeni imaj digest'iyle **aynı commit'te** gider ve kullanıcının onayını bekler. Faz 1'de öğrenilen sıra:

1. `main`'e birleş → CI yeşil
2. `v1.2.0` etiketi → Docker Build & Push (imaj **yalnızca `v*` etiketinde** yayınlanır, main push'unda değil)
3. Build log'undan `containerimage.digest`
4. gitops: digest + `AUTH_FED_*` + sırlar **tek commit**
5. Flux reconcile → pod, log, canlı doğrulama

- [ ] **Step 6: Canlı doğrulama adımlarını ledger'a yaz (kimlik bilgileri gelince koşacak)**

```bash
# Butonlar gorunuyor mu
curl -sS "https://makesinger-auth.celalettindemir.dev/authorize?client_id=makesinger-mobil&response_type=code&scope=openid&redirect_uri=com.makesinger.app%3A%2Foauth2redirect&state=x&code_challenge=<S256>&code_challenge_method=S256" -D- -o /dev/null | grep -i location
# -> /giris?authRequestID=... ; o sayfayi cerezle cek, "federasyon/google/baslat" GECMELI

# Baslatma yukari akisa yonlendiriyor mu ve cerez dogru mu
# -> Set-Cookie: kimlik_fed ... SameSite=None; Secure; HttpOnly; Path=/federasyon
# -> Location: https://accounts.google.com/o/oauth2/v2/auth?...

# Cerezsiz callback reddediliyor mu (K2'nin canli kaniti)
# -> 403
```

- [ ] **Step 7: Commit (yalnızca core-service tarafı)**

```bash
cd ~/Documents/make-singer/make-singer-backend
git add core-service/.env.example
git commit -m "docs(kimlik): federasyon icin gereken saglayici kimlik bilgileri"
```

---

## Plan sonrası: bilinen boşluklar

Bunlar bilinçli olarak Faz 2 dışında bırakılmıştır ve ledger'a da yazılır.

- **Hesap silme** spec'te `### Hesap silme` bölümünde ve App Store 5.1.1(v) gereği zorunlu, ama R2 anahtar düzenine bağlı olduğu için **Faz 3**'tedir. `federe_kimlikler` tablosu `ON DELETE CASCADE` ile baştan hazırdır.
- **Bağlamayı kaldırma** (bir sağlayıcının hesaptan sökülmesi) kapsamda değil. Son giriş yolunu söken bir "kaldır" düğmesi hesabı erişilemez bırakabilir; bu kendi tasarımını hak ediyor.
- **K1'in bedeli** kabul edilmiştir: kullanıcı Google ve Apple'ı tek hesapta birleştiremez. Değişirse yol, "diğer sağlayıcıyla kanıtlat" seçeneğidir.
- **E-posta doğrulama** hâlâ yok; federasyon bunu getirmez. `email_verified` sütunu yukarı akışın beyanını taşır ve **bağlama kararında kullanılmaz**.
- **`/ws/jobs/:jobId` kimlik doğrulamasız** (Faz 1'de not edildi, Faz 3'te WebSocket bileti ile kapanıyor).
- **Handler seviyesinde sahiplik kontrolü yok** (Faz 1'den devreden IDOR, Faz 3).
