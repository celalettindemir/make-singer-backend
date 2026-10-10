# Suno entegrasyonunu gercek API'ye baglama

Tarih: 2026-10-10. Durum: taslak, kullanici onayi bekliyor.

## Sorun

`internal/client/suno_client.go` var olmayan bir API'ye yaziyor. Saglayici
(`api.sunoapi.org`) ile koddaki sozlesme hicbir noktada uyusmuyor:

| | Kod | Saglayici |
|---|---|---|
| Uretim | `POST /v1/music/generate` | `POST /api/v1/generate` |
| Uretim durumu | `GET /v1/music/status/{id}` | `GET /api/v1/generate/record-info?taskId=` |
| Stem bolme | `POST /v1/audio/split-stems` + `audio_url` | `POST /api/v1/vocal-removal/generate` + `taskId`, `audioId`, `type` |
| Stem durumu | `GET /v1/audio/split-stems/{id}` | `GET /api/v1/vocal-removal/record-info?taskId=` |
| Zorunlu alan | yok | `customMode`, `instrumental`, `model`, `callBackUrl` |
| Yanit zarfi | duz (`task_id`) | `{code, msg, data}` (`data.taskId`) |
| Bitti | `completed` / `success` | `SUCCESS`; hata: `*_FAILED`, `CALLBACK_EXCEPTION`, `SENSITIVE_WORD_ERROR` |

Ayrica:

- `uploadStems` stem'leri indirip R2'ye yuklemiyor; yalnizca anahtar
  uretiyor. Istemciye giden adresler bos nesnelere isaret ediyor.
- Render gorevi `MaxRetry(3)` ile kuyruga giriyor. Ucretli adimdan sonra
  dusen bir hata Suno'yu bastan cagirir: bir render 12 (uretim) + 50
  (split_stem) = 62 kredi, dort deneme 248 kredi.
- `buildMusicPrompt` genre'yi iki kez yaziyor, vibes `%v` ile `[a b]`
  olarak gidiyor, `Arrangement` (enstrumanlar, yogunluk, groove) hic
  kullanilmiyor.

2026-10-10 spike'i dogru sozlesmeyle bir uretimi bastan sona calistirdi
(`/api/v1/generate`, `customMode: true`, `model: V6`, sahte `callBackUrl`
ile `record-info` poll'u; ~60 sn, 12 kredi, 2 parca).

## Kapsam

Icinde: Suno client'inin yeniden yazimi, render worker'in Suno yolu,
stem'lerin R2'ye gercekten yuklenmesi, ucretli adim sonrasi tekrar
denemenin kesilmesi, prompt duzeltmesi, config.

Disinda (YAGNI): vokalli sarki uretimi (urun bugun instrumental uretiyor),
callback ucu (API disa kapali, poll yeterli), gercek waveform hesabi
(mevcut sahte `generateWaveform` kalir), export'taki `.wav` dosya adlari.

## Tasarim

### Config

`SunoConfig`'e eklenir: `Model` (`SUNO_MODEL`, varsayilan `V6`),
`CallbackURL` (`SUNO_CALLBACK_URL`, varsayilan
`https://makesinger-api.celalettindemir.dev/suno/callback` -- saglayici
zorunlu tutuyor, biz poll ediyoruz; uc yok ve gerekmiyor). Stem tipi
sabit `split_stem` (kullanici karari, 2026-10-10); `separate_vocal`
secenegi eklenmez (YAGNI).

### Client (`internal/client/suno_client.go`)

Arayuz:

```go
type MusicGenerator interface {
    GenerateMusic(ctx, *GenerateMusicRequest) (taskID string, err error)
    WaitMusic(ctx, taskID string, interval, maxWait time.Duration) (*Track, error)
    SplitStems(ctx, taskID, audioID string) (stemTaskID string, err error)
    WaitStems(ctx, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error)
    IsConfigured() bool
}
```

- `GenerateMusicRequest{Style, Title string; Instrumental bool}`. Client
  `customMode: true`, `model`, `callBackUrl` alanlarini config'den ekler.
- Tum yanitlar `{code, msg, data}` zarfindan cozulur; `code != 200` hata.
- `WaitMusic` `SUCCESS`'te ilk parcayi (`data.response.sunoData[0]`) doner:
  `Track{ID, AudioURL, Duration}`. Saglayici iki parca uretir; ikincisi
  bugun kullanilmaz. `*_FAILED`, `CALLBACK_EXCEPTION`,
  `SENSITIVE_WORD_ERROR` aninda hata (`errorMessage` ile). Bilinmeyen
  durum beklemeye devam eder.
- `WaitStems` `successFlag == SUCCESS`'te camelCase stem alanlarini
  `Instrument` enum'ina esler: `drumsUrl`->drums, `bassUrl`->bass,
  `guitarUrl`->guitar, `keyboardUrl`->piano, `percussionUrl`->percussion,
  `stringsUrl`->strings, `synthUrl`->synth, `fxUrl`->fx, `brassUrl`->brass,
  `woodwindsUrl`->woodwinds. `vocalUrl`, `backingVocalsUrl` atlanir
  (instrumental uretimde vokal yok; mobil enum'u vokali tanimaz).
  Bos URL'ler atlanir.
- Eski `SeparateVocals`, `GetSeparationStatus`, `GetMusicStatus`,
  `GetStemSplitStatus` ve tipleri silinir; cagiran yok.

### Worker (`internal/worker/render_worker.go`)

`processWithSuno` akisi:

1. Style metni uretilir (asagida).
2. `GenerateMusic` -> `WaitMusic` (5 sn aralik, 10 dk tavan).
3. `SplitStems(taskID, track.ID)` -> `WaitStems` (5 sn, 5 dk).
4. Her stem icin: URL indirilir (30 sn zaman asimi, 50 MB tavan), R2'ye
   `stems/{projectID}/{stemID}.mp3` anahtariyla `audio/mpeg` olarak
   yuklenir. Sure olarak parcanin suresi kullanilir.
5. Hic stem kalmazsa is basarisiz (mobil en az 1 stem bekler).

Tekrar deneme: 2. adimdaki `GenerateMusic` basarili olduktan sonra donen
her hata `asynq.SkipRetry` ile sarilir. Boylece ag hatasi gibi ucretsiz bir
hata ilk istekte tekrar denenir, ama kredi harcandiktan sonra is bir daha
baslamaz. Is `failed` olur ve hata mesaji WebSocket'e gider.

R2 yapilandirilmamissa (`r2Client == nil`) Suno yolu baslamadan is
basarisiz olur: kredi harcayip sonucu atmak yerine.

Worker `*client.SunoClient` yerine `client.MusicGenerator` arayuzunu ve
indirme icin bir `*http.Client` alir; testler sahte uygulama verir.

### Style metni

```
{genre}, instrumental, {bpm} BPM, {tonic} {scale}, {vibe1}, {vibe2},
{density} arrangement, {groove} groove, featuring {inst1}, {inst2}
```

Bos alanlar yazilmaz: BPM veya key degeri yoksa (`auto`) o parca
style'a girmez ve Suno kendisi secer. Sonuc kaydindaki BPM/key varsayimi
(120, C major) `generateResult`'ta degismeden kalir; gercek tempo/key
tespiti kapsam disi. Title: `{genre} instrumental`. Metin 1000 karakterde
kesilir (V6 `style` siniri).

## Hata durumlari

| Durum | Sonuc |
|---|---|
| Uretim istegi reddedildi (`code != 200`, kredi yok = 429) | Hata, asynq tekrar dener (ucret yok) |
| Uretim `*_FAILED` / `SENSITIVE_WORD_ERROR` | Is basarisiz, tekrar yok |
| Poll zaman asimi | Is basarisiz, tekrar yok |
| Stem istegi / poll hatasi | Is basarisiz, tekrar yok |
| Indirme / R2 yukleme hatasi | Is basarisiz, tekrar yok |
| Context iptali (render cancel) | Mevcut davranis: is iptal |

## Test

- Client: `httptest` ile her uc icin yol, yontem, `Authorization`, govde
  alanlari (`customMode`, `model`, `callBackUrl`, `taskId`, `audioId`,
  `type`); zarf cozumleme; her durum degeri; stem eslemesi ve atlananlar.
- Worker: sahte `MusicGenerator` + sahte `StorageClient` + `httptest`
  ses sunucusu ile mutlu yol (R2'ye dogru anahtar ve icerik tipi), ucretli
  adim sonrasi hatada `asynq.SkipRetry`, ucretli adim oncesi hatada
  tekrar denenebilir hata, sifir stem'de basarisizlik, R2 yokken Suno'nun
  hic cagrilmamasi.
- Style metni: tablo testi.
- Canli dogrulama yalnizca kullanici onayiyla: tek render 62 kredi,
  hesapta 38 kredi var.
