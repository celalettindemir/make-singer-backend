# Kredi sistemi: abonelik, hediye kampanyalari, harcama

Tarih: 2026-10-13. Durum: Yerine geçti: make-singer-doc native/specs/2026-10-17-native-6-kredi-design.md.
Figma: "MakeASinger — Aurora Redesign" (r15IOW8FQNn1ChAZVyJEqy), sayfa
"06 Screens · Kredi" (K1–K7 mobil, W1–W2 web admin).

## Amac

Butun AI islemleri kredi harcar. Kullanici krediyi iki yoldan alir:

1. **Abonelik** (Plus, Pro): her donem basinda plan kredisi yuklenir,
   kullanilmayan kisim donem sonunda yanar.
2. **Hediye kampanyasi**: yonetici web admin'den kampanya acar (kisi
   kotasi, kisi basi kredi, kredi gecerliligi, istege bagli bitis).
   Kampanya basladiktan sonra **kayit olan** kullanicilar kota dolana
   kadar krediyi otomatik alir.

Disinda: kredi paketi (tek seferlik satin alma), referans/davet,
mevcut kullanicilara gecis hediyesi (karar: verilmez), acik tema.

## Kararlar

| # | Karar |
|---|---|
| K1 | Planlar: Free (aylik kredi yok), Plus, Pro. Aylik ve yillik urun. |
| K2 | Odeme: dogrudan StoreKit 2 / Play Billing; makbuz dogrulama ve sunucu bildirimleri backend'de. RevenueCat yok. |
| K3 | Abonelik kredisi donem sonunda sifirlanir; hediye kredisi ayri havuzda, kendi son kullanma tarihine kadar kalir. |
| K4 | Harcama sirasi: havuzdan bagimsiz, suresi en erken dolan parti once (esitlikte once olusturulan). Boylece hediye kredisi abonelik varken harcanmadan yanmaz. |
| K5 | Kampanya yalnizca kampanya baslangicindan sonra kayit olan kullaniciya gider. |
| K6 | Ayni anda tek aktif kampanya. Yeni kampanya baslatmak aktif olani durdurur. |
| K7 | Kredi gecerliligi kampanya basina gun sayisi; sure kullanici krediyi aldigi anda baslar. |
| K8 | Basarisiz is krediyi otomatik iade eder; hareketlerde iade satiri gorunur. |
| K9 | Admin: frontend /admin, kendi OP'mize OIDC (PKCE) girisi; backend `ADMIN_USER_IDS` listesine bakar. |
| K10 | Mevcut kullanicilara acilista kredi verilmez. |
| K11 | Defter (ledger) modeli; tek bakiye kolonu yok. |

## Maliyet tablosu

Degerler config'dedir (`CREDIT_COST_*`), deploy'suz degistirilemez ama
kod degisikligi gerektirmez. Istemci degerleri `GET /api/credits`
yanitindan okur; uygulamaya gomulmez.

| Islem | Anahtar | Kredi |
|---|---|---|
| Render · Miks | `render_mix` | 12 |
| Render · Odak | `render_focus` | 32 |
| Render · Kanallar | `render_stems` | 62 |
| Soz uret | `lyrics_generate` | 2 |
| Sozu yeniden yaz | `lyrics_rewrite` | 1 |
| Master onizleme | `master_preview` | 0 |
| Final master | `master_final` | 3 |
| Export, vokal yukleme | — | 0 |

Plan kredisi (config, `PLAN_CREDITS_PLUS=300`, `PLAN_CREDITS_PRO=1000`):
aylik. Yillik urunde de kredi her ay yuklenir (asagida "Yillik plan").

## Mimari

Yeni paket `internal/credit`, sosyal modul kalibinda: `AUTH_DB_URL`
uzerinde kendi pool'u, kendi semasi (`kredi`), kendi migration tablosu
(`kredi.schema_migrations`, gomulu `migrations/*.sql`, yalnizca ekleyici)
ve kendi advisory lock anahtari. `users` tablosuna FK yok (sosyal ile ayni gerekce: kimlik
modulu ayri pool'da).

```
handler (credits, subscriptions, admin, webhooks)
   │
credit.Service ── Reserve / Commit / Release / Grant / Claim / Balance
   │                 │
   │                 └─ UserCreatedAt arayuzu (kimlik modulu saglar)
   ├─ credit.Store (pgx, kredi semasi)
   └─ store.Verifier (Apple JWS, Google Play API) ── subscriptions
```

Mevcut servisler (`RenderService`, `MasterService`, `LyricsService`
handler'lari) yalnizca `credit.Spender` arayuzunu gorur:

```go
type Spender interface {
    Reserve(ctx context.Context, userID uuid.UUID, op Operation, ref string) (Reservation, error)
    Commit(ctx context.Context, ref string) error
    Release(ctx context.Context, ref string, reason string) error
}
```

`CREDITS_ENFORCE=false` iken `Reserve` hareketi kaydeder ama bakiye
yetersizligini engellemez (golge mod). Bkz. "Sevkiyat".

## Veri modeli (`kredi` semasi)

```sql
-- Bir kredi partisi: bir kaynaktan gelen, kalan miktari ve bitisi olan dilim.
lots (
  id            uuid PK,
  user_id       uuid NOT NULL,
  pool          text NOT NULL CHECK (pool IN ('subscription','gift')),
  source        text NOT NULL,           -- 'subscription' | 'campaign' | 'admin'
  source_ref    text,                    -- abonelik donem id'si / kampanya id'si
  amount        int  NOT NULL CHECK (amount > 0),
  remaining     int  NOT NULL CHECK (remaining >= 0),
  expires_at    timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
)
INDEX lots_user_live ON lots (user_id, expires_at) WHERE remaining > 0

-- Degismez hareket defteri; bakiye gecmisi ve "Son hareketler" buradan.
entries (
  id          bigserial PK,
  user_id     uuid NOT NULL,
  kind        text NOT NULL,  -- grant | reserve | commit | release | expire
  amount      int  NOT NULL,  -- isaretli: grant/release +, reserve/expire -
  operation   text,           -- render_mix, lyrics_rewrite, ... (harcamada)
  ref         text,           -- job id / kampanya id / donem id
  label       text,           -- gecmiste gosterilecek baglam (proje adi)
  created_at  timestamptz NOT NULL DEFAULT now()
)
INDEX entries_user_time ON entries (user_id, created_at DESC, id DESC)

-- Asenkron isler icin tutulan kredi; partilerden nereden dusuldugunu bilir.
reservations (
  ref         text PK,        -- job id ya da senkron cagri icin uuid
  user_id     uuid NOT NULL,
  operation   text NOT NULL,
  amount      int  NOT NULL,
  allocation  jsonb NOT NULL, -- [{lot_id, amount}]
  status      text NOT NULL CHECK (status IN ('held','committed','released')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  settled_at  timestamptz
)

campaigns (
  id             uuid PK,
  name           text NOT NULL,
  per_user       int  NOT NULL CHECK (per_user > 0),
  quota          int  NOT NULL CHECK (quota > 0),
  used           int  NOT NULL DEFAULT 0 CHECK (used <= quota),
  validity_days  int  NOT NULL CHECK (validity_days > 0),
  starts_at      timestamptz NOT NULL,
  ends_at        timestamptz,
  stopped_at     timestamptz,
  created_by     uuid NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
)
UNIQUE INDEX campaigns_one_active ON campaigns ((true)) WHERE stopped_at IS NULL
-- (bitmis/dolmus kampanyalar da stopped_at alir; asagida)

campaign_claims (
  campaign_id uuid NOT NULL REFERENCES campaigns(id),
  user_id     uuid NOT NULL,
  lot_id      uuid NOT NULL,
  claimed_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (campaign_id, user_id)
)

subscriptions (
  id                uuid PK,
  user_id           uuid NOT NULL,
  platform          text NOT NULL CHECK (platform IN ('apple','google')),
  store_id          text NOT NULL,   -- Apple originalTransactionId / Google purchaseToken (ilk)
  product_id        text NOT NULL,
  plan              text NOT NULL CHECK (plan IN ('plus','pro')),
  period            text NOT NULL CHECK (period IN ('monthly','yearly')),
  status            text NOT NULL,   -- active | grace | expired | revoked
  period_start      timestamptz NOT NULL,
  period_end        timestamptz NOT NULL,
  next_refill_at    timestamptz,     -- yillik plan icin aylik yukleme
  auto_renew        boolean NOT NULL,
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (platform, store_id)
)
INDEX subscriptions_user ON subscriptions (user_id)
```

Kampanya durumu turetilir, kolon degildir: `stopped_at` dolu ise
"Durduruldu" (ya da `used = quota` ise "Kota doldu", `ends_at < now()`
ise "Suresi bitti"); bos ise "Aktif". Kota dolunca ve sure bitince
`stopped_at` claim/sweeper tarafindan doldurulur, boylece "tek aktif"
indeksi dogru kalir.

Bakiye = `SUM(remaining) FROM lots WHERE user_id = $1 AND expires_at > now()`.
Tutulan (held) kredi partilerden zaten dusulmustur; bakiyede gorunmez.

## Akislar

### Harcama (Reserve → Commit / Release)

`Reserve` tek transaction:

1. `SELECT ... FROM lots WHERE user_id=$1 AND remaining>0 AND expires_at>now()
   ORDER BY expires_at, created_at FOR UPDATE`
   (havuzdan bagimsiz, en erken biten parti once; K4).
2. Toplam < maliyet ise `ErrInsufficient{Required, Balance}` (zorlama
   kapaliysa gecer, ama 0 tutar ayrilir ve hareket `reserve` olarak
   `amount=0` ile kaydedilir; golge modun amaci olcmek).
3. Partilerden sirayla dus, `reservations` satiri (`held`, allocation),
   `entries` (`reserve`, -maliyet).
4. Maliyet 0 olan islemler (master onizleme) `Reserve` cagirmaz.

`Commit(ref)`: `held → committed`, `entries` `commit` (amount 0, kayit
amacli). `Release(ref)`: `held → released`, allocation'daki her partiye
miktari geri ekle (parti suresi dolmus olsa bile ekle; sure dolmus parti
bakiyeye zaten girmez), `entries` `release` (+maliyet). Her ikisi de
idempotent: `held` olmayan rezervasyonda no-op.

Islem baglantilari:

| Islem | Reserve | Commit | Release |
|---|---|---|---|
| Lyrics generate/rewrite (senkron) | handler, Groq cagrisindan once | basarili yanit | Groq/parse hatasi |
| Render start | `StartRender`, enqueue'dan once, ref=jobID | worker basari | worker kalici hatasi (`SkipRetry` ya da son deneme); enqueue hatasi; kuyrukta iken iptal |
| Master final | `StartFinal`, ref=jobID | worker basari | worker kalici hatasi (`SkipRetry` ya da son deneme); enqueue hatasi |

Not: `FailJob` asynq'in tekrar deneyecegi hatalarda da cagrilir; orada iade
edilirse basarili bir tekrar ucretsiz olur. Bu yuzden iade yalnizca kalici
hatada yapilir (Faz A plani R4).

Render iptali: is `queued` iken iptal edilirse `Release`; `processing`
iken iptal edilirse kredi worker'in sonucuna gore kapanir (Suno ucreti
odenmis olabilir). Worker, `canceled` durumundaki bir isi Suno
cagirmadan birakmalidir (plan bunu dogrular, eksikse ekler); bu durumda
`Release`.

Asili rezervasyon: 3 saatten eski `held` rezervasyonlari periyodik
gorev (`kredi:supur`, saatlik) `Release` eder (worker coktu, is kayboldu).
Kullanici lehine hata tercih edilir.

Yetersiz bakiye yaniti (zorlama acikken): HTTP 402, mevcut hata zarfi
`{"error":{"code":"kredi_yetersiz","message":...,"details":{"required":32,"balance":20}}}`.
Mesaj `internal/i18n`'den, Accept-Language'a gore. Kod wire sozlesmesidir,
`tools/wirecheck` snapshot'ina eklenir.

### Kampanya talebi (yeni kayit hediyesi)

Tetik: istemcinin `GET /api/credits` cagrisi (uygulama acilisi / giris
sonrasi). Ayri bir "kayit oldu" olayi yok; kimlik modulune dokunmamak icin
tembel talep.

1. Aktif kampanya yoksa ya da kullanici o kampanyada zaten `claim`
   ettiyse atla.
2. `UserCreatedAt(userID)` (kimlik modulu arayuzu) `< starts_at` ise atla
   (K5).
3. Tek transaction:
   `UPDATE campaigns SET used = used + 1 WHERE id=$1 AND stopped_at IS NULL
   AND used < quota AND (ends_at IS NULL OR ends_at > now()) RETURNING used, quota`
   → satir yoksa atla. `used = quota` olduysa ayni transaction'da
   `stopped_at = now()`.
   `INSERT campaign_claims` (PK cakismasi → rollback, atla),
   `INSERT lots (pool='gift', source='campaign', expires_at = now() + validity_days)`,
   `INSERT entries (grant)`.
4. Yanit `welcomeGift` alanini tasir (K6 ekrani). Istemci gosterdigi
   claim'i yerel olarak `campaignId` ile isaretler; sunucu, claim'den
   sonraki 7 gun boyunca alani dondurmeye devam eder.

Kota asilmaz: sayac kosullu UPDATE ile artar, PK tekrar talebi engeller.

### Abonelik

Istemci:

- iOS: StoreKit 2, `appAccountToken = userID` (UUID) ile satin alir,
  basarili islemin `jwsRepresentation`'ini
  `POST /api/subscriptions/apple` ile gonderir.
- Android: Play Billing, `obfuscatedAccountId = userID` ile satin alir,
  `{productId, purchaseToken}`'i `POST /api/subscriptions/google` ile
  gonderir. Backend dogruladiktan sonra `acknowledge` eder.
- Mobil kutuphane: `react-native-iap` (plan surumu sabitler).

Backend dogrulama:

- Apple: JWS imzasi Apple kok sertifika zinciriyle dogrulanir,
  `bundleId`, `appAccountToken == userID`, `environment` kontrol edilir.
  Sunucu bildirimleri: App Store Server Notifications V2
  `POST /api/public/webhooks/apple` (ayni JWS dogrulamasi).
- Google: Play Developer API `purchases.subscriptionsv2.get` (servis
  hesabi), `obfuscatedExternalAccountId == userID`. Bildirimler: RTDN,
  Pub/Sub push → `POST /api/public/webhooks/google`; Pub/Sub'in OIDC
  token'i (audience + servis hesabi e-postasi) dogrulanir, sonra
  abonelik API'den tekrar okunur (bildirimin icerigine guvenilmez).

Donem olaylari (`ApplyPeriod(sub)`, idempotent; anahtar
`source_ref = <subscription id>:<period_start>`):

| Olay | Etki |
|---|---|
| Yeni abonelik / yenileme | Kullanicinin eski `subscription` partilerini `expire` et, yeni parti: plan kredisi, `expires_at = min(period_end, period_start + 1 ay)` |
| Yukseltme (Plus → Pro, aninda) | Ayni: eski parti yanar, Pro partisi yuklenir |
| Dusurme | Bir sonraki yenilemede yeni plan |
| Grace / billing retry | Parti dokunulmaz, durum `grace` |
| Sure doldu / iptal edildi ve donem bitti | `status=expired`; parti zaten kendi suresiyle biter |
| Iade / revoke | `status=revoked`, kalan abonelik partisi `expire` edilir |

Yillik plan: `next_refill_at` aylik ilerler; `kredi:yenile` periyodik
gorevi (saatlik; mevcut `asynq.Scheduler`, `bakim` kuyrugu, `Unique`) vadesi gelen yillik abonelikleri bulur, eski partiyi
yakar, yenisini yukler.

Ayni kullanicinin birden fazla aktif aboneligi (iki platform): en yuksek
plan gecerli, kredi yalnizca o abonelik icin yuklenir.

`POST /api/subscriptions/restore` yok: "Geri yukle" istemcide StoreKit /
Play'den islemleri alip ayni dogrulama endpoint'lerine gonderir.

### Suresi dolan partiler

`kredi:supur` gorevi `expires_at <= now() AND remaining > 0` partileri
icin `entries (expire, -remaining)` yazar ve `remaining = 0` yapar
(bakiye hesabi zaten bitis tarihine bakar; bu adim gecmisi dogru tutar).
Ayni gorev bitis tarihi gecmis ya da kotasi dolmus kampanyalara
`stopped_at` yazar.

### Hesap silme

`AccountService.Delete`, `SocialCleaner` gibi istege bagli bir
`CreditCleaner` cagirir (hatasi loglanir, akisi durdurmaz) ve
`kredi` semasindaki kullanici satirlarini siler
(lots, entries, reservations, campaign_claims; subscriptions satiri
`user_id` bosaltilarak degil silinerek). Kampanya `used` sayaci
geri alinmaz. Silinmis kullanici isareti (`deletionmark`) zaten API'yi
kapatir.

## API

Tumu `/api` altinda, kimlik dogrulamali (mevcut middleware). Webhook'lar
`/api/public/webhooks/*` (kimliksiz, kendi imza dogrulamasiyla); mevcut
IngressRoute `/api/` beyaz listesi kapsar.

```
GET  /api/credits
200 {
  "balance": 84,
  "pools": {
    "subscription": {"amount": 60, "plan": "plus", "renewsAt": "2026-11-01T00:00:00Z"} | null,
    "gift": {"amount": 24, "nextExpiry": "2026-12-31T23:59:59Z"} | null
  },
  "plan": "free" | "plus" | "pro",
  "costs": {"render_mix":12,"render_focus":32,"render_stems":62,
            "lyrics_generate":2,"lyrics_rewrite":1,"master_preview":0,"master_final":3},
  "enforced": true,
  "welcomeGift": {"campaignId":"...","amount":50,"expiresAt":"..."} | null
}

GET  /api/credits/history?cursor=&limit=20
200 {"items":[{"id":..,"kind":"reserve|release|grant|expire","amount":-32,
               "operation":"render_focus","label":"Gece Yarisi Demo",
               "createdAt":".."}], "nextCursor": ".." | null}
     (commit satirlari listelenmez; reserve harcama olarak gorunur)

POST /api/subscriptions/apple   {"signedTransaction": "<JWS>"}
POST /api/subscriptions/google  {"productId": "...", "purchaseToken": "..."}
200  GET /api/credits govdesi (istemci tek istekle tazelenir)
409  {"code":"abonelik_baska_hesapta"}  -- islem baska userID'ye bagli

POST /api/public/webhooks/apple   (App Store Server Notifications V2)
POST /api/public/webhooks/google  (Pub/Sub push)

GET  /api/admin/campaigns                -> liste + ozet (W1)
POST /api/admin/campaigns {name, quota, perUser, validityDays, endsAt?}
                                         -> aktif olani durdurur, yenisini baslatir (K6)
POST /api/admin/campaigns/:id/stop
```

Yol ve JSON anahtarlari render/lyrics gibi Ingilizce; hata kodlari sosyal
modul gibi kucuk harf Turkce. Hepsi wire sozlesmesidir ve
`tools/wirecheck` snapshot'ina girer. Hatalar mevcut zarfla
(`{"error":{"code","message","details"}}`) doner.

Hata kodlari (wire): `kredi_yetersiz` (402), `abonelik_gecersiz` (422),
`abonelik_baska_hesapta` (409), `yetkisiz` (403, admin degil).

Rate limit: `/api/credits` 60/dk, `/api/subscriptions/*` 10/dk,
`/api/admin/*` 30/dk; webhook'lar limitsiz ama imza dogrulamasi once.

Admin kapisi: `AdminGate` middleware, token'daki `sub` `ADMIN_USER_IDS`
(virgulle ayrilmis UUID) icinde degilse 403. Liste bos ise admin
endpoint'leri tamamen kapali.

## Mobil (make-singer-mobile)

Ekranlar Figma K1–K7:

- **K1 CreditChip**: Studio basliginda bakiye; dokununca K2.
- **K2 Kredi paneli** (Sheet): bakiye, havuzlar, son 3 hareket ("Tumu"
  ayri liste), CTA: Free/Plus → "Plani yukselt" (K3), Pro → "Aboneligi
  yonet" (magaza abonelik sayfasi).
- **K3 Paywall**: aylik/yillik segment, Ucretsiz/Plus/Pro kartlari
  (mevcut plan "Mevcut"), fiyatlar magazadan (yerellestirilmis), "Geri
  yukle", kosullar/gizlilik.
- **K4 maliyet etiketleri**: Render kalitesi segmentinde ve Island
  "Uret · N kredi"; soz uret/yeniden yaz ve final master butonlarinda.
  Degerler `costs`'tan.
- **K5 Yetersiz kredi** (Sheet): 402 yanitindan `required`/`balance`;
  "Planlari gor" ve render icin daha ucuz katman varsa "Miks ile devam
  et · 12 kredi". Ucuz katman bakiyeyi de asiyorsa yalnizca plan CTA.
- **K6 Hos geldin hediyesi** (Sheet): `welcomeGift` dolu ve yerelde
  gosterilmemisse bir kez.
- **K7 Profil**: plan karti (K2'yi acar) ve "Aboneligi yonet" satiri.

Veri: `useCredits` (react-query, `GET /api/credits`; harcama yapan her
mutasyondan ve WS is bitis olayindan sonra invalidate). 402'ler apiError
katmaninda tiplenir (`InsufficientCreditsError`), ekranlar K5'i acar.
`enforced=false` iken etiketler ve bakiye gosterilir, K5 hic acilmaz.

## Web admin (make-singer-frontend)

- `/admin` (W1) ve "Yeni kampanya" cekmecesi (W2), Figma'daki gibi.
- Giris: kendi OP'mize OIDC authorization code + PKCE; OP'ye yeni
  istemci `makesinger-admin-web` (redirect `https://<site>/admin/callback`).
  Token sunucu tarafi oturumda (httpOnly cookie) tutulur; backend'e
  Next.js sunucusu `Authorization: Bearer` ile gider. Admin olmayan
  kullanici 403 sayfasi gorur.
- W2 ozeti istemcide hesaplanir: toplam = kota x kisi basi; aktif
  kampanya varsa "Yerine gececek" satiri.

## Hata durumlari

- Reserve ile enqueue arasinda cokme: rezervasyon `held` kalir, saatlik
  supurme 3 saat sonra iade eder.
- Worker basarili ama `Commit` hatasi: worker hatayi loglar, tekrar dener
  (idempotent); kalici hata durumunda rezervasyon `held` kalir ve supurme
  iade eder (kullanici lehine).
- Ayni webhook iki kez: `ApplyPeriod` anahtari ayni donemi bir kez yukler.
- Istemci dogrulama ve webhook yarisi: ikisi de ayni `ApplyPeriod`'a
  gider; `subscriptions` `UNIQUE (platform, store_id)` + donem anahtari.
- Saat farki: tum zamanlar sunucu `now()`; istemci yalnizca gosterir.

## Test

- `credit` paketi: Reserve sira/oncelik (en erken biten parti once, havuzdan bagimsiz),
  yetersiz bakiye, golge mod, Release'in partilere geri yazmasi,
  idempotent Commit/Release, eszamanli iki Reserve (FOR UPDATE), kampanya
  kotasi yarisi (N paralel claim, `used <= quota`), K5 tarih kontrolu,
  tek aktif kampanya, supurme (expire + asili rezervasyon), ApplyPeriod
  idempotensi, yukseltme/iade. Canli PG testleri mevcut `faz2-pg` ile
  (ortam degiskeni yoksa SKIP — bkz. kimlik/sosyal testleri).
- Store dogrulama: Apple JWS icin test sertifika zinciriyle imzali
  fixture'lar; Google icin sahte HTTP sunucusu. Gercek magaza cagrisi yok.
- Handler'lar: 402 govdesi, admin 403, wirecheck snapshot.
- Worker entegrasyonu: render/master basari → commit, hata → release,
  kuyrukta iptal → release.
- Mobil: useCredits, 402 → K5, maliyet etiketleri, welcomeGift bir kez,
  paywall durumlari (react-native-iap mock).
- Frontend: admin listesi/formu, 403 sayfasi.

## Sevkiyat

Odeme (K3) gelmeden zorlamayi acmak, abonelik alamayan herkesi kilitler
(mevcut kullanicilara hediye yok, K10). Bu yuzden:

1. **Faz A — Backend defter + kampanya + admin API**, `CREDITS_ENFORCE=false`
   (golge mod: harcamalar kaydedilir, engel yok). Kampanyalar calisir,
   bakiye birikir.
2. **Faz B — Mobil kredi arayuzu (K1, K2, K4–K7) + web admin (W1, W2)**.
   Paywall'in satin alma butonu bu fazda gizli.
3. **Faz C — Abonelik**: magaza urunleri, dogrulama, webhook'lar, K3
   paywall. On kosul (kullanicinin isi): App Store Connect'te abonelik
   grubu + 4 urun (`makesinger.plus.monthly`, `makesinger.plus.yearly`,
   `makesinger.pro.monthly`, `makesinger.pro.yearly`), App Store Server
   API anahtari, bildirim URL'i; Play Console'da ayni urunler, servis
   hesabi, RTDN Pub/Sub konusu. iOS Release bundle id sorunu
   (`org.reactjs.native.example`) bu fazdan once cozulmeli.
4. **Zorlama**: Faz C'nin mobil surumu magazada yayindayken
   `CREDITS_ENFORCE=true`. Eski uygulama surumleri 402'yi genel hata
   olarak gosterir; zorlama oncesi minimum surum kontrolu planda ele
   alinir.

Her faz ayri plan dosyasi ve ayri onaydir.
