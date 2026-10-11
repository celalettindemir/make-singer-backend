# Mobil gorevi: render stem seviyeleri (Plan 4c-1'e Task 11 olarak)

Kaynak: backend v1.4.0 (make-singer-backend `main`, 2026-10-11). Sozlesme `core-service/docs/API.md` "Render start" ve "Stem modes" bolumlerinde.

## Backend sozlesmesi (canlida)

- `POST /api/render/start` govdesine iki opsiyonel alan:
  - `stemMode`: `"mix"` | `"focus"` | `"stems"`. Gonderilmezse `"mix"`.
  - `focusInstrument`: yalnizca `stemMode: "focus"` iken ZORUNLU ve `arrangement.instruments` icinde olmali. `mix`/`stems` ile gonderilirse 400.
- Seviyeler (Suno kredisi, kullaniciya "maliyet/kalite" olarak gosterilecek):
  | Mod | Kredi | Sonuc stem'leri |
  |---|---|---|
  | `mix` | 12 | 1 stem, `instrument: "mix"` (tam parca) |
  | `focus` | 32 | 2 stem: secilen enstruman + `instrument: "rest"` (o enstruman cikarilmis parca) |
  | `stems` | 62 | Suno'nun sabit seti, en fazla 10 enstruman stem'i (drums, bass, guitar, piano, percussion, strings, synth, fx, brass); aranjmandan bagimsiz |
- `GET /api/render/result/:jobId` yanitinda yeni alan `stemMode` (ayni uc deger).
- Hata: tutarsiz `stemMode`/`focusInstrument` → 400 `VALIDATION_ERROR`.

## Mobilde yapilacaklar

1. `src/types/schemas/render.ts`: `stemSchema.instrument` artik `instrumentEnum` ile sinirli olamaz; `mix` ve `rest` cikis degerlerini kabul etmeli (or. `z.union([instrumentEnum, z.enum(['mix', 'rest'])])` ya da ayri `stemInstrumentEnum`). `renderSchema`'ya `stemMode: z.enum(['mix', 'focus', 'stems'])` (eski kayitlar icin `.optional()`).
2. `src/hooks/domain/render/renderService.ts` `RenderStartInput`'a `stemMode` ve `focusInstrument?`; `useRenderJob.startRender` bunlari projedeki secimden gecirsin.
3. Proje modeline secim: `stemMode` (varsayilan `mix`) ve `focusInstrument?`. Kullanici render oncesi (Instruments adimi ya da render baslatma noktasi) uc seviyeden birini secer; `focus` secilince aranjmandaki enstrumanlardan biri secilir. Her seviyenin aciklamasi: ne verdigi (tek kanal / enstruman + geri kalan / enstruman basina kanal). Metinler i18n (en/tr/fr).
4. Mixer: stem listesi sonuctaki stem'lerden kurulur; `mix` ve `rest` icin etiket/ikon (or. "Full mix", "Rest of the track" / "Tam parca", "Geri kalan"). `mix` modunda tek fader; `focus`'ta iki.
5. Testler: schema `mix`/`rest` stem'ini kabul eder; `startRender` istegi `stemMode`/`focusInstrument` tasir; `focus` enstrumansiz secilemez (UI); Mixer `mix`/`rest` etiketlerini gosterir.

## Bu sirada fark edilen backend uyumsuzlugu (ayni goreve eklenebilir)

- `src/types/schemas/arrangementPlan.ts` `grooveEnum` `'half-time'` kullaniyor; backend `oneof=straight swing half_time` bekliyor. `half-time` secilirse render istegi 400 alir. Gonderirken `half_time`'a cevrilmeli (ya da enum degeri degismeli).

## Ayrica bekleyen (Faz 3 mobil, bu gorevin parcasi degil)

- WebSocket: `POST /api/ws/ticket` `{jobId}` → `{ticket, expiresAt}`, sonra `/ws/jobs/:jobId?ticket=`. Bilet olmadan baglanti 401.
- `DELETE /api/upload/vocal/:takeId?projectId=&sectionId=` (iki sorgu parametresi zorunlu).
- OIDC yapilandirmasi hala Zitadel'e bakiyor (`src/hooks/domain/auth/config.ts`).
