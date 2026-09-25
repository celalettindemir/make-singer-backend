# R2 public/private bucket ayrımı

Tarih: 2026-09-18
Durum: tasarım onaylandı, uygulanmadı

## Sorun

Kod tek bir R2 bucket'ı destekliyor. `core-service/internal/config/config.go:136`
ve `audio-service/main.py:35` tek bir `R2_BUCKET_NAME` okuyor. Kümedeki
manifest bu değere `makeasinger` yazıyor, ama Cloudflare hesabında böyle bir
bucket yok. Gerçek bucket'lar `makeasinger-public` ve `makeasinger-private`.
Yani api ve audio-service şu an var olmayan bir bucket'a yazmaya çalışıyor.

Ayrıca bütün dosyalar aynı erişim modelini paylaşıyor. Kullanıcının ham ses
kaydı ile paylaşmak için ürettiği nihai export aynı yerde duruyor.

## Kapsam

Bu spec yalnızca depolama ayrımını kapsar. Kimlik doğrulama, ForwardAuth,
Zitadel ve admin tarafı kapsam dışıdır; onlar ayrı bir tasarımın konusu.

## Kararlar

| Konu | Karar |
|---|---|
| `exports/` | public bucket, CDN üzerinden kalıcı URL |
| `vocals/`, `masters/`, `stems/` | private bucket, presigned URL |
| Presigned URL ömrü | 1 saat |
| R2 kimliği | İki bucket'a da yetkili TEK token |

Gerekçe: export, kullanıcının paylaşmak için ürettiği nihai üründür ve CDN
önbelleğinden hızlı akması istenir. Diğer üçü kullanıcının ham çalışma
dosyalarıdır.

## Yapılandırma

`R2_BUCKET_NAME` kaldırılır, yerine:

| Değişken | Değer |
|---|---|
| `R2_PUBLIC_BUCKET` | `makeasinger-public` |
| `R2_PRIVATE_BUCKET` | `makeasinger-private` |
| `R2_PUBLIC_URL` | `https://makesinger-cdn.celalettindemir.dev` (yalnız public bucket'ı tanımlar) |
| `R2_PRESIGN_TTL` | `1h` |

Kimlik bilgileri değişmez: `makesinger-kimlik.sops.yaml` içindeki
`r2_account_id`, `r2_access_key_id`, `r2_secret_access_key`. Tek token her iki
bucket'a yetkilendirilir; iki ayrı token kodda iki ayrı kimlik taşımayı
gerektirirdi ve bir karşılığı ölçülmedi.

## Yönlendirme kuralı

Hedef bucket dosya anahtarının önekinden belirlenir. Kural TEK bir yerde,
`bucketFor(key)` fonksiyonunda durur ve iki serviste de birebir aynıdır:

    exports/  -> public
    digerleri -> private

Bilinmeyen bir önek private'a gider. Yanlış tarafa düşen bir dosya sızıntı
degil, yalnız erişilemezlik üretsin diye varsayılan bu yöndedir.

Kullanımdaki önekler (ölçüldü): `exports/%s.{mp3,wav,zip}` — `export_service.go:47,107,140`;
`vocals/%s/%s/%s.wav` — `upload_service.go:37`; `masters/%s/%s.wav`; `stems/%s/%s.wav`.

## Depolama katmanı

**Go (core-service).** `StorageClient` arayüzü (`internal/client/r2_client.go:17-22`)
bugün `Upload`, `Delete`, `GetSignedURL`, `GetPublicURL` taşıyor; presigner
zaten kurulu. `R2Client` tek `bucketName` yerine iki bucket adı tutar. `Upload`
ve `Delete` hedefi `bucketFor(key)` ile seçer. `GetSignedURL` private bucket'a
bağlanır. Yeni bir `URLFor(key)` metodu doğru olanı döndürür: public ise CDN
adresi, private ise presigned URL.

**Python (audio-service).** `StorageService.__init__` (`services/storage.py:12-31`)
iki bucket adı alır. `upload`, `upload_file` ve `delete` aynı `bucket_for`
kuralını uygular. `get_public_url` ikiye ayrılır; private anahtarlar için boto3
`generate_presigned_url` kullanılır.

Kural iki dilde iki kez yazılacağı için ikisinde de aynı önek tablosunu
doğrulayan testler bulunur. Tablo değişirse iki testin de güncellenmesi gerekir.

## API yanıtları

`exports/` linkleri bugünkü gibi kalıcı CDN adresleridir.

`vocals/`, `masters/`, `stems/` linkleri her istekte yeniden üretilir ve bir
saat geçerlidir. Bu linkleri döndüren her yanıta `expiresAt` alanı eklenir
(RFC 3339). Alan olmazsa istemci süreli linki önbelleğe alır ve sonra kırık
link gösterir.

Servisler arası aktarımda presigned URL kullanılmaz. audio-service bir dosyayı
işlemek için indirdiğinde doğrudan S3 API ile okur.

## Kablo biçimi (2026-09-25 kararı)

audio-service bugün dosyaları S3 ile değil, kendisine verilen URL'den httpx ile
indiriyor (`services/storage.py:34-45`; çağrı yerleri `master.py:74,163`,
`encoder.py:37`, `archiver.py:44`). Bu URL'ler istemciden geliyor: mobil
uygulama `masterFileUrl` ve `stemUrls` alanlarını API'ye geri yolluyor
(`internal/model/export.go:8,34,52-57`, `master.go:9,39,48`). Dosyalar private
bucket'a taşınınca bu indirmeler kırılır.

**Karar: URL'yi anahtara Go çevirir.** İstemci API'si DEĞİŞMEZ; mobil ve web
uygulamasında değişiklik gerekmez. Go, istemciden gelen URL'den nesne
anahtarını çıkarır (`KeyFromURL`) ve audio-service'e anahtarı gönderir.

Reddedilen iki seçenek: (a) istemcinin anahtar göndermesi — en temizi ama
kırıcı değişiklik; (b) Python'un URL'den anahtar çözmesi — presigned sorgu
dizesini ayıklamak kırılgan ve hatası sessiz olur.

Go -> Python kablosunda URL taşıyan alanlar anahtar taşıyan alanlarla
DEĞİŞTİRİLİR (iki servis birlikte sevk edildiği icin geriye uyumluluk alanı
tutulmaz):

| Bugun | Yeni |
|---|---|
| `MasterRequest.stem_urls` | `stem_keys` |
| `VocalTakeInput.url` | `key` |
| `EncodeRequest.input_url` | `input_key` |
| `ZipFileEntry.url` | `key` |

`output_key` alanlari zaten anahtar tasiyor, degismez. Yanit alanlari
(`output_url`) degismez: audio-service yukledikten sonra dogru URL'yi
`URLFor` kuralina gore uretir.

`KeyFromURL` hem CDN adresini (`<R2_PUBLIC_URL>/<key>`), hem R2 endpoint
adresini (`https://<bucket>.r2.cloudflarestorage.com/<key>`), hem de presigned
bir URL'yi (sorgu dizesi atilir) kabul eder. Taninmayan bir konak adi hata
dondurur; tahmin edilmez.

## Test

TDD ile ilerlenir, testler önce yazılır.

- `bucketFor` / `bucket_for`: her önek için hedef bucket, bilinmeyen önek
  private'a düşüyor mu.
- Presigned URL üretimi: doğru bucket, süre 1 saat, URL imza taşıyor.
- `URLFor`: `exports/` için CDN adresi, digerleri için presigned URL.
- Depolama katmanı: sahte S3 ile upload ve delete'in doğru bucket'a gitmesi.
- API yanıtları: private link dönen uçlarda `expiresAt` dolu.

## Bilinen sınır

Presigned URL, linke sahip olan herkese erişim verir. API'de bugün sahiplik
kontrolü yok, çünkü kimlik doğrulama henüz kurulmadı. Bu nedenle bu değişiklik
tek başına "kullanıcı A, kullanıcı B'nin vokalini indiremez" garantisini
vermez; o garanti auth aşamasıyla gelir. Bu iş auth'u beklemeden yapılabilir,
ama sıra bilinerek yapılmalıdır.

## Sevkiyat

Kod değişikliği iki servisi de kapsadığı için iki imaj da yeniden üretilir.
Kümede `k3s-gitops/apps/makesinger/makesinger-backend.yaml` içindeki env
listesi yeni değişkenlere geçirilir ve imaj digest'leri güncellenir. Eski
`R2_BUCKET_NAME` satırı silinir.

Sevkiyattan önce `makesinger-cdn.celalettindemir.dev` custom domain'inin
`makeasinger-public` bucket'ına bağlı olması gerekir; bağlı değilse export
linkleri 404 döner.
