# Keşfet (sosyal) — Tasarım ve API sözleşmesi

**Tarih:** 2026-10-11
**Durum:** Onaylandı (brainstorming, 2026-10-11)
**Kaynak tasarım:** `docs/superpowers/specs/2026-10-10-aurora-redesign-design.md` §5.4 (ekran 16–20) ve Keşfet durum satırları. Figma: `03b Screens · Keşfet`.
**Kapsam:** Mobil (make-singer-mobile), backend (make-singer-backend/core-service) ve link sayfası (make-singer-frontend).

Bu belge iki tarafın ortak sözleşmesidir. Backend bu sözleşmeye göre Go'da ayrı oturumda yapılır. Mobil aynı sözleşmeye karşı geliştirilir ve testlerde sahte servis kullanır. Bu dosyanın bir kopyası `make-singer-backend/docs/superpowers/mobil-gorevleri/2026-10-11-kesfet-sozlesme.md` yolundadır. İki kopya arasında fark çıkarsa mobildeki kopya esastır.

## 1. Amaç ve kararlar

Kullanıcılar şarkılarını yayınlar, beğenir, zaman damgalı yorum yapar ve takip eder. Ayrıca başkasının altyapısında kendi vokalini kaydedebilir ("Bu altyapıda söyle").

Brainstorming kararları:

| # | Karar |
|---|---|
| K1 | Sözleşme önce yazılır. Backend ayrı oturumda yapılır, mobil sahte servisle geliştirilir. |
| K2 | Sözleşme Keşfet'in tamamını kapsar. Uygulama 5a → 5d sırasıyla aşamalıdır (§9). |
| K3 | Kullanıcı adı ilk "Yayınla"da seçilir. Görünen addan önerilir ve düzenlenebilir. Profil'den sonradan değiştirilebilir. |
| K4 | Görünürlük üç seçeneklidir: `herkes`, `takipciler`, `link`. "Sadece link" için web sayfası ve deep link yapılır (§7). |
| K5 | Mimari: core-service içinde REST ve Postgres. Ayrı mikroservis veya hazır servis kullanılmaz. |
| K6 | Yayın, dosyaların kendi kopyasını tutar (`sosyal/{yayinId}/…`). Proje sonradan değişse de yayın bozulmaz. |
| K7 | Cover başlatılınca altyapı stem'leri kullanıcının kendi R2 önekine kopyalanır. Cover projesi bundan sonra normal bir proje gibi çalışır. |
| K8 | "Senin için" v1 kişiselleştirilmemiştir. Basit bir popülerlik puanıyla sıralanır (§5.1). |
| K9 | Avatar fotoğrafı yoktur. Avatar rengi kullanıcı kimliğinden sabit bir paletten türetilir. |
| K10 | Şikayet yalnızca kaydedilir. Moderasyon, bildirimler, DM ve meydan okumalar kapsam dışıdır. |

## 2. Mevcut durum (2026-10-11)

- Mobil projeler yalnızca cihazda durur (MMKV `projects`). Sunucuda proje kaydı yoktur.
- Render stem'leri, master ve vokal take'leri R2'de kullanıcının öneki altında dosya olarak durur. `POST /api/files/sign` yalnızca dosyanın sahibine imza verir (`client.SahipliAnahtarCoz`).
- core-service'te Postgres (pgx v5) yalnızca kimlik modülünde kullanılır. Sosyal tablolar yeni bir `sosyal` şemasında olur.
- Prod API Faz 4'e kadar dışarıya kapalıdır. Keşfet canlıda ancak API açılınca çalışır.
- Mobil linking önekleri: `https://makesinger.celalettindemir.dev`, `com.makesinger.app://`.

## 3. Veri modeli (Postgres, şema `sosyal`)

Zaman alanları `timestamptz`, kimlikler `uuid`'dir. `kullanici_id`, kimlik modülündeki kullanıcı kimliğidir.

**profil**

| Alan | Tür | Not |
|---|---|---|
| kullanici_id | uuid PK | |
| kullanici_adi | text UNIQUE | Küçük harf, 3–20 karakter, `^[a-z0-9_.]+$`, nokta ile başlayamaz ve bitemez |
| gorunen_ad | text | 1–40 karakter |
| bio | text | En çok 160 karakter, boş olabilir |
| olusturma | timestamptz | |

Avatar rengi saklanmaz. `avatarRengi = fnv32(kullanici_id) % 8`, paletteki sırayı verir (0–7).

**yayin**

| Alan | Tür | Not |
|---|---|---|
| id | uuid PK | |
| slug | text UNIQUE | 8 karakter, `[A-Za-z0-9]` |
| sahip_id | uuid | profil.kullanici_id |
| baslik | text | 1–80 karakter |
| aciklama | text | En çok 300 karakter |
| tur | text | Brief türü (`pop`, `rock`…) |
| bpm | int | |
| ton_tonik, ton_mod | text | Örnek: `A`, `minor` |
| sure_sn | int | |
| gorunurluk | text | `herkes` \| `takipciler` \| `link` |
| altyapi_acik | bool | Cover yayınında her zaman `false` |
| yorum_acik | bool | |
| sozler | jsonb | §4.3 `Sozler` |
| yapi | jsonb | `[{ id, tur, olcu }]` |
| dalga_formu | jsonb | 64 adet 0–1 arası sayı. Hazırlanamazsa boş dizi |
| stem_listesi | jsonb | `[{ id, enstruman, sure }]`. Altyapı açıksa dolu |
| kaynak_yayin_id | uuid NULL | Cover ise orijinal yayın |
| durum | text | `hazirlaniyor` \| `yayinda` \| `hata` |
| olusturma, silinme | timestamptz | `silinme` NULL değilse yayın kaldırılmıştır |

**begeni** (`kullanici_id`, `yayin_id`, `olusturma`), PK (kullanici_id, yayin_id).

**takip** (`takipci_id`, `takip_edilen_id`, `olusturma`), PK ikisi birlikte. `CHECK takipci_id <> takip_edilen_id`.

**yorum**

| Alan | Tür | Not |
|---|---|---|
| id | uuid PK | |
| yayin_id | uuid | |
| yazar_id | uuid | |
| metin | text | 1–500 karakter |
| zaman_ms | int NULL | Şarkıdaki an; 0 ≤ zaman_ms ≤ sure_sn × 1000 |
| olusturma, silinme | timestamptz | |

**sikayet** (`id`, `bildiren_id`, `hedef_turu` = `yayin` \| `yorum`, `hedef_id`, `neden` = `spam` \| `taciz` \| `telif` \| `uygunsuz` \| `diger`, `aciklama` en çok 300 karakter, `olusturma`), UNIQUE (bildiren_id, hedef_turu, hedef_id).

### 3.1 Dosyalar (R2, özel bucket)

| Anahtar | İçerik | Ne zaman |
|---|---|---|
| `sosyal/{yayinId}/master.{uzanti}` | Master kopyası | Her yayında |
| `sosyal/{yayinId}/onizleme.mp3` | Masterın 30 sn'lik önizlemesi (en yüksek RMS'li 30 sn penceresi) | `herkes` ve `link` yayınlarında |
| `sosyal/{yayinId}/stems/{stemId}.{uzanti}` | Stem kopyaları | `altyapi_acik` ise |

Bu dosyalar herkese açık değildir. İmzalı adresler yalnızca §4'teki uçlardan, görünürlük kuralı uygulanarak verilir.

## 4. API sözleşmesi

- Temel yol `/api/social`. Tüm uçlar `Authorization: Bearer` ister; tek istisna §4.8.
- JSON alan adları camelCase'tir. Zamanlar ISO 8601'dir.
- Hata gövdesi, mevcut `pkg/response` biçimidir: `{ "error": { "code": "…", "message": "…" } }`.
  - Beklenmeyen sunucu hatası 500 `sunucu_hatasi` döner; ayrıntı yalnızca sunucu logundadır.
  - Gövde çözülemezse ya da alan kuralı bozulursa 422 `dogrulama` döner (400 kullanılmaz).
- Liste uçları sayfalıdır.
  - İstek: `?imlec=<opak dize>&limit=<1–50, varsayılan 20>`.
  - Yanıt: `{ "ogeler": [...], "sonrakiImlec": "…" | null }`.

### 4.1 Ortak tipler

```ts
type ProfilOzeti = {
  kullaniciAdi: string;
  gorunenAd: string;
  avatarRengi: number; // 0–7
};

type Profil = ProfilOzeti & {
  bio: string;
  istatistik: { sarki: number; takipci: number; takip: number; cover: number };
  takipEdiyorMuyum: boolean; // kendi profilinde false
  benMiyim: boolean;
};

type KaynakOzeti = {
  yayinId: string;
  baslik: string;
  sahipKullaniciAdi: string;
  silindi: boolean; // orijinal kaldırıldıysa true, diğer alanlar son bilinen değer
};

type YayinOzeti = {
  id: string;
  slug: string;
  baslik: string;
  sahip: ProfilOzeti;
  tur: string;
  bpm: number;
  ton: { tonik: string; mod: 'major' | 'minor' };
  sureSn: number;
  dalgaFormu: number[]; // 64 değer ya da boş
  begeniSayisi: number;
  yorumSayisi: number;
  coverSayisi: number;
  begendimMi: boolean;
  altyapiAcik: boolean;
  kaynak: KaynakOzeti | null; // cover ise dolu
  olusturma: string;
};

type Sozler = { bolumler: { bolumId: string; tur: string; satirlar: string[] }[] };

type YayinDetayi = YayinOzeti & {
  aciklama: string;
  yorumAcik: boolean;
  sozler: Sozler;
  yapi: { id: string; tur: string; olcu: number }[];
  durum: 'hazirlaniyor' | 'yayinda' | 'hata';
  benimMi: boolean;
  gorunurluk: 'herkes' | 'takipciler' | 'link'; // yalnızca benimMi ise anlamlı
};

type Yorum = {
  id: string;
  yazar: ProfilOzeti;
  metin: string;
  zamanMs: number | null;
  olusturma: string;
  silebilirMiyim: boolean; // yazar ya da yayın sahibi
};
```

### 4.2 Profil

| Uç | İstek | Yanıt | Hatalar |
|---|---|---|---|
| `GET /profil/ben` | — | `Profil` | 404 `profil_yok`: profil henüz oluşturulmamış |
| `PUT /profil/ben` | `{ kullaniciAdi, gorunenAd, bio }` | `Profil` | 409 `kullanici_adi_alinmis`, 422 `gecersiz_kullanici_adi` |
| `GET /profil/kullanici-adi/uygun?ad=` | — | `{ uygun: boolean, oneri: string \| null }` | 422 `gecersiz_kullanici_adi` |
| `GET /profiller/{kullaniciAdi}` | — | `Profil` | 404 `bulunamadi` |
| `GET /profiller/{kullaniciAdi}/yayinlar?tur=sarkilar\|coverlar` | sayfalı | `YayinOzeti[]` (görünürlüğe göre süzülmüş) | 404 |

`PUT /profil/ben` hem oluşturur hem günceller. `oneri`, ad alınmışsa sonuna sayı eklenmiş uygun bir addır.

### 4.3 Yayın

**`POST /yayinlar`** — yayınla. Önce profil olmalıdır; yoksa 409 `profil_yok` döner.

```ts
type YayinIstegi = {
  baslik: string;
  aciklama: string;
  gorunurluk: 'herkes' | 'takipciler' | 'link';
  altyapiAcik: boolean; // kaynakYayinId varsa yok sayılır, false kaydedilir
  yorumAcik: boolean;
  masterUrl: string; // kullanıcının kendi R2 adresi
  stemler: { id: string; enstruman: string; url: string; sure: number }[]; // altyapiAcik ise en az 1
  sozler: Sozler;
  yapi: { id: string; tur: string; olcu: number }[];
  tur: string;
  bpm: number;
  ton: { tonik: string; mod: 'major' | 'minor' };
  sureSn: number;
  kaynakYayinId: string | null;
};
```

- **Yanıt:** `202 { id, slug, durum: "hazirlaniyor" }`.
- **Hatalar:**
  - 403 `sahiplik`: bir adres kullanıcıya ait değil.
  - 404 `kaynak_bulunamadi`.
  - 422 `dogrulama`: gövde çözülemedi ya da bir alan kuralı bozuldu. Metin alanlarından (başlık, açıklama, tür, ton, `masterUrl`, stem, yapı ve söz alanları) biri NUL karakteri içeriyorsa da bu hata döner.
  - 409 `profil_yok`, 429 (kullanıcı başına saatte 30).
- **Sınırlar** (karakter sayısı rune ile ölçülür; başlık ve açıklama kırpılır): `baslik` 1–80, `aciklama` en çok 300, `tur` 1–40, `ton.tonik` 1–16, `bpm` 20–300, `sureSn` > 0, en çok 32 stem, en çok 200 `yapi` öğesi ve 200 söz bölümü, bölüm başına en çok 200 satır ve toplamda 1000 satır, tek satır en çok 200 karakter, `bolumId`/bölüm türü/yapı `id` ve `tur` en çok 64 karakter.

Sunucu adreslerin sahipliğini `SahipliAnahtarCoz` ile doğrular. Ardından asynq'e bir `sosyal:hazirla` işi koyar. Bu iş:
1. Dosyaları §3.1'e kopyalar.
2. audio-service ile 64 tepe değerlik dalga formunu ve 30 sn'lik önizlemeyi üretir.
3. `durum`u `yayinda` yapar.

Dalga formu veya önizleme üretilemezse yayın yine `yayinda` olur: dalga formu boş dizi kalır, önizleme olmaz. Kopyalama başarısız olursa `durum` `hata` olur. `hazirlaniyor` ve `hata` durumundaki yayınlar yalnızca sahibine görünür.

**Diğer uçlar:**

| Uç | İstek | Yanıt | Not |
|---|---|---|---|
| `GET /yayinlar/{id}` | — | `YayinDetayi` | Görme yetkisi yoksa 404 |
| `GET /s/{slug}` | — | `YayinDetayi` | Slug ile aynı detay (deep link için) |
| `POST /yayinlar/{id}/tekrar-dene` | — | `202 { id, slug, durum: "hazirlaniyor" }` | Yalnızca sahibi, yalnızca `hata` durumunda; başka durumda 409 `durum`, sahibi değilse 404 |
| `PATCH /yayinlar/{id}` | `{ aciklama?, gorunurluk?, altyapiAcik?, yorumAcik? }` | `YayinDetayi` | Yalnızca sahibi. 422 `stem_yok`: `altyapiAcik: true` istenen yayının stem kopyası yok (altyapı kapalı yayınlanmış) ya da yayın bir cover |
| `DELETE /yayinlar/{id}` | — | `204` | Yalnızca sahibi. Yumuşak silme; dosyalar 24 saat sonra `bakim` kuyruğunda silinir |
| `POST /yayinlar/{id}/dosyalar` | — | `{ master: { url, expiresAt? } }` | Dinleme için. 1 saat geçerli; adres kalıcıysa `expiresAt` yoktur. Yayın henüz `yayinda` değilse 409 `durum` ("hâlâ hazırlanıyor") |
| `GET /akis?sekme=senin-icin\|takip\|yeni` | sayfalı | `YayinOzeti[]` | §5.1 |

### 4.4 Beğeni ve takip

| Uç | Yanıt | Not |
|---|---|---|
| `PUT /yayinlar/{id}/begeni` | `{ begeniSayisi, begendimMi: true }` | Tekrarı güvenli |
| `DELETE /yayinlar/{id}/begeni` | `{ begeniSayisi, begendimMi: false }` | Tekrarı güvenli |
| `PUT /profiller/{kullaniciAdi}/takip` | `{ takipci: number, takipEdiyorMuyum: true }` | Kendini takip: 422 `kendini_takip` |
| `DELETE /profiller/{kullaniciAdi}/takip` | `{ takipci: number, takipEdiyorMuyum: false }` | |
| `GET /oneriler` | `ProfilOzeti[]` (en çok 10, sayfasız) | Takip etmediğin, son 30 günde en çok beğeni alan yayıncılar |

- Takip: kullanıcı adı yoksa 404 `bulunamadi`. `DELETE` kendine de 422 `kendini_takip` döner. Takip eden için profil gerekmez.
- Öneriler: `herkes` ya da `takipciler` görünürlüğünde en az bir `yayinda` yayını olan yayıncılar. Puan, bu yayınların son 30 günde başkalarından aldığı beğenilerdir; sahibin kendi beğenisi sayılmaz. Eşitlikte son yayını yeni olan öne geçer. Liste boşsa `[]` döner.

### 4.5 Yorumlar

| Uç | İstek | Yanıt | Hatalar |
|---|---|---|---|
| `GET /yayinlar/{id}/yorumlar` | sayfalı | `Yorum[]`: önce `zamanMs` artan, sonra damgasızlar (`olusturma` azalan) | 403 `yorum_kapali` |
| `POST /yayinlar/{id}/yorumlar` | `{ metin, zamanMs: number \| null }` | `201 Yorum` | 403 `yorum_kapali`, 409 `profil_yok`: profil henüz oluşturulmamış, 422 `dogrulama`, 429 `yavasla` (kullanıcı başına dakikada 10) |
| `DELETE /yorumlar/{id}` | — | `204` | 403 `yetki`: yazar ya da yayın sahibi değilse (yayını göremeyene 404). Silinmiş yorum 404 |

- `metin` kırpılır, 1–500 karakterdir. `zamanMs` null ya da 0 ≤ `zamanMs` ≤ `sureSn` × 1000 olmalıdır.
- `yorumAcik` false ise iki uç da yayın sahibine dahil 403 `yorum_kapali` döner. Yayın görünmüyorsa önce 404 döner.
- 429 `yavasla` sınırı istek başınadır; geçersiz istekler de sayılır.

### 4.6 Cover

| Uç | Yanıt | Hatalar |
|---|---|---|
| `GET /yayinlar/{id}/coverlar` | sayfalı `YayinOzeti[]` | Altyapı kapalıysa boş liste |
| `POST /yayinlar/{id}/cover-baslat` | `CoverBaslangici` | 403 `altyapi_kapali` (altyapı kapalı, yayın bir cover ya da stem kopyası yok), 404, 409 `durum` (sahibin henüz `yayinda` olmayan yayını), 429 (kullanıcı başına saatte 20) |

```ts
type CoverBaslangici = {
  kaynak: KaynakOzeti;
  tur: string;
  bpm: number;
  ton: { tonik: string; mod: 'major' | 'minor' };
  sureSn: number;
  yapi: { id: string; tur: string; olcu: number }[];
  sozler: Sozler;
  stemler: { id: string; enstruman: string; fileUrl: string; sure: number }[];
};
```

Sunucu yayının stem'lerini kullanıcının kendi önekine kopyalar. Anahtarlar mevcut render stem düzenine uyar. `fileUrl` alanları, mevcut `/api/files/sign` ile yeniden imzalanabilen olağan kullanıcı adresleridir. Kopyalama eşzamanlı yapılır, çünkü stem'ler aynı bucket içinde sunucu tarafında kopyalanır.

### 4.7 Şikayet

| Uç | İstek | Yanıt |
|---|---|---|
| `POST /sikayetler` | `{ hedefTuru: 'yayin' \| 'yorum', hedefId, neden, aciklama? }` | `201`, gövdesiz. Tekrar gönderilirse yine `201` döner, yeni kayıt açılmaz. Hedef görünmüyor ya da yoksa 404, alan kuralı bozuksa 422 `dogrulama` |

### 4.8 Herkese açık önizleme (oturumsuz)

`GET /api/public/s/{slug}` şunu döner:

```json
{ "baslik", "sahip": ProfilOzeti, "sureSn", "dalgaFormu", "onizlemeUrl" | null, "tur" }
```

- Yalnızca `herkes` ve `link` görünürlüğündeki, durumu `yayinda` olan yayınlar için çalışır. Diğerleri 404 döner.
- IP başına dakikada 30 istek sınırı vardır; aşılırsa 429 döner. IP, Traefik'in yazdığı `X-Real-Ip` başlığından okunur.
- Web sunucusu (`make-singer-frontend`) bütün ziyaretçiler adına tek IP'den çağırır. Bu yüzden `X-Web-Anahtari: <SOSYAL_WEB_ANAHTARI>` başlığını gönderir; anahtar doğruysa istek IP yerine ortak bir sayaçta dakikada 600 ile sınırlanır. Anahtar yanlışsa olağan IP sınırı geçerlidir. Sunucuda anahtar boşsa bu yol kapalıdır.
- `onizlemeUrl` 1 saat geçerli imzalı bir adrestir. Yanıt `Cache-Control: no-store` taşır.

### 4.9 Görünürlük kuralı

Her uçta sunucu tarafında uygulanır. Görme yetkisi olmayana `404 bulunamadi` döner.

| Görünürlük | Akış "Yeni" / "Senin için" | Akış "Takip" | Profil listesi | id veya slug ile detay | Herkese açık önizleme |
|---|---|---|---|---|---|
| herkes | Evet | Takip edenlere | Evet | Evet | Evet |
| takipciler | Hayır | Takip edenlere | Sahibi ve takipçileri | Sahibi ve takipçileri | Hayır |
| link | Hayır | Hayır | Yalnızca sahibine | Evet | Evet |

Sahibi kendi yayınlarını her durumda görür.

## 5. Kurallar

### 5.1 Akış

- **yeni:** `herkes`, `yayinda`, `silinme IS NULL`; `olusturma` azalan.
- **takip:** takip ettiklerinin `herkes` ve `takipciler` yayınları; `olusturma` azalan.
- **senin-icin:** `herkes` yayınları; kendi yayınların hariç.
  - Puan = `(1 + b + 2·y + 3·c) / (saat + 2)^1.5`
    - `b`, `y` ve `c`: son 7 günün beğeni, yorum ve cover sayıları.
    - `saat`: yayından bu yana geçen saat.
  - Puan sorgu sırasında SQL'de hesaplanır. İmleç, (puan, id) çiftini kodlar.

### 5.2 Diğer

- Silinen yorum listeden düşer ve `yorumSayisi` azalır.
- `yorumAcik` false ise yorum listesi ve yazma çubuğu mobilde gizlenir. Uçlar 403 döner.
- `altyapiAcik` false ise "Bu altyapıda söyle" ve "söyleyenler" şeridi gizlenir. Var olan cover'lar sahiplerinin profillerinde kalır.
- Cover'ın cover'ı yoktur: cover yayınlarında `altyapiAcik` her zaman false'tur.

### 5.3 Hesap silme

Mevcut `DELETE /api/account` akışı genişler:
- Silinen kullanıcının şu kayıtları silinir: profil, yayınlar ve `sosyal/{yayinId}/` dosyaları, yorumlar, beğeniler, takipler, şikayetler.
- Başkalarının cover'ları kalır. Onların `kaynak.silindi` alanı true olur.

## 6. Mobil

### 6.1 Alan katmanı

`src/hooks/domain/social/` klasörü:
- `schema.ts`: §4.1 tipleri için Zod şemaları.
- `socialService.ts`: `ky` istemcisi; her uç için bir metot.
- Sorgu ve mutasyon hook'ları:
  - Listeler (`useFeed`, `useComments`, `useProfilePublications`, `useCovers`) `useInfiniteQuery` kullanır.
  - Beğeni, takip ve yorum gönderme iyimser güncellenir; hata olursa önceki önbellek geri yüklenir.

### 6.2 Ekranlar ve bileşenler

| Ekran | Not |
|---|---|
| Keşfet akışı (16) | Tab. `ScreenHeader` ("Keşfet" · arama). Filtre chip'leri: Senin için / Takip ettiklerin / Yeni. `FeedCard` listesi. Takip boşken öneriler gösterilir |
| Şarkı detayı (17) | `TimedComments` dalga formu, beğeni/yorum/paylaş, `CoverStrip`, yorum listesi, `CommentBar`. Island "Bu altyapıda söyle" |
| Paylaş paneli (18) | Export'taki "Keşfet'te paylaş"tan açılır. Panelde: açıklama, "Altyapıyı başkalarına aç" (cover'da gizli), "Yorumlara izin ver", görünürlük (3 seçenek), "Yayınla". Profil yoksa önce Kullanıcı adı paneli açılır |
| Kullanıcı profili (20) | `ProfileHeader`, Şarkılar/Coverlar sekmeleri, 2 sütun grid |
| Kullanıcı adı paneli | Görünen ad ve `@kullanıcı` (uygunluk anlık kontrol edilir) |

- **Yeni bileşenler:** `FeedCard`, `TimedComments`, `CommentBar`, `CoverStrip`, `ProfileHeader`.
- **Dalga formu:** `dalgaFormu` boşsa mevcut `Waveform` yayın id'sini tohum (seed) alarak çizilir.
- **Island:**
  - Keşfet'te çalan şarkı `global/live` (playing) durumunu gösterir.
  - Yayın hazırlanırken island "Yayınlanıyor" gösterir. Bunun için `GET /yayinlar/{id}` yoklanır; WebSocket kullanılmaz.
- **Dinleme:** imzalı adres `POST /yayinlar/{id}/dosyalar` ile alınır ve `useAudioPlayer` ile çalınır. Adresin süresi dolarsa aynı uçla yenilenir.
- **Linking:** `s/:slug` → Şarkı detayı.

### 6.3 Cover projesi (ekran 19)

- `Project` şemasına isteğe bağlı iki alan eklenir:
  - `kind?: 'original' | 'cover'` (varsayılan `original`).
  - `coverSource?: KaynakOzeti`.
- `cover-baslat` yanıtından cihazda yeni bir proje oluşur:
  - `brief.structure` ← `yapi`
  - `lyrics` ← `sozler`
  - `renders[0]` ← stem'ler, BPM ve ton
  - `currentStep` ← `vocal`
- **Adımlar:** cover projesinde adım listesi `['vocal', 'mixer', 'master', 'share']`'dir. `PROJECT_STEPS`, proje türüne göre seçilir. Island halkası 1/4 ile başlar.
- **Paylaş adımı:** Paylaş paneliyle aynıdır, `kaynakYayinId` ile gönderilir.
- **Vokal sahnesi:** lime renkli "Orijinal: @kullanıcı · şarkı" etiketi gösterilir.

### 6.4 Hata durumları

| Durum | Davranış |
|---|---|
| Ağ hatası (listeler) | "Tekrar dene" kartı |
| 404 | "Bu şarkı artık yok" ekranı |
| 409 `kullanici_adi_alinmis` | Panelde satır içi hata ve `oneri` |
| 429 `yavasla` | Yorum çubuğunda "Biraz yavaşla" mesajı |
| 403 `yorum_kapali` | Yorum alanı gizlenir |
| 403 `altyapi_kapali` | Island butonu gizlenir, bilgi mesajı gösterilir |
| Yayın `hata` | Island'da hata durumu ve "Tekrar dene" (`POST /yayinlar/{id}/tekrar-dene`) |

## 7. Link sayfası ve deep link

- **Link:** `https://makesinger.celalettindemir.dev/s/{slug}`. Paylaş butonu bu adresi sistem paylaşım sayfasıyla paylaşır.
- **Uygulama yüklüyse:** iOS'ta Universal Links, Android'de App Links kullanılır.
  - `make-singer-frontend` `/.well-known/apple-app-site-association` ve `/.well-known/assetlinks.json` dosyalarını sunar; yol deseni `/s/*`.
  - Mobilde iOS associated domains (`applinks:makesinger.celalettindemir.dev`) ve bir Android intent filter tanımlanır.
- **Uygulama yoksa:** `make-singer-frontend` `/s/[slug]` sayfasını sunucu tarafında üretir. Sayfada:
  - kapak gradyanı, başlık, `@kullanıcı`, dalga formu
  - 30 sn önizleme oynatıcısı
  - "Uygulamada aç" ve mağaza bağlantıları
  - Open Graph meta etiketleri
- **Gizli yayın:** `takipciler` yayını ya da bulunmayan bir slug için sayfa "Bu şarkı gizli ya da kaldırılmış" der.

## 8. Testler

- **Backend:**
  - Birim testleri: görünürlük tablosu (§4.9), sahiplik doğrulaması, kullanıcı adı kuralları, puan sıralaması, hesap silme genişlemesi.
  - Canlı Postgres testleri: `KIMLIK_TEST_DB_URL` düzeniyle.
  - asynq iş testleri: sahte R2 ve sahte audio-service ile.
- **Mobil:**
  - Şema testleri: Zod ile.
  - Servis testleri: sahte `ky` yanıtlarıyla.
  - Hook testleri: iyimser güncelleme ve geri alma.
  - Ekran testleri: RNTL ile; testler İngilizce çeviriyle koşar.
- **Web:** `/s/[slug]` için sahte API yanıtıyla render testi.

## 9. Aşamalar

| Aşama | Mobil | Backend | Web |
|---|---|---|---|
| 5a | Profil ve kullanıcı adı paneli, Paylaş paneli ve yayınlama, akış (Yeni ve Senin için), şarkı detayı (dinleme, beğeni), `s/:slug` rotası | §3 tabloları, §4.2, §4.3, §4.4 beğeni, §4.9, §5.1, §5.3, `sosyal:hazirla` işi | — |
| 5b | `TimedComments`, yorum listesi, `CommentBar`, şikayet | §4.5, §4.7 | — |
| 5c | Takip, kullanıcı profili ekranı, öneriler, Takip sekmesi | §4.4 takip ve öneriler, profil listeleri | — |
| 5d | "Bu altyapıda söyle", cover projesi, `CoverStrip` | §4.6, §4.8 | `/s/[slug]` sayfası, `.well-known` dosyaları |

Mobil her aşamayı sözleşmeye karşı sahte servisle geliştirir. Gerçek uçlara geçiş, backend o aşamayı yayınlayınca yapılır.

## 10. Kapsam dışı

Bildirim ekranı, DM, meydan okumalar, moderasyon akışı, avatar fotoğrafı yükleme, kişiselleştirilmiş öneri, arama sonuç ekranı, yorum yanıtları ve yorum beğenisi.

Keşfet başlığındaki arama ikonu bu sürümde görünür ama devre dışıdır.
