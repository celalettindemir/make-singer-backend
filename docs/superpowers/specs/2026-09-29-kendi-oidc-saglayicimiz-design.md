# Zitadel'in kaldırılması ve kendi OIDC sağlayıcımız

Tarih: 2026-09-29
Durum: tasarım onaylandı, uygulanmadı

## Sorun

Zitadel kümede ayakta ama backend ile hiçbir bağı yok. `makesinger-backend.yaml:37-38`
`GATEWAY_ENABLED: "true"`, `ZITADEL_DOMAIN/ISSUER/CLIENT_ID` ise bilinçli olarak boş
(satır 66-71). Bu modda `GatewayAuthMiddleware` (`gateway_auth.go:10-22`) `X-User-Id`
header'ına **hiçbir doğrulama yapmadan** güveniyor; JWKS doğrulayıcı hiç kurulmuyor
(`main.go:114-122`). k3s'te ForwardAuth tanımı yok, Zitadel için IngressRoute yok.
Yani Zitadel fiilen devre dışı: ayakta tuttuğu şey iki container, ayrı bir Postgres
veritabanı, 1Gi PVC ve bir masterkey.

Buna karşılık ortada **token üretecek hiçbir bileşen yok**. Backend'de `/auth/login`,
`/auth/register`, `/auth/refresh` yok (`main.go:189-259` tam rota listesi). Şifre
saklama, kullanıcı kaydı, bcrypt bağımlılığı yok. `middleware.GenerateToken`
(`auth.go:121`) tek token üreticisi ve hiçbir çağıranı yok — ölü kod. Kayıt/şifre
işi tamamen Zitadel'e devredilmişti; legacy HMAC yolu (`legacy.go`) yalnızca
doğrulayıcı, üretici tarafı hiç yazılmamış.

Ayrıca kimlikten bağımsız iki boşluk var ve auth'a dokunmadan kapatılamıyor:

- **Sahiplik kontrolü hiç yok.** `GetUserID` çağrısı tüm kod tabanında yalnızca
  `ratelimit.go:24`. Hiçbir handler, service veya worker userId okumuyor. `Job`
  struct'ında `OwnerID` alanı yok (`model/job.go:6-19`). Sonuç: kimliği doğrulanmış
  herhangi bir kullanıcı `jobId`/`takeId` tahmin ederek başkasının işini okuyabilir,
  iptal edebilir, dosyasını silebilir.
- **`/ws/jobs/:jobId` auth'un tamamen dışında** (`main.go:249-259`).

## Kapsam

Bu spec Zitadel'in sökülmesini, yerine kendi OpenID Provider'ımızın kurulmasını,
sahiplik kontrolünün getirilmesini, mobil tarafta auth yüzeyi ile ona bağlı iki
kusurun düzeltilmesini, ve bunların sonucunda `makesinger-api.celalettindemir.dev`
adresinin dışa açılmasını kapsar. API'nin bugüne kadar kapalı tutulmasının tek
gerekçesi doğrulanmayan `X-User-Id`'ydi; bu iş o gerekçeyi ortadan kaldırıyor.

Kapsam dışı, bilinçli olarak: mobil ses motoru (gerçek kayıt ve çoklu kanal
oynatma — bugün tamamen mock, kendi tasarımını ister), bulut proje senkronu
(projeler cihazdaki MMKV'de kalır), admin paneli.

## Kararlar

| Konu | Karar |
|---|---|
| Kimlik | Kendi OpenID Provider'ımız, `github.com/zitadel/oidc/v3/pkg/op` ile |
| Giriş yöntemleri | E-posta + şifre, Apple ile giriş, Google ile giriş |
| Akış | Authorization Code + PKCE (S256), public client, secret yok |
| Nerede çalışır | core-service ile **aynı binary**, ikinci HTTP dinleyicisi |
| Hostname | `makesinger-auth.celalettindemir.dev` |
| Kalıcı veri | Postgres (CNPG `pg-kiracilar`), Zitadel'in veritabanı devralınır |
| Kısa ömürlü veri | Redis (authorization code, login durumu, WS bileti, rate limit) |
| İmzalama anahtarı | RS256, sops'lu Kubernetes Secret, iki anahtar yayınlanabilir |
| access_token | RS256 JWT, 15 dakika |
| refresh_token | Opak, Postgres'te hash'li, 60 gün kayan, her kullanımda döner |
| Sahiplik — işler | `Job.OwnerID`, sahibi değilse 404 |
| Sahiplik — dosyalar | R2 anahtarı kullanıcıyla öneklenir (yapısal) |
| MVP'de yok | E-posta doğrulama, şifre sıfırlama, MFA, consent ekranı |

## Neden kendi OP'umuz, neden kütüphaneyle

Alternatifler değerlendirildi. Opak Redis oturum token'ı ve "JWT + refresh" gibi
standart dışı şemalar reddedildi: OIDC'nin authorization code akışı, native
uygulamalar için doğru olan yoldur (RFC 8252, sistem tarayıcı sekmesi) ve mobil
uygulamada bu altyapı **zaten kurulu** — `react-native-app-auth` 8.1.0, Android
intent-filter'ları (`AndroidManifest.xml:26-48`), `ios/AppDelegate+RNAppAuth.m`.
Standardı korumak mobil tarafta iş azaltıyor, artırmıyor.

OIDC elle yazılmaz: PKCE, nonce, `at_hash`, anahtar rotasyonu, kod tek
kullanımlılığı — güvenlik hatalarının yaşadığı yer. `zitadel/oidc` Apache-2.0
lisanslı bağımsız bir Go paketidir; Zitadel ürününü çalıştırmakla hiçbir ilgisi
yok, ağ teması yok. Kaldırdığımız şey işletimsel (container, veritabanı, PVC,
masterkey, sızmış PAT'lar), aldığımız şey `go.mod`'da bir satır.

Bu bağımlılığın gerçek riski `Storage` arayüzüdür: yirmiye yakın metot, yani
kodumuzun şekli kütüphanenin soyutlamalarına göre kurulur. Karşı önlem: sürüm
kesin pinlenir ve `Storage` uygulaması kendi arayüzümüzün arkasına konur, böylece
kütüphane değişirse tek bir katmanın işi olur. Değerlendirilen alternatif
`ory/fosite` daha alt seviyedir ve OIDC'nin bir kısmını bizim birleştirmemizi
bekler — daha çok kod, daha çok hata yüzeyi.

## Mimari

### Nerede çalışır

OP, core-service'in aynı binary'si içinde **ikinci bir HTTP dinleyicisinde**
(8001) çalışır. Sebep teknik: `op` paketi `net/http` üzerine kurulu, core-service
ise Fiber/fasthttp. Aynı Fiber uygulamasına adaptörle bağlamak her istekte gövde
kopyalayan kırılgan bir ara katman demektir. İkinci dinleyici bunu ortadan
kaldırır ve ayrı imaj, ayrı CI hattı, ayrı deployment gerektirmez; config, Redis
ve kullanıcı deposu paylaşılır.

Dışarıya kendi IngressRoute'u ve sertifikasıyla `makesinger-auth.celalettindemir.dev`
olarak çıkar. Cloudflare "Flexible" SSL modu gereği, diğer hostlarda olduğu gibi
iki route gerekir: `websecure` üzerinde `tls:` bloklu olan, ve `web` üzerinde
`tls:` bloğu **olmayan** ayrı bir `-http` route (bkz. `apps/makesinger/ingress.yaml`).

### Uçlar

Standart OIDC yüzeyi: `/.well-known/openid-configuration`, `/authorize`, `/token`,
`/userinfo`, `/jwks.json`, `/end_session`.

Bizim sunduğumuz sayfalar: giriş formu, kayıt formu, hesap bağlama formu. Sunucu
tarafında render edilen sade HTML; JavaScript çatısı yok. Dil, OIDC'nin standart
`ui_locales` parametresinden okunur; `tr` ve `en` desteklenir, tanınmayan değerde
`en`'e düşülür.

### Veri yerleşimi

Üç yer, dayanıklılık ihtiyacına göre ayrılmıştır.

**Postgres** (CNPG `pg-kiracilar`, Zitadel'in veritabanı devralınır) kalıcı olanı
tutar: kullanıcı kaydı, bcrypt şifre hash'i, federe kimlik eşleşmeleri
(sağlayıcı + sağlayıcıdaki `sub` → bizim `sub`), refresh token kayıtları.

**Redis** kısa ömürlü olanı tutar: authorization code (60 saniye), login akışının
ara durumu, WebSocket bileti (30 saniye), rate limit sayaçları.

LRU eviction bu üç şey için zararsızdır ve bu bilinçli bir kabuldür: bir
authorization code veya login durumu eviction'a düşerse akış hata verir ve
kullanıcı girişi bir kez baştan dener. Kalıcı hiçbir şey kaybolmaz.

Bu ayrım zorunludur, tercih değil: `makesinger-redis.yaml:20-38` Redis'i hiçbir
volume olmadan, AOF/RDB kapalı ve **`--maxmemory-policy allkeys-lru`** ile
çalıştırıyor. Yani pod yeniden başlayınca içerik gider ve bellek dolduğunda Redis
rastgele anahtarları siler. Etiketi `tier: cache`. Kullanıcı hesabı oraya konulamaz.

**Kubernetes Secret** (sops ile şifreli) RS256 imzalama anahtarını tutar. JWKS ucu
bu anahtardan beslenir ve rotasyon için aynı anda iki anahtarı yayınlayabilecek
şekilde kurulur.

### Token modeli

`id_token`: RS256, standart claim'ler — `iss`, `sub`, `aud`, `exp`, `iat`, `nonce`,
`at_hash`, `email`, `email_verified`, `name`.

`access_token`: RS256 JWT, 15 dakika. `/api/*` doğrulaması bu sayede veri deposuna
hiç gitmez. Doğrulama anahtarı **süreç içinden** okunur, kendi JWKS ucumuza ağ
üzerinden gidilmez — aksi halde servis kendi başlangıcına bağımlı hale gelirdi.

`refresh_token`: opak, Postgres'te hash'li, 60 gün kayan. Her kullanımda döner.
Kullanılmış bir refresh token tekrar sunulursa o ailenin tamamı iptal edilir
(çalıntı token tespiti). `end_session` refresh ailesini gerçekten öldürür.

`jwks_verifier.go` silinmez, yönü değişir: Apple/Google `id_token`'larını
federasyon sırasında doğrular, ve ileride ikinci bir istemci (admin paneli)
geldiğinde bizim token'larımızı.

### Federasyon ve hesap bağlama

Apple/Google butonları bizim login sayfamızdadır. Biz onlara karşı RP oluruz:
yetkilendirme sonrası dönen `id_token` sağlayıcının JWKS'i ile doğrulanır,
`(sağlayıcı, sub)` çiftiyle kullanıcı bulunur veya oluşturulur. Mobil uygulama
Apple/Google SDK'sı taşımaz, bu akıştan hiç haberi olmaz.

**E-posta üzerinden otomatik bağlama yapılmaz.** Sağlayıcı `email_verified: true`
dese bile, aynı e-postaya sahip mevcut bir şifreli hesaba sessizce bağlanmaz.
Gerekçe somut bir saldırıdır: e-posta doğrulaması yapmadığımız için saldırgan
kurbanın adresiyle şifreli bir hesap açabilir; kurban Google ile girip otomatik
bağlanırsa şifresi saldırganda olan bir hesaba düşer. Çakışmada bağlama sayfası
çıkar ve mevcut hesabın şifresi bir kez sorulur.

Apple'ın gizli aktarma adresleri (`@privaterelay.appleid.com`) hiçbir koşulda
bağlama için güvenilir sayılmaz.

### Hesap silme

App Store yönergesi 5.1.1(v), hesap oluşturmayı sunan uygulamaların uygulama
içinden hesap silmeyi de sunmasını zorunlu kılar. Bu yüzden hesap silme ucu ve
mobildeki karşılığı kapsamdadır. Silme, kullanıcının Postgres kaydını, federe
eşleşmelerini, refresh ailelerini ve R2'de her önek altındaki `<userId>/` bölümüne
düşen nesneleri (`vocals/<userId>/`, `masters/<userId>/`, `stems/<userId>/`,
`exports/<userId>/`) kaldırır.

## Sahiplik

İki ayrı mekanizma, iki ayrı şeyi korur.

**İşler — kayıtta sahip.** `Job` struct'ına `OwnerID` eklenir ve işi başlatan
kullanıcıdan doldurulur. `render/status`, `render/result`, `render/cancel`,
`master/status`, `master/result` uçları işi yükler ve sahibi değilse **404**
döndürür. 403 değil: 403 o işin var olduğunu doğrular.

Bilinen sınır: iş kayıtları Redis'te 24 saat TTL ile durur ve LRU eviction'a
tabidir. Kayıt düşerse kullanıcı kendi sonucunu da okuyamaz. Bu, sahiplikten
önce de var olan bir kırılganlıktır ve bu spec onu değiştirmez.

**Dosyalar — anahtarda sahip.** R2 anahtar düzeni kullanıcıyla öneklenir:

    vocals/<userId>/<projectId>/<sectionId>/<takeId>.wav
    masters/<userId>/<projectId>/<id>.wav
    stems/<userId>/<projectId>/<id>.wav
    exports/<userId>/<exportId>.{mp3,wav,zip}

Sahiplik böylece yapısal olur: kimse kendi öneki dışına yazamaz ve silemez,
kontrol edilmesi gereken yeni bir tablo doğmaz. `bucketFor` kuralı bozulmaz —
ilk segment hâlâ `exports/`, yani public/private ayrımı aynen çalışır
(bkz. `2026-09-18-r2-public-private-ayrimi-design.md`).

Bir sınırı açıkça yazmak gerekir: `exports/` **public bucket'tadır**, yani önek
orada okuma koruması sağlamaz — linke sahip olan herkes indirir, ki paylaşılabilir
export'un amacı budur. Önek orada yalnızca yazma ve silmeyi kullanıcıya kapatır.
Okuma koruması `vocals/`, `masters/`, `stems/` için geçerlidir; onlar private
bucket'ta ve presigned URL ile sunuluyor. Ayrıca `userId` public URL'lerde görünür
olur; opak bir UUID olduğu için bu kabul edilir.

Bu düzen, listede duran bir kusuru da kapatır: `upload_service.go:70` silme
işlemini `vocals/*/<id>.wav` literal glob anahtarıyla yapıyor ve hiçbir zaman
eşleşmiyor. Anahtar artık deterministik olarak türetilebildiği için silme gerçekten
siler.

Eski düzendeki nesneler için geriye uyumluluk tutulmaz: kümedeki veri deneme
verisidir ve taşınacak hesap yoktur.

## WebSocket

`/ws/jobs/:jobId` bugün auth'un dışındadır; jobId bilen herkes dinler. Kapatılır,
ama React Native'in WebSocket'i iOS'ta özel başlık göndermeyi güvenilir biçimde
desteklemez, token'ı sorgu dizesine koymak da onu loglara ve proxy kayıtlarına
taşır.

Çözüm: mobil, kimliği doğrulanmış normal bir istekle **tek kullanımlık bilet**
alır (Redis, 30 saniye), WebSocket'e onunla bağlanır. Bilet iş sahipliğine bağlıdır
ve ikinci kullanımda reddedilir.

## Auth'a bağlı diğer düzeltmeler

`ratelimit.go:24` bugün userId boşken rate limit'i **tamamen atlıyor**
(`return c.Next()`). Auth middleware bunu garanti ettiği için boş gelmesi bir
hatadır ve fail-open bir kota bypass'ıdır. Fail-closed'a çevrilir.

Küme manifestinde `LOG_LEVEL: debug` (`makesinger-backend.yaml:33-36`) tüm request
header'larını, yani `Authorization`'ı logluyor (`main.go:176`). `info`'ya inilir ve
logger seviyeden bağımsız olarak Authorization'ı maskeler.

## Mobil

Hosted login sayfası olduğu için native kayıt ekranı yazılmaz; Login ekranı yine
tek butonla tarayıcıyı açar.

- `src/hooks/domain/auth/config.ts`: issuer ve client ID bizim OP'umuza döner;
  `dangerouslyAllowInsecureHttpRequests: true` (satır 10-11) kaldırılır; hardcoded
  `CLIENT_ID` (satır 4) env'e taşınır.
- Roller ve `hasRole` silinir — backend `roles` claim'ini hiç okumuyor.
- 401 artık AuthProvider durumunu gerçekten düşürür ve Login'e yönlendirir. Bugün
  `instance.ts:22-24` yalnızca token'ı siliyor, UI haberdar olmuyor.
- Hesap silme girişi eklenir.
- **Presigned URL'ler MMKV'ye yazılmaz.** Bugün `useRenderJob.ts:69` sonucu kalıcı
  yazıyor ve `useMaster.ts:109,151,204` ile `useExport.ts:87,96-98` eski URL'leri
  geri gönderiyor; presign ömrü 1 saat olduğu için bu URL'ler `validate:"url"`'den
  geçip R2'de 403 alır. Sonuç kullanılmadan önce yeniden çekilir ve `expiresAt`
  okunur.
- **Export düzeltilir.** `Export.tsx:46` `useExport(project)` çağırıyor ama
  `masterFileUrl` hiç geçilmiyor, `useExport.ts:62` bu yüzden sessizce dönüyor;
  `useMaster.ts:60-63` final sonucu projeye kaydetmiyor. Master sonucu kaydedilir
  ve export'a geçirilir.
- `applicationId` (`com.makesinger`) ile redirect scheme ve Keychain servisi
  (`com.makeasinger.*`) arasındaki tutarsızlık tek bir isimde karara bağlanır;
  redirect URI kaydı buna bağlıdır.

## Sökülenler

**core-service.** `zitadel-service/` klasörünün tamamı; `internal/handler/auth_handler.go`
(ForwardAuth'un k3s'te tüketicisi yok, tek gerçek tanımı `docker-stack.yml:92-95`);
`internal/middleware/gateway_auth.go` ve `GatewayConfig`; `internal/auth/legacy.go`;
`auth.go`'daki üç constructor ve ölü `GenerateToken`; `ZitadelConfig` + üç BindEnv +
`readSecret("ZITADEL_CLIENT_ID")` + `config.yaml` zitadel bloğu + `.env.example:31-34`;
kullanılmayan `jwt.expiration`.

`jwks_verifier.go` **kalır**, yönü değişir.

**Compose/Swarm.** `docker-compose.yml` satır 2 (include), 16, 38-40, ve tanımsız
`auth-forward@file` referansı (satır 56 — `traefik/dynamic/` boş, yani bu middleware
hiç tanımlı değil). `docker-stack.yml` satır 18, 52-54, 65, 92-95, 114, 183-317,
332, 340, 355-359.

**k3s-gitops.** `apps/makesinger/makesinger-zitadel.yaml`,
`apps/makesinger/zitadel-masterkey.sops.yaml`,
`apps/makesinger/zitadel-db-kimlik.sops.yaml`,
`apps/kiracilar/zitadel-db-kimlik.sops.yaml`, iki kustomization girdisi,
`infrastructure/policy/quotas.yaml:267-279` sayı ve yorum güncellemesi,
`apps/kiracilar/postgres.yaml:12,74` ve `yedek-yetkisi.yaml:19` yorumları,
`apps/makesinger/ingress.yaml:1-14` başlık yorumunun yeniden yazılması.

`apps/kiracilar/makesinger-db.yaml` **silinmez, devralınır**: `DatabaseRole` ve
`Database` adları Zitadel'den bizim auth veritabanımıza çevrilir. Envanterde
"tamamen silinebilir" görünüyordu; Redis'in kullanıcı saklayamaması bu kararı
tersine çevirdi. `databaseReclaimPolicy: retain` olduğu için Zitadel verisi
kümede kalacaktı ve elle temizlenmesi gerekecekti — düzenli devir bunu da çözer.

**Doküman.** `CLAUDE.md:11,57,68,89-91`; `README.md:3,32`;
`core-service/docs/API.md:16-37`.

## Sızmış sırlar

`zitadel-service/admin.pat` (IAM_OWNER) ve `zitadel-service/login-client.pat`
(IAM_LOGIN_CLIENT) repoda düz metin ve **git geçmişinde** duruyor; dosyayı silmek
onları geçmişten kaldırmaz. `zitadel-service/docker-compose.yaml:5` masterkey'i
de düz metin taşıyor. Zitadel örneği yok edileceği için token'lar ölecek, ama bu
bilinçli bir kapanış maddesidir.

Ayrıca fiili JWT secret her ortamda repoda yazılı varsayılan sabittir
(`config.go:174` `"change-me-in-production"`): `JWT_SECRET` ne
`docker-compose.yml`'de, ne `docker-stack.yml`'de, ne k3s-gitops'un hiçbir yerinde,
ne `.env.example`'da set edilmiş. Legacy yolun kaldırılması bu boşluğu da kapatır.

## Göç

Taşınacak hesap yoktur ve bu ölçüldü: backend'de kullanıcı kaydı yok, Redis'te
`user:*` anahtarı yok, mobil `auth.celalettindemir.dev`'e bakıyor ve o host k3s'te
hiç yayınlanmamış. İş kayıtları 24 saatlik. R2'deki nesneler deneme verisi.

Kullanıcı eylemi: `makesinger-auth.celalettindemir.dev` ve
`makesinger-api.celalettindemir.dev` için DNS kaydı. Sertifikaları cert-manager
halleder.

## Sevkiyat

Tek sevkiyat değil, sıralı fazlar. Sıra zorunludur: OP olmadan sahiplik anlamsız,
sahiplik olmadan API'yi dışa açmak güvenli değil.

**Faz 1 — OP ayağa kalkar.** Postgres veritabanı devralınır, imzalama anahtarı
üretilip sops'la saklanır, ikinci dinleyici ve IngressRoute kurulur, hosted giriş
ve kayıt sayfaları ile e-posta+şifre akışı çalışır. Mobil bu fazda hâlâ eski
yapılandırmadadır; kümede api dışa kapalı olduğu için kimse etkilenmez.

**Faz 2 — Federasyon.** Apple ve Google yukarı akış sağlayıcı olarak eklenir,
bağlama sayfası yazılır.

**Faz 3 — Sahiplik ve sertleştirme.** `Job.OwnerID`, 404 kuralı, R2 anahtar
düzeni, WebSocket bileti, rate limit'in fail-closed'a çevrilmesi, log seviyesi ve
Authorization maskeleme. R2 anahtar düzeni ve WebSocket bileti mobil için kırıcı
olduğundan, mobil güncellemesi **bu fazla aynı sevkiyatta** gider.

**Faz 4 — API dışa açılır.** `makesinger-api.celalettindemir.dev` için
IngressRoute eklenir. Bu, bugüne kadar bilinçli olarak yapılmayan şeydir
(`apps/makesinger/ingress.yaml:5-8` gerekçeyi yazıyor: `GATEWAY_ENABLED=true` ve
doğrulanmayan `X-User-Id`). Gateway modu kalktığı ve token'lar gerçekten
doğrulandığı için engel ortadan kalkar. Cloudflare "Flexible" modu gereği burada da
`websecure` + `tls` route'unun yanına `tls:` bloğu olmayan bir `-http` route gerekir.

**Faz 5 — Söküm.** Zitadel Deployment/Service/PVC ve secret'ları, ölü Go kodu,
compose/stack girdileri ve dokümanlar. Veritabanı devralındığı için silinmez.

## Test

Protokolün kendisi test edilmez, o kütüphanenin işidir. Testler bizim
kararlarımıza bakar:

- Sahibi olmayan `jobId` ile `status`/`result`/`cancel` → 404.
- Refresh token her kullanımda döner; kullanılmış bir token tekrar sunulunca
  ailenin tamamı iptal olur.
- Federasyonda e-posta çakışmasında otomatik bağlama **reddedilir**; bağlama
  ancak mevcut şifre doğrulanınca olur.
- `@privaterelay.appleid.com` adresi bağlama için güvenilir sayılmaz.
- userId boşken rate limit fail-closed.
- WebSocket bileti ikinci kullanımda reddedilir; başka kullanıcının işine
  düzenlenmiş bilet reddedilir.
- R2 anahtarı kullanıcı önekini taşır; `bucketFor` public/private ayrımı bozulmaz.
- Hesap silme, federe eşleşmeleri ve refresh ailelerini birlikte kaldırır.

## Bilinen sınır

Bu tasarım kimliği ve sahipliği getirir, ama mobil uygulamayı çalışır hale
getirmez. Ses kayıt ve oynatma katmanı bugün tamamen mock
(`hooks/useAudioRecorder.ts:27`, `hooks/useMultiTrackPlayer.ts:47`) ve hiçbir ses
kütüphanesi kurulu değil; vokal yükleme gerçek dosya göndermiyor. Bu ayrı bir
tasarımın konusudur ve bu işten bağımsızdır.

Projeler cihazdaki MMKV'de kalır. Hesap gelmesi bulut senkronu getirmez; cihaz
değişince projeler gelmez. Her iki PRD de bulut senkronunu kapsam dışı ilan
etmiştir (`make-singer-doc/prd.md`, `make-singer-mobile/docs/PRD.md:156-158`).
