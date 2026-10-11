# Render Stem Seviyeleri Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Kullanicinin render basina uc stem seviyesinden birini secmesi: `mix` (12 kredi), `focus` (32 kredi), `stems` (62 kredi).

**Architecture:** `RenderStartRequest`'e `stemMode` ve `focusInstrument` eklenir. Istek `RenderStartRequest.StemAyarlari()` ile normallestirilir: varsayilan `mix`, `focus` icin enstruman zorunlu ve aranjmanda olmali. Mod is payload'ina ve sonuca yazilir. Worker, uretimden sonra moda gore dallanir:
- `mix`: uretilen parcanin kendisini tek stem olarak yukler; ayirma cagrisi yok.
- `focus`: `split_stem_advanced` cagirir ve iki stem yukler: secilen enstruman (`extract`) ve geri kalan (`remove`).
- `stems`: bugunku `split_stem` yolu.

**Tech Stack:** Go 1.25, Fiber v2, go-playground/validator, Asynq, `net/http/httptest`.

**Spec:** Bu oturumda onaylanan tasarim (spec dosyasi yok; kararlar asagida "Kararlar" bolumunde, kanit `~/.claude-mba/projects/-Users-cos-Documents-make-singer/memory/ai-providers-deploy.md` "Live findings 2026-10-11").

## Global Constraints

- Tum komutlar `core-service/` dizininde calisir. Birim testler: `go test ./internal/...`. CI'da `redis:7-alpine` var; yerelde Redis localhost:6379'da acik.
- Lint: CI `golangci-lint v2.14.0` kullanir ve Docker adimini o geciktirir. Commit oncesi `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` → `0 issues.`
- `go vet ./...` temiz, `gofmt -l internal cmd e2e` bos.
- Kod yorumlari ve test adlari Turkce, **aksan isareti yok** (c g i o s u).
- **Canli Suno cagrisi YASAK.** `e2e/` testleri calistirilmaz; ozellikle `TestRenderFullPipeline_RealSuno` ucretlidir. Tum testler `httptest` ve sahte uygulamalarla.
- Mod degerleri tam olarak `mix`, `focus`, `stems`; varsayilan `mix`.
- Yeni cikis enstrumanlari: `mix` (tam parca), `rest` (focus modunda secilen enstruman cikarilmis parca). Istek dogrulamasindaki aranjman enstruman listesi DEGISMEZ; `mix`/`rest` istemciden kabul edilmez.
- Suno `stemName` eslemesi (saglayici enum'u, harfi harfine): drums→"Drum Kit", bass→"Bass", piano→"Piano", guitar→"Guitar", synth→"Synth", strings→"String Section", brass→"Brass Section", woodwinds→"Woodwinds", percussion→"Percussion", pads→"Synth Pad", lead→"Synth Lead", fx→"Sound Effects".
- `split_stem_advanced` yaniti: `data.successFlag`, `data.response.originData[]`, her eleman `{"extract": {"audio_url", "duration", "id", "stem_type_group_name"}, "remove": {...}}`. Ilk eleman (`originData[0]`) kullanilir. Adresler bosluk icerebilir.
- Ucret sonrasi kurallar aynen gecerli: `GenerateMusic` basarili olduktan sonraki her hata `asynq.SkipRetry` ile sarilir (mevcut `ucretli` yardimcisi ve panic `recover`'i).
- Stem anahtari `client.KullaniciAnahtari("stems", ownerID, projectID, stemID+".mp3")`, icerik tipi `audio/mpeg` (mevcut `uploadStems`).
- Commit mesajlari `tip(alan): ozet`, sonunda tam olarak `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch: `stem-seviyeleri`. Push YOK.

## Kararlar

- Uc seviye kullanici secimidir; "ayricalik" icin bugun plan/odeme kurali yok, tek denetim noktasi `StemAyarlari()` (ileride plan kurali buraya eklenir).
- `focus` tek enstrumanla sinirli: iki enstruman 52 kredi eder, `stems`'e (62) cok yakin.
- `mix` modunda `separate_vocal` KULLANILMAZ: uretim zaten instrumental, parcanin kendisi enstrumantaldir.
- `focusInstrument` aranjmanda olmali: kullanici parcada olmayan bir enstrumani ayirmaya 20 kredi odemesin.
- `stemMode` `mix` ya da `stems` iken `focusInstrument` gonderilirse 400 (sessizce yok saymak yanlis bir beklenti uretir).
- Mod sonuc kaydina ve yanita (`stemMode`) yazilir; mobil mikser kanallari buna gore kurar.

## Dosya Yapisi

| Dosya | Sorumluluk |
|---|---|
| `internal/model/enums.go` (degisir) | `StemMode` tipi ve sabitleri; `InstrumentMix`, `InstrumentRest` |
| `internal/model/render.go` (degisir) | Istek alanlari, `StemAyarlari()`, sonuc alanlari |
| `internal/model/job.go` (degisir) | `RenderJobPayload.StemMode`, `FocusInstrument` |
| `internal/model/stem_modu_test.go` (yeni) | `StemAyarlari` tablo testi |
| `internal/service/render_service.go` (degisir) | Payload'a mod, `BuildResult` kopyalar |
| `internal/handler/render.go` (degisir) | `StemAyarlari` hatasi → 400 |
| `internal/client/suno_client.go` (degisir) | `IsolateStem`, `WaitIsolated`, `SunoStemAdi`, `IsolatedStem` |
| `internal/client/suno_client_test.go` (degisir) | Yeni uclarin sozlesme testleri |
| `internal/worker/render_worker.go` (degisir) | Moda gore dallanma (Suno + mock) |
| `internal/worker/render_worker_test.go`, `mock_adres_test.go` (degisir) | Mod testleri |
| `CLAUDE.md`, `core-service/docs/API.md`, swagger | Dokuman |

---

### Task 1: Model, istek dogrulamasi, servis ve handler

**Files:**
- Modify: `internal/model/enums.go`, `internal/model/render.go`, `internal/model/job.go`
- Create: `internal/model/stem_modu_test.go`
- Modify: `internal/service/render_service.go` (`StartRender`, `BuildResult`)
- Modify: `internal/handler/render.go` (`Start`)
- Test: `internal/service/sahiplik_canli_test.go` (yeni test eklenir), `internal/service/sonuc_tazeleme_test.go` (yeni test eklenir)

**Interfaces:**
- Produces:
  - `type StemMode string`; `StemModeMix = "mix"`, `StemModeFocus = "focus"`, `StemModeStems = "stems"`
  - `InstrumentMix Instrument = "mix"`, `InstrumentRest Instrument = "rest"`
  - `RenderStartRequest.StemMode StemMode` (`json:"stemMode"`), `RenderStartRequest.FocusInstrument *Instrument` (`json:"focusInstrument"`)
  - `var ErrGecersizStemAyari error`; `func (r *RenderStartRequest) StemAyarlari() (StemMode, *Instrument, error)`
  - `RenderJobPayload.StemMode StemMode` (`json:"stemMode"`), `RenderJobPayload.FocusInstrument *Instrument` (`json:"focusInstrument,omitempty"`)
  - `RenderResultRecord.StemMode`, `RenderResultResponse.StemMode` (`json:"stemMode"`)
  - `RenderService.StartRender` payload'a normallestirilmis modu yazar; hata `ErrGecersizStemAyari` sarar.

- [ ] **Step 1: Write the failing model test**

`internal/model/stem_modu_test.go`:

```go
package model

import (
	"errors"
	"testing"
)

func enstruman(i Instrument) *Instrument { return &i }

func TestStemAyarlari(t *testing.T) {
	aranjman := Arrangement{Instruments: []Instrument{InstrumentDrums, InstrumentBass}}
	durumlar := []struct {
		ad        string
		mod       StemMode
		odak      *Instrument
		beklenen  StemMode
		odakBekle *Instrument
		hata      bool
	}{
		{"bos mod varsayilan mix", "", nil, StemModeMix, nil, false},
		{"mix", StemModeMix, nil, StemModeMix, nil, false},
		{"stems", StemModeStems, nil, StemModeStems, nil, false},
		{"focus aranjmandaki enstrumanla", StemModeFocus, enstruman(InstrumentDrums), StemModeFocus, enstruman(InstrumentDrums), false},
		{"focus enstrumansiz", StemModeFocus, nil, "", nil, true},
		{"focus aranjmanda olmayan enstruman", StemModeFocus, enstruman(InstrumentPiano), "", nil, true},
		{"mix ile focusInstrument", StemModeMix, enstruman(InstrumentDrums), "", nil, true},
		{"stems ile focusInstrument", StemModeStems, enstruman(InstrumentDrums), "", nil, true},
	}
	for _, d := range durumlar {
		t.Run(d.ad, func(t *testing.T) {
			r := &RenderStartRequest{Arrangement: aranjman, StemMode: d.mod, FocusInstrument: d.odak}
			mod, odak, err := r.StemAyarlari()
			if d.hata {
				if !errors.Is(err, ErrGecersizStemAyari) {
					t.Fatalf("ErrGecersizStemAyari bekleniyordu, geldi: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("hata beklenmiyordu: %v", err)
			}
			if mod != d.beklenen {
				t.Errorf("mod = %q, beklenen %q", mod, d.beklenen)
			}
			if (odak == nil) != (d.odakBekle == nil) || (odak != nil && *odak != *d.odakBekle) {
				t.Errorf("odak = %v, beklenen %v", odak, d.odakBekle)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/model/ -run StemAyarlari -v`
Expected: FAIL — `undefined: StemMode`

- [ ] **Step 3: Model**

`internal/model/enums.go` — Instruments const blogunun SONUNA (`InstrumentFX` satirindan sonra):

```go
	// Yalnizca render CIKTISINDA gorunur; istekte kabul edilmez.
	InstrumentMix  Instrument = "mix"  // tam parca (mix modu)
	InstrumentRest Instrument = "rest" // secilen enstruman cikarilmis parca (focus modu)
```

Dosyanin sonuna:

```go
// StemMode, render'in stem seviyesi. Kullanici secer; maliyet Suno
// kredisidir: mix 12, focus 32 (12 + split_stem_advanced 20), stems 62
// (12 + split_stem 50).
type StemMode string

const (
	StemModeMix   StemMode = "mix"
	StemModeFocus StemMode = "focus"
	StemModeStems StemMode = "stems"
)
```

`internal/model/render.go` — `RenderStartRequest`'e alanlar:

```go
type RenderStartRequest struct {
	ProjectID       string      `json:"projectId" validate:"required,uuid"`
	Brief           Brief       `json:"brief" validate:"required"`
	Arrangement     Arrangement `json:"arrangement" validate:"required"`
	StemMode        StemMode    `json:"stemMode" validate:"omitempty,oneof=mix focus stems"`
	FocusInstrument *Instrument `json:"focusInstrument" validate:"omitempty,oneof=drums bass piano guitar synth strings brass woodwinds percussion pads lead fx"`
}
```

Hemen altina (`"errors"` ve `"fmt"` importlarini ekle):

```go
// ErrGecersizStemAyari: stemMode/focusInstrument birlikte tutarsiz.
var ErrGecersizStemAyari = errors.New("gecersiz stem ayari")

// StemAyarlari, istegin stem modunu normallestirir: bos mod mix'tir (ucuz
// taraf; eski istemci kazara 62 kredi harcatamaz). focus icin enstruman
// zorunludur ve aranjmanda olmalidir; diger modlarda enstruman verilemez.
// Ileride plan bazli kisit (or. stems yalnizca belirli planda) buraya eklenir.
func (r *RenderStartRequest) StemAyarlari() (StemMode, *Instrument, error) {
	mod := r.StemMode
	if mod == "" {
		mod = StemModeMix
	}
	if mod != StemModeFocus {
		if r.FocusInstrument != nil {
			return "", nil, fmt.Errorf("%w: focusInstrument yalnizca focus modunda", ErrGecersizStemAyari)
		}
		return mod, nil, nil
	}
	if r.FocusInstrument == nil {
		return "", nil, fmt.Errorf("%w: focus modu focusInstrument ister", ErrGecersizStemAyari)
	}
	for _, i := range r.Arrangement.Instruments {
		if i == *r.FocusInstrument {
			odak := *r.FocusInstrument
			return mod, &odak, nil
		}
	}
	return "", nil, fmt.Errorf("%w: focusInstrument aranjmanda yok", ErrGecersizStemAyari)
}
```

`RenderResultResponse` ve `RenderResultRecord` struct'larina `CreatedAt` satirindan sonra:

```go
	StemMode  StemMode           `json:"stemMode"`
```

(`RenderResultResponse`'ta hizalama `StemMode  StemMode     `json:"stemMode"``; gofmt duzeltir.)

`internal/model/job.go` — `RenderJobPayload`'a `Arrangement` satirindan sonra:

```go
	StemMode        StemMode    `json:"stemMode"`
	FocusInstrument *Instrument `json:"focusInstrument,omitempty"`
```

Run: `go test ./internal/model/ -v` → PASS

- [ ] **Step 4: Failing service tests**

`internal/service/sahiplik_canli_test.go` sonuna ekle (mevcut `canliRedis`, `bekleyenGorevPayload`, `canliRedisAdresi` kullanilir; `"errors"`, `uuid`, `asynq`, `model` zaten import edili):

```go
func TestRenderService_StartRenderStemModunuYazar(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	davul := model.InstrumentDrums
	req := &model.RenderStartRequest{
		ProjectID:       uuid.New().String(),
		Arrangement:     model.Arrangement{Instruments: []model.Instrument{model.InstrumentDrums}},
		StemMode:        model.StemModeFocus,
		FocusInstrument: &davul,
	}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	var p model.RenderJobPayload
	bekleyenGorevPayload(t, "render", &p)
	if p.StemMode != model.StemModeFocus || p.FocusInstrument == nil || *p.FocusInstrument != model.InstrumentDrums {
		t.Errorf("payload stem ayari = %q %v", p.StemMode, p.FocusInstrument)
	}
}

func TestRenderService_StartRenderVarsayilanMix(t *testing.T) {
	rdb := canliRedis(t)
	ac := asynq.NewClient(asynq.RedisClientOpt{Addr: canliRedisAdresi, DB: 13})
	defer ac.Close()
	s := NewRenderService(rdb, ac, nil, testR2Cfg())

	req := &model.RenderStartRequest{ProjectID: uuid.New().String()}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); err != nil {
		t.Fatalf("StartRender: %v", err)
	}
	var p model.RenderJobPayload
	bekleyenGorevPayload(t, "render", &p)
	if p.StemMode != model.StemModeMix {
		t.Errorf("varsayilan mod = %q, mix bekleniyordu", p.StemMode)
	}
}

func TestRenderService_GecersizStemAyariIsYaratmaz(t *testing.T) {
	// Redis nil: dogrulama is kaydindan ONCE olmali, dokunulursa panic.
	s := NewRenderService(nil, nil, nil, testR2Cfg())
	req := &model.RenderStartRequest{ProjectID: uuid.New().String(), StemMode: model.StemModeFocus}
	if _, err := s.StartRender(context.Background(), "kullanici-a", req); !errors.Is(err, model.ErrGecersizStemAyari) {
		t.Fatalf("ErrGecersizStemAyari bekleniyordu, geldi: %v", err)
	}
}
```

`internal/service/sonuc_tazeleme_test.go` sonuna ekle:

```go
func TestRenderService_BuildResultStemModunuTasir(t *testing.T) {
	s := NewRenderService(nil, nil, &sahteDepo{}, testR2Cfg())
	record := &model.RenderResultRecord{
		StemMode: model.StemModeMix,
		Stems:    []model.StemResultRecord{{ID: "s1", Instrument: model.InstrumentMix, FileKey: "stems/kullanici-a/p1/s1.mp3"}},
	}
	resp, err := s.BuildResult(context.Background(), record)
	if err != nil {
		t.Fatalf("BuildResult: %v", err)
	}
	if resp.StemMode != model.StemModeMix {
		t.Errorf("yanit stemMode = %q", resp.StemMode)
	}
}
```

Run: `go test ./internal/service/ -run 'StemModu|VarsayilanMix|GecersizStemAyari|StemModunuTasir' -v`
Expected: FAIL (payload'da mod yok / ErrGecersizStemAyari donmuyor / yanit modu bos)

- [ ] **Step 5: Servis**

`internal/service/render_service.go` `StartRender`'in EN BASINA (jobID uretmeden once):

```go
	// Stem ayari is kaydindan ONCE dogrulanir: tutarsiz istek is yaratmaz.
	stemModu, odak, err := req.StemAyarlari()
	if err != nil {
		return nil, err
	}
```

`payload` literaline:

```go
		StemMode:        stemModu,
		FocusInstrument: odak,
```

(Fonksiyonda sonraki `payloadBytes, err := json.Marshal(payload)` satiri artik `err` tanimli oldugu icin `payloadBytes, err := ` olarak derlenir; derlenmezse `payloadBytes, err = ` yap.)

`BuildResult`'in dondurdugu `RenderResultResponse` literaline `StemMode: record.StemMode,` ekle.

- [ ] **Step 6: Handler**

`internal/handler/render.go` `Start`'ta `h.service.StartRender` hatasi:

```go
	result, err := h.service.StartRender(c.Context(), userID, &req)
	if err != nil {
		if errors.Is(err, model.ErrGecersizStemAyari) {
			return response.ValidationError(c, err.Error(), nil)
		}
		return response.ServiceError(c, err.Error())
	}
```

(`errors` zaten import edili; `model` import edili.) `ErrGecersizStemAyari` mesajlari deger tasimaz, istemciye gosterilebilir.

- [ ] **Step 7: Run all**

Run: `go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd e2e`
Expected: PASS, gofmt bos.

- [ ] **Step 8: Commit**

```bash
git add internal/model internal/service internal/handler
git commit -m "feat(render): stem modu (mix/focus/stems) istek, payload ve sonucta"
```

---

### Task 2: Suno client — split_stem_advanced

**Files:**
- Modify: `internal/client/suno_client.go`
- Test: `internal/client/suno_client_test.go`

**Interfaces:**
- Consumes: Task 1 `model.Instrument` sabitleri.
- Produces:
  - `func SunoStemAdi(i model.Instrument) (string, bool)`
  - `type IsolatedStem struct { ExtractURL, RemoveURL string; Duration float64 }`
  - `MusicGenerator` arayuzune: `IsolateStem(ctx context.Context, taskID, audioID, stemName string) (string, error)` ve `WaitIsolated(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) (*IsolatedStem, error)`
  - `*SunoClient` her ikisini uygular.

- [ ] **Step 1: Write the failing tests**

`internal/client/suno_client_test.go`'daki mevcut yardimcilari once oku (`TestSplitStems_SaglayiciSozlesmesi` ve `TestWaitStems_EnstrumanlariEslerVokaliAtlar` sahte sunucuyu ve client'i nasil kuruyorsa ayni kalibi kullan). Sonra dosyaya ekle — asagidaki kod `httptest` ile kendi sunucusunu kurar, mevcut yardimcilara bagli degildir:

```go
func advancedClient(t *testing.T, h http.HandlerFunc) *SunoClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewSunoClient(&config.SunoConfig{APIKey: "k", BaseURL: srv.URL, Model: "V6", CallbackURL: "https://cb.test/suno"})
}

func TestIsolateStem_SaglayiciSozlesmesi(t *testing.T) {
	var govde map[string]any
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vocal-removal/generate" {
			t.Errorf("istek = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&govde)
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"taskId":"a1"}}`))
	})

	id, err := c.IsolateStem(context.Background(), "t1", "m1", "Drum Kit")
	if err != nil || id != "a1" {
		t.Fatalf("IsolateStem = %q, %v", id, err)
	}
	beklenen := map[string]any{"taskId": "t1", "audioId": "m1", "type": "split_stem_advanced", "stemName": "Drum Kit", "callBackUrl": "https://cb.test/suno"}
	for k, v := range beklenen {
		if govde[k] != v {
			t.Errorf("govde[%s] = %v, beklenen %v", k, govde[k], v)
		}
	}
}

// advancedYanit, 2026-10-11 canli kesifteki gercek yanitin kisaltilmis hali.
const advancedYanit = `{"code":200,"msg":"success","data":{
 "taskId":"a1","successFlag":"SUCCESS","errorMessage":null,
 "response":{"originUrl":null,"drumsUrl":null,"originData":[
  {"extract":{"duration":141.48,"audio_url":"https://tempfile.test/r/x_Drum Kit.mp3","stem_type_group_name":"Drum Kit","id":"e1"},
   "remove":{"duration":141.48,"audio_url":"https://tempfile.test/r/y_Drum Kit.mp3","stem_type_group_name":"Drum Kit","id":"r1"}},
  {"extract":{"duration":141.48,"audio_url":"https://tempfile.test/r/ikinci-e.mp3","id":"e2"},
   "remove":{"duration":141.48,"audio_url":"https://tempfile.test/r/ikinci-r.mp3","id":"r2"}}
 ]}}}`

func TestWaitIsolated_IlkVaryantinExtractVeRemoveAdresleri(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vocal-removal/record-info" || r.URL.Query().Get("taskId") != "a1" {
			t.Errorf("istek = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(advancedYanit))
	})

	s, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitIsolated: %v", err)
	}
	if s.ExtractURL != "https://tempfile.test/r/x_Drum Kit.mp3" || s.RemoveURL != "https://tempfile.test/r/y_Drum Kit.mp3" || s.Duration != 141.48 {
		t.Errorf("sonuc = %+v", s)
	}
}

func TestWaitIsolated_BosOriginDataHata(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"SUCCESS","response":{"originData":[]}}}`))
	})
	if _, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second); err == nil {
		t.Fatal("bos originData icin hata bekleniyordu")
	}
}

func TestWaitIsolated_EksikAdresHata(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"SUCCESS","response":{"originData":[{"extract":{"audio_url":"https://x.test/e.mp3"},"remove":{"audio_url":""}}]}}}`))
	})
	if _, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second); err == nil {
		t.Fatal("bos remove adresi icin hata bekleniyordu")
	}
}

func TestWaitIsolated_HataBayragi(t *testing.T) {
	c := advancedClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"successFlag":"GENERATE_AUDIO_FAILED","errorMessage":"katalog eslesmesi"}}`))
	})
	_, err := c.WaitIsolated(context.Background(), "a1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "katalog eslesmesi") {
		t.Fatalf("hata mesaji tasinmali, geldi: %v", err)
	}
}

func TestSunoStemAdi(t *testing.T) {
	beklenen := map[model.Instrument]string{
		model.InstrumentDrums: "Drum Kit", model.InstrumentBass: "Bass", model.InstrumentPiano: "Piano",
		model.InstrumentGuitar: "Guitar", model.InstrumentSynth: "Synth", model.InstrumentStrings: "String Section",
		model.InstrumentBrass: "Brass Section", model.InstrumentWoodwinds: "Woodwinds", model.InstrumentPercussion: "Percussion",
		model.InstrumentPads: "Synth Pad", model.InstrumentLead: "Synth Lead", model.InstrumentFX: "Sound Effects",
	}
	for i, ad := range beklenen {
		if got, ok := SunoStemAdi(i); !ok || got != ad {
			t.Errorf("SunoStemAdi(%s) = %q, %v; beklenen %q", i, got, ok, ad)
		}
	}
	for _, i := range []model.Instrument{model.InstrumentMix, model.InstrumentRest, "kazoo"} {
		if _, ok := SunoStemAdi(i); ok {
			t.Errorf("SunoStemAdi(%s) eslesmemeliydi", i)
		}
	}
}
```

Gerekli importlar (eksikse ekle): `context`, `encoding/json`, `net/http`, `net/http/httptest`, `strings`, `testing`, `time`, `config`, `model`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/client/ -run 'IsolateStem|WaitIsolated|SunoStemAdi' -v`
Expected: FAIL — `c.IsolateStem undefined`

- [ ] **Step 3: Implementation**

`internal/client/suno_client.go`:

`MusicGenerator` arayuzune `WaitStems` satirindan sonra:

```go
	IsolateStem(ctx context.Context, taskID, audioID, stemName string) (string, error)
	WaitIsolated(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) (*IsolatedStem, error)
```

`Stem` tipinden sonra:

```go
// IsolatedStem, split_stem_advanced sonucu: secilen enstruman (Extract) ve
// o enstruman cikarilmis parca (Remove). Adresler sureli ve bosluk
// icerebilir; net/http istekte kodlar.
type IsolatedStem struct {
	ExtractURL string
	RemoveURL  string
	Duration   float64
}

// sunoStemAdlari, bizim enstrumanlarimizin saglayicinin stemName enum'undaki
// karsiliklari (harfi harfine; buyuk/kucuk harf ve bosluk onemli).
var sunoStemAdlari = map[model.Instrument]string{
	model.InstrumentDrums:      "Drum Kit",
	model.InstrumentBass:       "Bass",
	model.InstrumentPiano:      "Piano",
	model.InstrumentGuitar:     "Guitar",
	model.InstrumentSynth:      "Synth",
	model.InstrumentStrings:    "String Section",
	model.InstrumentBrass:      "Brass Section",
	model.InstrumentWoodwinds:  "Woodwinds",
	model.InstrumentPercussion: "Percussion",
	model.InstrumentPads:       "Synth Pad",
	model.InstrumentLead:       "Synth Lead",
	model.InstrumentFX:         "Sound Effects",
}

// SunoStemAdi, enstrumanin split_stem_advanced stemName'ini doner.
func SunoStemAdi(i model.Instrument) (string, bool) {
	ad, ok := sunoStemAdlari[i]
	return ad, ok
}
```

`WaitStems`'ten sonra:

```go
// IsolateStem, parcadan tek bir enstrumani ayirir (split_stem_advanced,
// 20 kredi). Yanit hem enstrumani hem de onsuz parcayi doner.
func (c *SunoClient) IsolateStem(ctx context.Context, taskID, audioID, stemName string) (string, error) {
	body := map[string]any{
		"taskId":      taskID,
		"audioId":     audioID,
		"type":        "split_stem_advanced",
		"stemName":    stemName,
		"callBackUrl": c.callbackURL,
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	if err := c.post(ctx, "/api/v1/vocal-removal/generate", body, &data); err != nil {
		return "", err
	}
	if data.TaskID == "" {
		return "", fmt.Errorf("suno: ayirma yanitinda taskId yok")
	}
	return data.TaskID, nil
}

// WaitIsolated, split_stem_advanced SUCCESS olana kadar poll eder.
// Saglayici uretimin her varyanti icin bir originData elemani doner;
// biz ilk varyanti (WaitMusic'in dondurdugu sunoData[0]) kullaniriz.
func (c *SunoClient) WaitIsolated(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) (*IsolatedStem, error) {
	type parca struct {
		AudioURL string  `json:"audio_url"`
		Duration float64 `json:"duration"`
	}
	var sonuc *IsolatedStem
	err := c.bekle(ctx, interval, maxWait, "ayirma", func() (bool, error) {
		var data struct {
			SuccessFlag  string `json:"successFlag"`
			ErrorMessage string `json:"errorMessage"`
			Response     *struct {
				OriginData []struct {
					Extract parca `json:"extract"`
					Remove  parca `json:"remove"`
				} `json:"originData"`
			} `json:"response"`
		}
		if err := c.get(ctx, "/api/v1/vocal-removal/record-info?taskId="+url.QueryEscape(stemTaskID), &data); err != nil {
			return false, geciciHata{err}
		}
		if sunoHataDurumlari[data.SuccessFlag] {
			return false, fmt.Errorf("suno ayirma basarisiz (%s): %s", data.SuccessFlag, data.ErrorMessage)
		}
		if data.SuccessFlag != "SUCCESS" {
			return false, nil
		}
		if data.Response == nil || len(data.Response.OriginData) == 0 {
			return false, fmt.Errorf("suno: SUCCESS ama originData yok")
		}
		ilk := data.Response.OriginData[0]
		if ilk.Extract.AudioURL == "" || ilk.Remove.AudioURL == "" {
			return false, fmt.Errorf("suno: SUCCESS ama ayirma adresi eksik")
		}
		sonuc = &IsolatedStem{ExtractURL: ilk.Extract.AudioURL, RemoveURL: ilk.Remove.AudioURL, Duration: ilk.Extract.Duration}
		return true, nil
	})
	return sonuc, err
}
```

`MusicGenerator` arayuzu genisledigi icin `internal/worker/render_worker_test.go`'daki `sahteSuno` artik derlenmez. Bu task'ta ona iki minimal metod ekle (Task 3 genisletecek):

```go
func (s *sahteSuno) IsolateStem(_ context.Context, taskID, audioID, stemName string) (string, error) {
	return "i1", nil
}
func (s *sahteSuno) WaitIsolated(context.Context, string, time.Duration, time.Duration) (*client.IsolatedStem, error) {
	return nil, nil
}
```

- [ ] **Step 4: Run all**

Run: `go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd e2e`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client internal/worker/render_worker_test.go
git commit -m "feat(suno): split_stem_advanced ile tek enstruman ayirma"
```

---

### Task 3: Worker — moda gore dallanma

**Files:**
- Modify: `internal/worker/render_worker.go` (`sunoIleUret`, `generateResult`, `generateMockResult`)
- Modify: `internal/worker/render_worker_test.go`, `internal/worker/mock_adres_test.go`

**Interfaces:**
- Consumes: Task 1 (`payload.StemMode`, `payload.FocusInstrument`, `StemMode*`, `InstrumentMix`, `InstrumentRest`, `RenderResultRecord.StemMode`); Task 2 (`IsolateStem`, `WaitIsolated`, `IsolatedStem`, `SunoStemAdi`).
- Produces: `RenderResultRecord.StemMode` dolu; `mix` → 1 stem (`mix`), `focus` → 2 stem (secilen enstruman, `rest`), `stems` → bugunku davranis. Bos/bilinmeyen `payload.StemMode` (Task 1 oncesi kuyruga girmis gorev) `stems` gibi DEGIL, `mix` gibi islenir (ucuz taraf).

- [ ] **Step 1: Write the failing tests**

`internal/worker/render_worker_test.go`:

1. `sahteSuno` struct'ina alanlar ekle ve Task 2'nin minimal metodlarini degistir:

```go
	// focus modu
	ayirArgs      [3]string
	ayirSayisi    int
	ayirHata      error
	ayrik         *client.IsolatedStem
	ayrikBekleHata error
	bolSayisi     int
```

```go
func (s *sahteSuno) IsolateStem(_ context.Context, taskID, audioID, stemName string) (string, error) {
	s.ayirSayisi++
	s.ayirArgs = [3]string{taskID, audioID, stemName}
	return "i1", s.ayirHata
}
func (s *sahteSuno) WaitIsolated(context.Context, string, time.Duration, time.Duration) (*client.IsolatedStem, error) {
	return s.ayrik, s.ayrikBekleHata
}
```

ve mevcut `SplitStems` metoduna `s.bolSayisi++` ekle.

2. `testPayload()` mevcut testler `stems` yolunu sinadigi icin `StemMode: model.StemModeStems` alsin:

```go
func testPayload() *model.RenderJobPayload {
	return &model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", StemMode: model.StemModeStems, Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{"x"}}}
}
```

3. Yeni testler:

```go
func TestSunoIleUret_MixModuAyirmaYapmaz(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 120}}
	d := &sahteDepo{}
	p := testPayload()
	p.StemMode = model.StemModeMix

	res, err := testWorker(s, d).sunoIleUret(context.Background(), p, hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.bolSayisi != 0 || s.ayirSayisi != 0 {
		t.Errorf("mix modunda ayirma cagrildi: split=%d advanced=%d", s.bolSayisi, s.ayirSayisi)
	}
	if res.StemMode != model.StemModeMix || len(res.Stems) != 1 || res.Stems[0].Instrument != model.InstrumentMix {
		t.Fatalf("sonuc = %+v", res)
	}
	if len(d.yuklemeler) != 1 || !bytes.Equal(d.yuklemeler[0].veri, []byte("ses:/tam.mp3")) || d.yuklemeler[0].tip != "audio/mpeg" {
		t.Errorf("yukleme = %+v", d.yuklemeler)
	}
	if !strings.HasPrefix(d.yuklemeler[0].key, "stems/kullanici-a/p1/") {
		t.Errorf("anahtar = %q", d.yuklemeler[0].key)
	}
}

func TestSunoIleUret_BosModMixGibi(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 120}}
	p := testPayload()
	p.StemMode = ""
	res, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.bolSayisi != 0 || res.StemMode != model.StemModeMix {
		t.Errorf("bos mod mix gibi islenmeli: split=%d mod=%q", s.bolSayisi, res.StemMode)
	}
}

func TestSunoIleUret_MixModuParcaAdresiYoksaTekrarYok(t *testing.T) {
	s := &sahteSuno{track: &client.Track{ID: "a1", Duration: 120}}
	p := testPayload()
	p.StemMode = model.StemModeMix
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}

func TestSunoIleUret_FocusModuIkiStem(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{
		track: &client.Track{ID: "a1", Duration: 141},
		ayrik: &client.IsolatedStem{ExtractURL: srv.URL + "/davul.mp3", RemoveURL: srv.URL + "/kalan.mp3", Duration: 141},
	}
	d := &sahteDepo{}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	davul := model.InstrumentDrums
	p.FocusInstrument = &davul

	res, err := testWorker(s, d).sunoIleUret(context.Background(), p, hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if s.ayirArgs != [3]string{"t1", "a1", "Drum Kit"} || s.bolSayisi != 0 {
		t.Errorf("ayirma argumanlari = %v, split=%d", s.ayirArgs, s.bolSayisi)
	}
	if res.StemMode != model.StemModeFocus || len(res.Stems) != 2 {
		t.Fatalf("sonuc = %+v", res)
	}
	if res.Stems[0].Instrument != model.InstrumentDrums || res.Stems[1].Instrument != model.InstrumentRest {
		t.Errorf("enstrumanlar = %v, %v", res.Stems[0].Instrument, res.Stems[1].Instrument)
	}
	if len(d.yuklemeler) != 2 || !bytes.Equal(d.yuklemeler[1].veri, []byte("ses:/kalan.mp3")) {
		t.Errorf("yuklemeler = %+v", d.yuklemeler)
	}
}

func TestSunoIleUret_FocusEnstrumaniEslenmiyorsaSunoCagrilmaz(t *testing.T) {
	s := &sahteSuno{}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	p.FocusInstrument = nil
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) || s.uretimSayisi != 0 {
		t.Fatalf("ucret oncesi SkipRetry ve sifir uretim bekleniyordu: err=%v uretim=%d", err, s.uretimSayisi)
	}
}

func TestSunoIleUret_FocusAyirmaHatasiTekrarYok(t *testing.T) {
	s := &sahteSuno{track: &client.Track{ID: "a1"}, ayrikBekleHata: errors.New("katalog eslesmesi")}
	p := testPayload()
	p.StemMode = model.StemModeFocus
	davul := model.InstrumentDrums
	p.FocusInstrument = &davul
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), p, hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
}
```

`internal/worker/mock_adres_test.go` sonuna:

```go
func TestRenderWorker_MockModlari(t *testing.T) {
	w := &RenderWorker{}
	davul := model.InstrumentDrums
	durumlar := []struct {
		mod      model.StemMode
		odak     *model.Instrument
		beklenen []model.Instrument
	}{
		{model.StemModeMix, nil, []model.Instrument{model.InstrumentMix}},
		{"", nil, []model.Instrument{model.InstrumentMix}},
		{model.StemModeFocus, &davul, []model.Instrument{model.InstrumentDrums, model.InstrumentRest}},
		{model.StemModeStems, nil, []model.Instrument{model.InstrumentDrums, model.InstrumentBass}},
	}
	for _, d := range durumlar {
		p := &model.RenderJobPayload{OwnerID: "kullanici-a", ProjectID: "p1", StemMode: d.mod, FocusInstrument: d.odak}
		p.Arrangement.Instruments = []model.Instrument{model.InstrumentDrums, model.InstrumentBass}
		rec, err := w.generateMockResult(p)
		if err != nil {
			t.Fatalf("%q: %v", d.mod, err)
		}
		var got []model.Instrument
		for _, s := range rec.Stems {
			got = append(got, s.Instrument)
		}
		if len(got) != len(d.beklenen) {
			t.Fatalf("%q: stemler = %v, beklenen %v", d.mod, got, d.beklenen)
		}
		for i := range got {
			if got[i] != d.beklenen[i] {
				t.Errorf("%q: stemler = %v, beklenen %v", d.mod, got, d.beklenen)
			}
		}
	}
}
```

Ayrica mevcut `TestRenderWorker_MockStemAdresiCozulebilir`'in payload'ina `StemMode: model.StemModeStems` ekle (yoksa yeni varsayilanla `mix` stem'i doner ve test ettigi ayri enstruman yolu kaybolur).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/worker/ -v -run 'MixModu|BosMod|FocusModu|FocusEnstrumani|FocusAyirma|MockModlari'`
Expected: FAIL

- [ ] **Step 3: Implementation**

`internal/worker/render_worker.go`:

1. `sunoIleUret`'te sahip/proje kontrolunden hemen sonra, `GenerateMusic`'ten ONCE:

```go
	// Mod ucretli cagridan ONCE cozulur: focus enstrumani eslenemiyorsa
	// 12 kredi harcanip sonra dusulmesin.
	mod, odakAdi, err := stemPlani(payload)
	if err != nil {
		return nil, fmt.Errorf("stem ayari gecersiz, Suno cagrilmadi: %w: %w", err, asynq.SkipRetry)
	}
```

Not: `sunoIleUret` adlandirilmis donus (`sonuc`, `hata`) kullaniyor; yukaridaki `err` yeni bir yerel degiskendir, sorun yok. Sonra gelen `taskID, err := w.music.GenerateMusic(...)` satiri `taskID` yeni oldugu icin derlenir.

2. `WaitMusic`'ten sonra, mevcut "Splitting stems..." blogundan `uploadStems` cagrisina kadar olan kismi su dallanmayla degistir (`sure` hesabi dallanmadan ONCE kalir):

```go
	// Suno sure donmezse yapidan tahmin edilir.
	sure := track.Duration
	if sure <= 0 {
		sure = tahminiSure(payload)
	}

	var stems []client.Stem
	switch mod {
	case model.StemModeMix:
		// Uretim instrumental; parcanin kendisi tek stem'dir, ayirma yok.
		if track.AudioURL == "" {
			return nil, ucretli("mix render failed", errors.New("suno parca adresi donmedi"))
		}
		stems = []client.Stem{{Instrument: model.InstrumentMix, URL: track.AudioURL}}

	case model.StemModeFocus:
		ilerleme(60, "Isolating instrument...")
		ayirID, err := w.music.IsolateStem(ctx, taskID, track.ID, odakAdi)
		if err != nil {
			return nil, ucretli("instrument isolation failed", err)
		}
		ilerleme(75, "Waiting for instrument isolation...")
		ayrik, err := w.music.WaitIsolated(ctx, ayirID, w.pollAraligi, 5*time.Minute)
		if err != nil {
			return nil, ucretli("instrument isolation failed", err)
		}
		stems = []client.Stem{
			{Instrument: *payload.FocusInstrument, URL: ayrik.ExtractURL},
			{Instrument: model.InstrumentRest, URL: ayrik.RemoveURL},
		}

	default: // stems
		ilerleme(60, "Splitting stems...")
		stemTaskID, err := w.music.SplitStems(ctx, taskID, track.ID)
		if err != nil {
			return nil, ucretli("stem splitting failed", err)
		}
		ilerleme(75, "Waiting for stem separation...")
		stems, err = w.music.WaitStems(ctx, stemTaskID, w.pollAraligi, 5*time.Minute)
		if err != nil {
			return nil, ucretli("stem splitting failed", err)
		}
		if len(stems) == 0 {
			return nil, ucretli("stem splitting failed", errors.New("suno hic enstruman stem'i donmedi"))
		}
	}
```

(Eski `sure` blogu bu blogun icine tasindi; tekrar etme.) Sondaki `return w.generateResult(payload, sure, records), nil` → `generateResult` kaydina modu yazsin: `generateResult` icinde donen `RenderResultRecord` literaline `StemMode: mod,` ekleyemezsin cunku `mod` orada yok; bu yuzden imzayi `generateResult(payload *model.RenderJobPayload, mod model.StemMode, duration float64, stems []model.StemResultRecord)` yap ve cagriyi `w.generateResult(payload, mod, sure, records)` olarak guncelle.

3. Yeni yardimci (ayni dosyada, `sunoIleUret`'ten sonra):

```go
// stemPlani, payload'in stem modunu cozer. Bos ya da bilinmeyen mod mix
// sayilir (ucuz taraf; Task oncesi kuyruga girmis gorevler dahil). focus
// icin enstrumanin Suno stemName karsiligi doner.
func stemPlani(payload *model.RenderJobPayload) (model.StemMode, string, error) {
	switch payload.StemMode {
	case model.StemModeStems:
		return model.StemModeStems, "", nil
	case model.StemModeFocus:
		if payload.FocusInstrument == nil {
			return "", "", errors.New("focus modu enstruman ister")
		}
		ad, ok := client.SunoStemAdi(*payload.FocusInstrument)
		if !ok {
			return "", "", fmt.Errorf("enstruman ayrilamaz: %s", *payload.FocusInstrument)
		}
		return model.StemModeFocus, ad, nil
	default:
		return model.StemModeMix, "", nil
	}
}
```

4. `generateMockResult`: stem dongusunu moda gore kur (bpm/tonic/scale/duration hesaplari aynen kalir):

```go
	mod, _, err := stemPlani(payload)
	if err != nil {
		return nil, err
	}
	var enstrumanlar []model.Instrument
	switch mod {
	case model.StemModeMix:
		enstrumanlar = []model.Instrument{model.InstrumentMix}
	case model.StemModeFocus:
		enstrumanlar = []model.Instrument{*payload.FocusInstrument, model.InstrumentRest}
	default:
		enstrumanlar = payload.Arrangement.Instruments
	}

	var stems []model.StemResultRecord
	for _, instrument := range enstrumanlar {
		// (mevcut dongu govdesi aynen: stemID, KullaniciAnahtari, append)
	}
```

ve donen kayda `StemMode: mod,` ekle.

- [ ] **Step 4: Run all**

Run: `go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd e2e && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...`
Expected: PASS, gofmt bos, `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/worker
git commit -m "feat(render): mix/focus/stems moduna gore Suno akisi"
```

---

### Task 4: Dokuman ve Swagger

**Files:**
- Modify: `CLAUDE.md` (repo koku)
- Modify: `core-service/docs/API.md`
- Regenerate: `core-service/docs/docs.go`, `swagger.json`, `swagger.yaml`

- [ ] **Step 1: CLAUDE.md**

`### External services` tablosundaki Suno satirini su hale getir:

```markdown
| Suno (`api.sunoapi.org`, model `SUNO_MODEL`, default `V6`) | Instrumental rendering. Per-render stem level chosen by the user via `stemMode`: `mix` (default, 12 credits: the generated track as one `mix` stem), `focus` (32: `split_stem_advanced` on `focusInstrument` → that instrument + `rest`), `stems` (62: `split_stem`, 10 instrument stems kept; vocals and woodwinds skipped) | Simulated render steps |
```

`### Key layers` icindeki `worker/` maddesinde `render_worker.go (Suno /api/v1/generate + split_stem, ...` ifadesini `render_worker.go (Suno /api/v1/generate, then by stemMode: none / split_stem_advanced / split_stem, ...` yap.

Testler satirinin altindaki Suno e2e notunu su hale getir:

```markdown
Ucretli gercek Suno testi (`TestRenderFullPipeline_RealSuno`) yalnizca `SUNO_E2E_ONAY=1` ile kosar; istek `stemMode` gondermedigi icin varsayilan `mix` ile 12 kredi harcar.
```

- [ ] **Step 2: API.md**

`core-service/docs/API.md`'de render start bolumunu bul (`grep -n "render/start" core-service/docs/API.md`). Istek govdesine `stemMode` (`"mix" | "focus" | "stems"`, opsiyonel, varsayilan `"mix"`) ve `focusInstrument` (yalnizca `focus`'ta, zorunlu, aranjmandaki enstrumanlardan biri) alanlarini, kredi maliyetlerini (12 / 32 / 62) ve 400 durumlarini (focus'ta enstruman yok, enstruman aranjmanda yok, mix/stems ile focusInstrument) ekle. Render result bolumune `stemMode` alanini ve cikis enstrumanlari `mix` ile `rest`'in anlamini ekle. Dosyanin dili neyse onu kullan.

- [ ] **Step 3: Swagger**

Run: `cd core-service && swag init -g cmd/server/main.go`
Expected: `grep -c "stemMode" docs/swagger.json` ≥ 2.

- [ ] **Step 4: Son dogrulama**

Run: `cd core-service && go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd e2e`

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md core-service/docs
git commit -m "docs: render stem seviyeleri (mix/focus/stems)"
```

---

## Mobil icin (ayri plan: Faz 3 mobil)

- Render baslatirken uc seviyeli secim (maliyet/kanal aciklamasiyla) ve `focus` icin aranjmandan enstruman secimi; istekte `stemMode` + `focusInstrument`.
- Sonuctaki `stemMode`'a gore mikser: `mix` → tek kanal, `focus` → enstruman + `rest`, `stems` → enstruman basina kanal. Mobil enstruman tipi `mix` ve `rest` degerlerini tanimali.
