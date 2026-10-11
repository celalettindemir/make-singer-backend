# Suno Entegrasyonu Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render akisini `api.sunoapi.org`'un gercek API'sine baglamak, stem'leri R2'ye gercekten yuklemek ve kredi harcandiktan sonra tekrar denemeyi kesmek.

**Architecture:** `internal/client/suno_client.go` saglayicinin `{code,msg,data}` zarfli API'sine gore bastan yazilir ve `MusicGenerator` arayuzunu uygular. `internal/worker/render_worker.go`'daki Suno yolu, renderService/hub'a dokunmayan test edilebilir bir `sunoIleUret` fonksiyonuna ayrilir; ucretli adimdan sonraki hatalar `asynq.SkipRetry` ile sarilir. Style metni ayri bir dosyada saf fonksiyon olarak uretilir.

**Tech Stack:** Go 1.25, Fiber v2, Asynq, Viper, `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-10-10-suno-entegrasyonu-design.md`

## Global Constraints

- Tum komutlar `core-service/` dizininde calisir. Testler: `go test ./internal/...` (ag gerekmez).
- Kod yorumlari ve test adlari Turkce, **aksan isareti yok** (ç ğ ı ö ş ü yerine c g i o s u). Repo bu kurali uyguluyor.
- `go vet ./...` ve `gofmt -l internal` temiz kalmali.
- Stem tipi sabit `split_stem`; `separate_vocal` secenegi eklenmez.
- Saglayici sabitleri: model varsayilani `V6`; style siniri 1000 karakter; uretim poll'u 5 sn aralik / 10 dk tavan; stem poll'u 5 sn / 5 dk; indirme zaman asimi 30 sn; stem boyut tavani 50 MB.
- Gecici poll hatalari arka arkaya 3 kez tolere edilir.
- Stem anahtari: `stems/{projectID}/{stemID}.mp3`, icerik tipi `audio/mpeg`.
- Canli Suno cagrisi YAPILMAZ (tek render 62 kredi, hesapta 38 var). Tum testler `httptest` ve sahte uygulamalarla.
- Commit mesajlari `tip(alan): ozet` bicimi, sonunda `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch: `ai-modelleri`. Push YOK.

## Dosya Yapisi

| Dosya | Sorumluluk |
|---|---|
| `internal/config/config.go` (degisir) | `SunoConfig.Model`, `SunoConfig.CallbackURL`, env baglama, varsayilanlar |
| `internal/client/suno_client.go` (bastan yazilir) | Saglayici HTTP sozlesmesi, zarf cozumleme, poll, stem eslemesi |
| `internal/client/suno_client_test.go` (yeni) | Client sozlesme testleri |
| `internal/worker/render_style.go` (yeni) | `buildMusicStyle` saf fonksiyonu |
| `internal/worker/render_style_test.go` (yeni) | Style tablo testi |
| `internal/worker/render_worker.go` (degisir) | `sunoIleUret`, stem indirme/yukleme, SkipRetry |
| `internal/worker/render_worker_test.go` (yeni) | Worker Suno yolu testleri |

---

### Task 1: Config ve Suno uretim ucu

**Files:**
- Modify: `core-service/internal/config/config.go` (`SunoConfig` ~satir 112; `BindEnv` blogu ~satir 220; varsayilanlar ~satir 266; `Suno: SunoConfig{` ~satir 370)
- Modify: `core-service/config.yaml` (`suno:` blogu), `core-service/.env.example`
- Rewrite: `core-service/internal/client/suno_client.go`
- Test: `core-service/internal/config/config_test.go`, `core-service/internal/client/suno_client_test.go` (yeni)

**Interfaces:**
- Produces:
  - `config.SunoConfig{APIKey, BaseURL, Model, CallbackURL string}`
  - `client.NewSunoClient(cfg *config.SunoConfig) *SunoClient`
  - `type GenerateMusicRequest struct { Style, Title string; Instrumental bool }`
  - `type Track struct { ID, AudioURL string; Duration float64 }`
  - `(*SunoClient) GenerateMusic(ctx, *GenerateMusicRequest) (taskID string, err error)`
  - `(*SunoClient) WaitMusic(ctx, taskID string, interval, maxWait time.Duration) (*Track, error)`
  - `(*SunoClient) IsConfigured() bool`
  - Paket ici: `(*SunoClient) post/get(ctx, path, body, out any) error` (zarfi cozer), `(*SunoClient) bekle(ctx, interval, maxWait, ad string, kontrol func() (bool, error)) error`

- [ ] **Step 1: Config testini yaz**

`internal/config/config_test.go` sonuna ekle:

```go
func TestLoad_SunoVarsayilanlari(t *testing.T) {
	os.Unsetenv("SUNO_MODEL")
	os.Unsetenv("SUNO_CALLBACK_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.Suno.Model != "V6" {
		t.Errorf("Suno.Model = %q, beklenen V6", cfg.Suno.Model)
	}
	if cfg.Suno.CallbackURL == "" {
		t.Error("Suno.CallbackURL bos; saglayici zorunlu tutuyor")
	}
}

func TestLoad_SunoModelEnvIleDegisir(t *testing.T) {
	os.Setenv("SUNO_MODEL", "V6_MINI")
	defer os.Unsetenv("SUNO_MODEL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load hata verdi: %v", err)
	}
	if cfg.Suno.Model != "V6_MINI" {
		t.Errorf("Suno.Model = %q, beklenen V6_MINI", cfg.Suno.Model)
	}
}
```

- [ ] **Step 2: Client uretim testlerini yaz**

`internal/client/suno_client_test.go` olustur:

```go
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/config"
)

func testSuno(t *testing.T, h http.HandlerFunc) *SunoClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewSunoClient(&config.SunoConfig{
		APIKey: "anahtar", BaseURL: srv.URL, Model: "V6", CallbackURL: "https://ornek.test/geri",
	})
}

func zarfYaz(w http.ResponseWriter, code int, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": "m", "data": data})
}

func TestGenerateMusic_SaglayiciSozlesmesi(t *testing.T) {
	var govde map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/generate" {
			t.Errorf("istek = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer anahtar" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&govde)
		zarfYaz(w, 200, map[string]any{"taskId": "t1"})
	})

	id, err := c.GenerateMusic(context.Background(), &GenerateMusicRequest{Style: "pop, instrumental", Title: "pop instrumental", Instrumental: true})
	if err != nil {
		t.Fatalf("GenerateMusic: %v", err)
	}
	if id != "t1" {
		t.Errorf("taskID = %q", id)
	}
	beklenen := map[string]any{
		"customMode": true, "instrumental": true, "model": "V6",
		"callBackUrl": "https://ornek.test/geri", "style": "pop, instrumental", "title": "pop instrumental",
	}
	for k, v := range beklenen {
		if govde[k] != v {
			t.Errorf("govde[%q] = %v, beklenen %v", k, govde[k], v)
		}
	}
}

func TestGenerateMusic_ZarfHataKodu(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 429, "msg": "insufficient credits", "data": nil})
	})
	_, err := c.GenerateMusic(context.Background(), &GenerateMusicRequest{Style: "pop"})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("429 hatasi bekleniyordu, geldi: %v", err)
	}
}

func TestWaitMusic_SuccessIlkParcayiDoner(t *testing.T) {
	var n atomic.Int32
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/generate/record-info" || r.URL.Query().Get("taskId") != "t1" {
			t.Errorf("istek = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if n.Add(1) == 1 {
			zarfYaz(w, 200, map[string]any{"status": "PENDING"})
			return
		}
		zarfYaz(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{"sunoData": []any{
			map[string]any{"id": "a1", "audio_url": "https://cdn.test/a1.mp3", "duration": 201.16},
			map[string]any{"id": "a2", "audio_url": "https://cdn.test/a2.mp3", "duration": 191.6},
		}}})
	})

	tr, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitMusic: %v", err)
	}
	if tr.ID != "a1" || tr.AudioURL != "https://cdn.test/a1.mp3" || tr.Duration != 201.16 {
		t.Errorf("parca = %+v", tr)
	}
}

func TestWaitMusic_HataDurumuAnindaDoner(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		zarfYaz(w, 200, map[string]any{"status": "SENSITIVE_WORD_ERROR", "errorMessage": "yasakli kelime"})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "yasakli kelime") {
		t.Fatalf("hata mesaji bekleniyordu, geldi: %v", err)
	}
}

func TestWaitMusic_IkiGeciciHatayiTolereEder(t *testing.T) {
	var n atomic.Int32
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		zarfYaz(w, 200, map[string]any{"status": "SUCCESS", "response": map[string]any{"sunoData": []any{
			map[string]any{"id": "a1", "audio_url": "u", "duration": 1.0},
		}}})
	})
	if _, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second); err != nil {
		t.Fatalf("iki gecici hata tolere edilmeliydi: %v", err)
	}
}

func TestWaitMusic_UcArdisikHatadaVazgecer(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, time.Second); err == nil {
		t.Fatal("uc ardisik hatada vazgecmeliydi")
	}
}

func TestWaitMusic_ZamanAsimi(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		zarfYaz(w, 200, map[string]any{"status": "TEXT_SUCCESS"})
	})
	_, err := c.WaitMusic(context.Background(), "t1", time.Millisecond, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "zaman asimi") {
		t.Fatalf("zaman asimi bekleniyordu, geldi: %v", err)
	}
}
```

- [ ] **Step 3: Testlerin kirmizi oldugunu dogrula**

Run: `go test ./internal/config/ ./internal/client/`
Expected: derleme hatasi (`unknown field Model`/`CallbackURL`, `GenerateMusic` donus tipi uyusmuyor, `WaitMusic undefined`).

- [ ] **Step 4: Config'i uygula**

`internal/config/config.go`:

```go
type SunoConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	// CallbackURL: saglayici zorunlu tutuyor ama biz poll ediyoruz; uc yok.
	CallbackURL string
}
```

BindEnv blogunda `suno.base_url` satirinin altina:

```go
	_ = viper.BindEnv("suno.model", "SUNO_MODEL")
	_ = viper.BindEnv("suno.callback_url", "SUNO_CALLBACK_URL")
```

Varsayilanlarda `suno.base_url` satirinin altina:

```go
	viper.SetDefault("suno.model", "V6")
	viper.SetDefault("suno.callback_url", "https://makesinger-api.celalettindemir.dev/suno/callback")
```

`Suno: SunoConfig{` blogu:

```go
		Suno: SunoConfig{
			APIKey:      viper.GetString("suno.api_key"),
			BaseURL:     viper.GetString("suno.base_url"),
			Model:       viper.GetString("suno.model"),
			CallbackURL: viper.GetString("suno.callback_url"),
		},
```

`config.yaml` `suno:` bloguna `base_url` altina:

```yaml
  model: "V6"
  callback_url: "https://makesinger-api.celalettindemir.dev/suno/callback"
```

`.env.example`'da `SUNO_BASE_URL` satirinin altina:

```
SUNO_MODEL=V6
SUNO_CALLBACK_URL=https://makesinger-api.celalettindemir.dev/suno/callback
```

(`.env.example`'da `SUNO_BASE_URL` yoksa `SUNO_API_KEY` satirinin altina koy.)

- [ ] **Step 5: Client'i bastan yaz (uretim kismi)**

`internal/client/suno_client.go` dosyasinin TAMAMINI su icerikle degistir. Stem kismi Task 2'de eklenecek; `MusicGenerator` arayuzu de Task 2'de tamamlanir.

```go
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/makeasinger/api/internal/config"
)

// SunoClient, api.sunoapi.org istemcisi. Saglayici her yaniti
// {code, msg, data} zarfinda doner; HTTP 200 olsa bile code != 200 hatadir.
type SunoClient struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	model       string
	callbackURL string
}

// GenerateMusicRequest, worker'in uretim istegi. customMode, model ve
// callBackUrl client tarafindan eklenir.
type GenerateMusicRequest struct {
	Style        string
	Title        string
	Instrumental bool
}

// Track, tamamlanmis bir uretimin ilk parcasi. ID, stem bolmede audioId'dir.
type Track struct {
	ID       string
	AudioURL string
	Duration float64
}

// sunoHataDurumlari, beklemeyi aninda bitiren basarisizlik durumlari.
var sunoHataDurumlari = map[string]bool{
	"CREATE_TASK_FAILED":    true,
	"GENERATE_AUDIO_FAILED": true,
	"CALLBACK_EXCEPTION":    true,
	"SENSITIVE_WORD_ERROR":  true,
}

// pollHataToleransi: poll sirasinda arka arkaya kac gecici hata yutulur.
// Kredi harcandiktan sonra tek bir ag hatasi isi bosa cikarmasin.
const pollHataToleransi = 3

// NewSunoClient creates a new Suno API client
func NewSunoClient(cfg *config.SunoConfig) *SunoClient {
	return &SunoClient{
		httpClient:  &http.Client{Timeout: 120 * time.Second},
		baseURL:     cfg.BaseURL,
		apiKey:      cfg.APIKey,
		model:       cfg.Model,
		callbackURL: cfg.CallbackURL,
	}
}

// IsConfigured returns true if the client has valid configuration
func (c *SunoClient) IsConfigured() bool {
	return c.apiKey != ""
}

// GenerateMusic, custom modda uretim baslatir ve gorev kimligini doner.
// Bu cagri basariliysa kredi harcanmistir.
func (c *SunoClient) GenerateMusic(ctx context.Context, req *GenerateMusicRequest) (string, error) {
	body := map[string]any{
		"customMode":   true,
		"instrumental": req.Instrumental,
		"model":        c.model,
		"callBackUrl":  c.callbackURL,
		"style":        req.Style,
		"title":        req.Title,
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	if err := c.post(ctx, "/api/v1/generate", body, &data); err != nil {
		return "", err
	}
	if data.TaskID == "" {
		return "", fmt.Errorf("suno: yanitta taskId yok")
	}
	return data.TaskID, nil
}

// WaitMusic, uretim SUCCESS olana kadar poll eder ve ilk parcayi doner.
func (c *SunoClient) WaitMusic(ctx context.Context, taskID string, interval, maxWait time.Duration) (*Track, error) {
	var sonuc *Track
	err := c.bekle(ctx, interval, maxWait, "muzik", func() (bool, error) {
		var data struct {
			Status       string `json:"status"`
			ErrorMessage string `json:"errorMessage"`
			Response     *struct {
				SunoData []struct {
					ID       string  `json:"id"`
					AudioURL string  `json:"audio_url"`
					Duration float64 `json:"duration"`
				} `json:"sunoData"`
			} `json:"response"`
		}
		if err := c.get(ctx, "/api/v1/generate/record-info?taskId="+url.QueryEscape(taskID), &data); err != nil {
			return false, geciciHata{err}
		}
		if sunoHataDurumlari[data.Status] {
			return false, fmt.Errorf("suno uretimi basarisiz (%s): %s", data.Status, data.ErrorMessage)
		}
		if data.Status != "SUCCESS" {
			return false, nil
		}
		if data.Response == nil || len(data.Response.SunoData) == 0 {
			return false, fmt.Errorf("suno: SUCCESS ama parca yok")
		}
		p := data.Response.SunoData[0]
		sonuc = &Track{ID: p.ID, AudioURL: p.AudioURL, Duration: p.Duration}
		return true, nil
	})
	return sonuc, err
}

// geciciHata, bekle'nin tolere ettigi hata sinifi (ag, 5xx, bozuk yanit).
type geciciHata struct{ error }

func (e geciciHata) Unwrap() error { return e.error }

// bekle, kontrol true donene kadar interval araliklarla cagirir. kontrol'un
// geciciHata'si arka arkaya pollHataToleransi kez yutulur; diger hatalar
// aninda doner.
func (c *SunoClient) bekle(ctx context.Context, interval, maxWait time.Duration, ad string, kontrol func() (bool, error)) error {
	sonTarih := time.Now().Add(maxWait)
	ardisikHata := 0
	for time.Now().Before(sonTarih) {
		bitti, err := kontrol()
		if err != nil {
			var g geciciHata
			if !asGecici(err, &g) {
				return err
			}
			ardisikHata++
			log.Printf("[Suno API] %s poll gecici hata %d/%d: %v", ad, ardisikHata, pollHataToleransi, err)
			if ardisikHata >= pollHataToleransi {
				return fmt.Errorf("suno %s poll'u %d ardisik hatadan sonra birakildi: %w", ad, ardisikHata, g.error)
			}
		} else {
			ardisikHata = 0
			if bitti {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("suno %s beklemesi zaman asimi (%v)", ad, maxWait)
}

func asGecici(err error, hedef *geciciHata) bool {
	g, ok := err.(geciciHata)
	if ok {
		*hedef = g
	}
	return ok
}

// post, JSON govdeli POST atar ve zarfin data alanini out'a cozer.
func (c *SunoClient) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	return c.do(req, out)
}

// get, GET atar ve zarfin data alanini out'a cozer.
func (c *SunoClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	return c.do(req, out)
}

func (c *SunoClient) do(req *http.Request, out any) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("suno istegi gonderilemedi: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("suno yaniti okunamadi: %w", err)
	}
	log.Printf("[Suno API] %d %s %s", resp.StatusCode, req.Method, req.URL.Path)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("suno API error (status %d): %s", resp.StatusCode, string(raw))
	}

	var zarf struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &zarf); err != nil {
		return fmt.Errorf("suno yaniti cozulemedi: %w", err)
	}
	if zarf.Code != 200 {
		return fmt.Errorf("suno API error (code %d): %s", zarf.Code, zarf.Msg)
	}
	if out == nil || len(zarf.Data) == 0 || string(zarf.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(zarf.Data, out); err != nil {
		return fmt.Errorf("suno data alani cozulemedi: %w", err)
	}
	return nil
}
```

Not: Bu adimdan sonra `internal/worker` ve `cmd/server` derlenmez (eski metotlar silindi). Task 1'in dogrulamasi yalnizca `config` ve `client` paketleridir; derleme Task 3'te geri gelir.

- [ ] **Step 6: Testlerin gectigini dogrula**

Run: `go test ./internal/config/ ./internal/client/ && gofmt -l internal/client internal/config`
Expected: iki paket `ok`, gofmt ciktisi bos.

- [ ] **Step 7: Commit**

```bash
git add core-service/internal/config core-service/internal/client/suno_client.go core-service/internal/client/suno_client_test.go core-service/config.yaml core-service/.env.example
git commit -m "feat(suno): gercek /api/v1/generate sozlesmesi, model ve callback config

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Stem bolme ve enstruman eslemesi

**Files:**
- Modify: `core-service/internal/client/suno_client.go`
- Test: `core-service/internal/client/suno_client_test.go`

**Interfaces:**
- Consumes: Task 1'deki `post`, `get`, `bekle`, `geciciHata`, `sunoHataDurumlari`, `Track`, `GenerateMusicRequest`.
- Produces:
  - `type Stem struct { Instrument model.Instrument; URL string }`
  - `(*SunoClient) SplitStems(ctx, taskID, audioID string) (stemTaskID string, err error)`
  - `(*SunoClient) WaitStems(ctx, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error)`
  - Arayuz:
    ```go
    type MusicGenerator interface {
        GenerateMusic(ctx context.Context, req *GenerateMusicRequest) (string, error)
        WaitMusic(ctx context.Context, taskID string, interval, maxWait time.Duration) (*Track, error)
        SplitStems(ctx context.Context, taskID, audioID string) (string, error)
        WaitStems(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error)
        IsConfigured() bool
    }
    ```

- [ ] **Step 1: Stem testlerini yaz**

`suno_client_test.go` import listesine `"github.com/makeasinger/api/internal/model"` ekle ve sonuna:

```go
var _ MusicGenerator = (*SunoClient)(nil)

func TestSplitStems_SaglayiciSozlesmesi(t *testing.T) {
	var govde map[string]any
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vocal-removal/generate" {
			t.Errorf("istek = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&govde)
		zarfYaz(w, 200, map[string]any{"taskId": "s1"})
	})

	id, err := c.SplitStems(context.Background(), "t1", "a1")
	if err != nil {
		t.Fatalf("SplitStems: %v", err)
	}
	if id != "s1" {
		t.Errorf("stemTaskID = %q", id)
	}
	beklenen := map[string]any{"taskId": "t1", "audioId": "a1", "type": "split_stem", "callBackUrl": "https://ornek.test/geri"}
	for k, v := range beklenen {
		if govde[k] != v {
			t.Errorf("govde[%q] = %v, beklenen %v", k, govde[k], v)
		}
	}
}

func TestWaitStems_EnstrumanlariEslerVokaliAtlar(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vocal-removal/record-info" || r.URL.Query().Get("taskId") != "s1" {
			t.Errorf("istek = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		zarfYaz(w, 200, map[string]any{"successFlag": "SUCCESS", "response": map[string]any{
			"originData":       []any{map[string]any{"duration": 1.0}},
			"vocalUrl":         "https://cdn.test/vokal.mp3",
			"backingVocalsUrl": "https://cdn.test/geri.mp3",
			"keyboardUrl":      "https://cdn.test/klavye.mp3",
			"drumsUrl":         "https://cdn.test/davul.mp3",
			"bassUrl":          nil,
			"guitarUrl":        "",
		}})
	})

	stems, err := c.WaitStems(context.Background(), "s1", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("WaitStems: %v", err)
	}
	beklenen := []Stem{
		{Instrument: model.InstrumentDrums, URL: "https://cdn.test/davul.mp3"},
		{Instrument: model.InstrumentPiano, URL: "https://cdn.test/klavye.mp3"},
	}
	if len(stems) != len(beklenen) {
		t.Fatalf("stems = %+v, beklenen %+v", stems, beklenen)
	}
	for i := range beklenen {
		if stems[i] != beklenen[i] {
			t.Errorf("stems[%d] = %+v, beklenen %+v", i, stems[i], beklenen[i])
		}
	}
}

func TestWaitStems_HataBayragi(t *testing.T) {
	c := testSuno(t, func(w http.ResponseWriter, r *http.Request) {
		zarfYaz(w, 200, map[string]any{"successFlag": "GENERATE_AUDIO_FAILED", "errorMessage": "bolunemedi"})
	})
	_, err := c.WaitStems(context.Background(), "s1", time.Millisecond, time.Second)
	if err == nil || !strings.Contains(err.Error(), "bolunemedi") {
		t.Fatalf("hata bekleniyordu, geldi: %v", err)
	}
}
```

- [ ] **Step 2: Kirmizi oldugunu dogrula**

Run: `go test ./internal/client/`
Expected: derleme hatasi (`undefined: MusicGenerator`, `SplitStems`, `WaitStems`, `Stem`).

- [ ] **Step 3: Uygula**

`suno_client.go` import listesine `"github.com/makeasinger/api/internal/model"` ekle. `SunoClient` tipinin hemen ustune:

```go
// MusicGenerator, render worker'in Suno'dan bekledigi islemler.
type MusicGenerator interface {
	GenerateMusic(ctx context.Context, req *GenerateMusicRequest) (string, error)
	WaitMusic(ctx context.Context, taskID string, interval, maxWait time.Duration) (*Track, error)
	SplitStems(ctx context.Context, taskID, audioID string) (string, error)
	WaitStems(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error)
	IsConfigured() bool
}
```

`Track` tipinin altina:

```go
// Stem, indirilmeye hazir tek bir enstruman kanali. Adres sureli; hemen
// indirilmeli.
type Stem struct {
	Instrument model.Instrument
	URL        string
}

// stemAlanlari, split_stem yanitindaki camelCase alanlarin Instrument
// karsiliklari. Sira sonuc sirasidir. vocalUrl ve backingVocalsUrl yok:
// uretim instrumental ve mobil enum'u vokali tanimaz.
var stemAlanlari = []struct {
	alan       string
	instrument model.Instrument
}{
	{"drumsUrl", model.InstrumentDrums},
	{"bassUrl", model.InstrumentBass},
	{"guitarUrl", model.InstrumentGuitar},
	{"keyboardUrl", model.InstrumentPiano},
	{"percussionUrl", model.InstrumentPercussion},
	{"stringsUrl", model.InstrumentStrings},
	{"synthUrl", model.InstrumentSynth},
	{"fxUrl", model.InstrumentFX},
	{"brassUrl", model.InstrumentBrass},
	{"woodwindsUrl", model.InstrumentWoodwinds},
}
```

`WaitMusic`'in altina:

```go
// SplitStems, uretilen parcayi 12 kanala boler (split_stem, 50 kredi).
func (c *SunoClient) SplitStems(ctx context.Context, taskID, audioID string) (string, error) {
	body := map[string]any{
		"taskId":      taskID,
		"audioId":     audioID,
		"type":        "split_stem",
		"callBackUrl": c.callbackURL,
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	if err := c.post(ctx, "/api/v1/vocal-removal/generate", body, &data); err != nil {
		return "", err
	}
	if data.TaskID == "" {
		return "", fmt.Errorf("suno: stem yanitinda taskId yok")
	}
	return data.TaskID, nil
}

// WaitStems, stem bolme SUCCESS olana kadar poll eder ve enstruman
// kanallarini doner. Bos alanlar atlanir.
func (c *SunoClient) WaitStems(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error) {
	var sonuc []Stem
	err := c.bekle(ctx, interval, maxWait, "stem", func() (bool, error) {
		var data struct {
			SuccessFlag  string                     `json:"successFlag"`
			ErrorMessage string                     `json:"errorMessage"`
			Response     map[string]json.RawMessage `json:"response"`
		}
		if err := c.get(ctx, "/api/v1/vocal-removal/record-info?taskId="+url.QueryEscape(stemTaskID), &data); err != nil {
			return false, geciciHata{err}
		}
		if sunoHataDurumlari[data.SuccessFlag] {
			return false, fmt.Errorf("suno stem bolme basarisiz (%s): %s", data.SuccessFlag, data.ErrorMessage)
		}
		if data.SuccessFlag != "SUCCESS" {
			return false, nil
		}
		for _, a := range stemAlanlari {
			var adres string
			if raw, ok := data.Response[a.alan]; ok {
				_ = json.Unmarshal(raw, &adres) // null ve string disi degerler bos kalir
			}
			if adres != "" {
				sonuc = append(sonuc, Stem{Instrument: a.instrument, URL: adres})
			}
		}
		return true, nil
	})
	return sonuc, err
}
```

- [ ] **Step 4: Gectigini dogrula**

Run: `go test ./internal/client/ && gofmt -l internal/client`
Expected: `ok`, gofmt bos.

- [ ] **Step 5: Commit**

```bash
git add core-service/internal/client/suno_client.go core-service/internal/client/suno_client_test.go
git commit -m "feat(suno): split_stem ve enstruman eslemesi

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Style metni ve worker'in Suno yolu

**Files:**
- Create: `core-service/internal/worker/render_style.go`, `core-service/internal/worker/render_style_test.go`, `core-service/internal/worker/render_worker_test.go`
- Modify: `core-service/internal/worker/render_worker.go` (struct, `NewRenderWorker`, `ProcessTask`'taki Suno kontrolu, `processWithSuno`, `buildMusicPrompt` silinir, `uploadStems`, `generateResult`)
- Modify: `core-service/cmd/server/main.go` (sunoClient'in kullanildigi yerler derlenmeli; imza degisikligi gerekmez, `*SunoClient` arayuzu uygular)

**Interfaces:**
- Consumes: `client.MusicGenerator`, `client.GenerateMusicRequest`, `client.Track`, `client.Stem`, `client.StorageClient` (`Upload(ctx, key string, body io.Reader, contentType string) (string, time.Time, error)`).
- Produces:
  - `func buildMusicStyle(p *model.RenderJobPayload) string`
  - `func NewRenderWorker(renderService *service.RenderService, music client.MusicGenerator, r2Client client.StorageClient, hub *websocket.Hub) *RenderWorker`
  - `func (w *RenderWorker) sunoIleUret(ctx context.Context, payload *model.RenderJobPayload, ilerleme func(int, string)) (*model.RenderResultRecord, error)`

- [ ] **Step 1: Style testini yaz**

`internal/worker/render_style_test.go`:

```go
package worker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/model"
)

func intp(v int) *int { return &v }

func TestBuildMusicStyle(t *testing.T) {
	tonic, scale := model.Tonic("A"), model.Scale("minor")
	tam := &model.RenderJobPayload{
		Brief: model.Brief{
			Genre: model.Genre("pop"), Vibes: []string{"huzunlu", " umutlu "},
			BPM: model.BPMConfig{Value: intp(96)},
			Key: model.KeyConfig{Tonic: &tonic, Scale: &scale},
		},
		Arrangement: model.Arrangement{
			Instruments: []model.Instrument{model.InstrumentPiano, model.InstrumentDrums},
			Density:     model.Density("medium"), Groove: model.Groove("half_time"),
		},
	}
	got := buildMusicStyle(tam)
	want := "pop, instrumental, 96 BPM, A minor, huzunlu, umutlu, medium arrangement, half time groove, featuring piano, drums"
	if got != want {
		t.Errorf("tam:\n got %q\nwant %q", got, want)
	}

	asgari := &model.RenderJobPayload{Brief: model.Brief{Genre: model.Genre("rock"), Vibes: []string{"x"}}}
	if got := buildMusicStyle(asgari); got != "rock, instrumental, x" {
		t.Errorf("asgari = %q", got)
	}

	uzun := &model.RenderJobPayload{Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{strings.Repeat("ş", 900)}}}
	got = buildMusicStyle(uzun)
	if len(got) > 1000 || !utf8.ValidString(got) {
		t.Errorf("uzun style %d bayt, gecerli UTF-8: %v", len(got), utf8.ValidString(got))
	}
}
```

- [ ] **Step 2: Worker testini yaz**

`internal/worker/render_worker_test.go`:

```go
package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

type sahteSuno struct {
	uretHata, bekleHata, bolHata error
	track                         *client.Track
	stems                         []client.Stem
	uretimIstegi                  *client.GenerateMusicRequest
	uretimSayisi                  int
	bolArgs                       [2]string
}

func (s *sahteSuno) GenerateMusic(_ context.Context, r *client.GenerateMusicRequest) (string, error) {
	s.uretimSayisi++
	s.uretimIstegi = r
	return "t1", s.uretHata
}
func (s *sahteSuno) WaitMusic(context.Context, string, time.Duration, time.Duration) (*client.Track, error) {
	return s.track, s.bekleHata
}
func (s *sahteSuno) SplitStems(_ context.Context, taskID, audioID string) (string, error) {
	s.bolArgs = [2]string{taskID, audioID}
	return "s1", s.bolHata
}
func (s *sahteSuno) WaitStems(context.Context, string, time.Duration, time.Duration) ([]client.Stem, error) {
	return s.stems, nil
}
func (s *sahteSuno) IsConfigured() bool { return true }

type yukleme struct {
	key, tip string
	veri     []byte
}

type sahteDepo struct{ yuklemeler []yukleme }

func (d *sahteDepo) Upload(_ context.Context, key string, body io.Reader, tip string) (string, time.Time, error) {
	b, _ := io.ReadAll(body)
	d.yuklemeler = append(d.yuklemeler, yukleme{key, tip, b})
	return "https://depo.test/" + key, time.Time{}, nil
}
func (d *sahteDepo) Delete(context.Context, string) error { return nil }
func (d *sahteDepo) GetSignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (d *sahteDepo) URLFor(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func sesSunucusu(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/yok.mp3" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ses:" + r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testPayload() *model.RenderJobPayload {
	return &model.RenderJobPayload{ProjectID: "p1", Brief: model.Brief{Genre: model.Genre("pop"), Vibes: []string{"x"}}}
}

func testWorker(s *sahteSuno, d client.StorageClient) *RenderWorker {
	w := NewRenderWorker(nil, s, d, nil)
	w.pollAraligi = time.Millisecond
	return w
}

func hic(int, string) {}

func TestSunoIleUret_MutluYol(t *testing.T) {
	srv := sesSunucusu(t)
	s := &sahteSuno{
		track: &client.Track{ID: "a1", AudioURL: srv.URL + "/tam.mp3", Duration: 180},
		stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/davul.mp3"},
			{Instrument: model.InstrumentPiano, URL: srv.URL + "/piyano.mp3"},
		},
	}
	d := &sahteDepo{}

	res, err := testWorker(s, d).sunoIleUret(context.Background(), testPayload(), hic)
	if err != nil {
		t.Fatalf("sunoIleUret: %v", err)
	}
	if !s.uretimIstegi.Instrumental || !strings.HasPrefix(s.uretimIstegi.Style, "pop, instrumental") {
		t.Errorf("uretim istegi = %+v", s.uretimIstegi)
	}
	if s.bolArgs != [2]string{"t1", "a1"} {
		t.Errorf("SplitStems argumanlari = %v", s.bolArgs)
	}
	if res.Duration != 180 || len(res.Stems) != 2 {
		t.Fatalf("sonuc = %+v", res)
	}
	for i, y := range d.yuklemeler {
		st := res.Stems[i]
		if y.key != st.FileKey || !strings.HasPrefix(y.key, "stems/p1/") || !strings.HasSuffix(y.key, ".mp3") {
			t.Errorf("anahtar = %q, kayit %q", y.key, st.FileKey)
		}
		if y.tip != "audio/mpeg" {
			t.Errorf("icerik tipi = %q", y.tip)
		}
		if st.Duration != 180 {
			t.Errorf("stem suresi = %v", st.Duration)
		}
	}
	if !bytes.Equal(d.yuklemeler[0].veri, []byte("ses:/davul.mp3")) || res.Stems[1].Instrument != model.InstrumentPiano {
		t.Errorf("yuklenen icerik/enstruman yanlis: %q, %v", d.yuklemeler[0].veri, res.Stems[1].Instrument)
	}
}

func TestSunoIleUret_UretimReddiTekrarDenenebilir(t *testing.T) {
	s := &sahteSuno{uretHata: errors.New("kredi yok")}
	_, err := testWorker(s, &sahteDepo{}).sunoIleUret(context.Background(), testPayload(), hic)
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ucret oncesi hata tekrar denenebilir olmali, geldi: %v", err)
	}
}

func TestSunoIleUret_UcretliAdimSonrasiTekrarYok(t *testing.T) {
	srv := sesSunucusu(t)
	durumlar := map[string]*sahteSuno{
		"bekleme hatasi": {bekleHata: errors.New("zaman asimi")},
		"bolme hatasi":   {track: &client.Track{ID: "a1"}, bolHata: errors.New("reddedildi")},
		"stem yok":       {track: &client.Track{ID: "a1"}},
		"indirme 404": {track: &client.Track{ID: "a1"}, stems: []client.Stem{
			{Instrument: model.InstrumentDrums, URL: srv.URL + "/yok.mp3"},
		}},
	}
	for ad, s := range durumlar {
		t.Run(ad, func(t *testing.T) {
			d := &sahteDepo{}
			_, err := testWorker(s, d).sunoIleUret(context.Background(), testPayload(), hic)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
			}
			if len(d.yuklemeler) != 0 {
				t.Errorf("basarisiz iste yukleme olmamali: %d", len(d.yuklemeler))
			}
		})
	}
}

func TestSunoIleUret_DepoYoksaSunoCagrilmaz(t *testing.T) {
	s := &sahteSuno{}
	_, err := testWorker(s, nil).sunoIleUret(context.Background(), testPayload(), hic)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("SkipRetry bekleniyordu, geldi: %v", err)
	}
	if s.uretimSayisi != 0 {
		t.Errorf("depo yokken Suno %d kez cagrildi", s.uretimSayisi)
	}
}
```

- [ ] **Step 3: Kirmizi oldugunu dogrula**

Run: `go test ./internal/worker/`
Expected: derleme hatasi (`undefined: buildMusicStyle`, `sunoIleUret`, `pollAraligi`; ayrica eski `client.MusicResult` referanslari).

- [ ] **Step 4: Style'i uygula**

`internal/worker/render_style.go`:

```go
package worker

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/makeasinger/api/internal/model"
)

// styleTavani: Suno V6 style alani siniri (karakter). Bayt olarak kesmek
// her zaman bu sinirin altinda kalir.
const styleTavani = 1000

// buildMusicStyle, brief ve arrangement'tan Suno style metnini uretir.
// Degeri olmayan alanlar yazilmaz; BPM/key yoksa Suno kendisi secer.
func buildMusicStyle(p *model.RenderJobPayload) string {
	parcalar := []string{string(p.Brief.Genre), "instrumental"}

	if v := p.Brief.BPM.Value; v != nil {
		parcalar = append(parcalar, fmt.Sprintf("%d BPM", *v))
	}
	if k := p.Brief.Key; k.Tonic != nil && k.Scale != nil {
		parcalar = append(parcalar, fmt.Sprintf("%s %s", *k.Tonic, *k.Scale))
	}
	for _, v := range p.Brief.Vibes {
		if v = strings.TrimSpace(v); v != "" {
			parcalar = append(parcalar, v)
		}
	}
	if d := p.Arrangement.Density; d != "" {
		parcalar = append(parcalar, fmt.Sprintf("%s arrangement", d))
	}
	if g := p.Arrangement.Groove; g != "" {
		parcalar = append(parcalar, strings.ReplaceAll(string(g), "_", " ")+" groove")
	}
	if len(p.Arrangement.Instruments) > 0 {
		adlar := make([]string, len(p.Arrangement.Instruments))
		for i, in := range p.Arrangement.Instruments {
			adlar[i] = string(in)
		}
		parcalar = append(parcalar, "featuring "+strings.Join(adlar, ", "))
	}

	return kes(strings.Join(parcalar, ", "), styleTavani)
}

// kes, s'yi en fazla n bayta rune sinirinda keser.
func kes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
```

- [ ] **Step 5: Worker'i uygula**

`internal/worker/render_worker.go`:

Import listesi:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
	"github.com/makeasinger/api/internal/service"
	"github.com/makeasinger/api/internal/websocket"
)
```

Struct ve kurucu:

```go
// stemBoyutTavani: tek stem dosyasi icin indirme siniri.
const stemBoyutTavani = 50 << 20

// RenderWorker processes render jobs
type RenderWorker struct {
	renderService *service.RenderService
	music         client.MusicGenerator
	r2Client      client.StorageClient
	hub           *websocket.Hub
	indirici      *http.Client
	pollAraligi   time.Duration
}

// NewRenderWorker creates a new render worker
func NewRenderWorker(renderService *service.RenderService, music client.MusicGenerator, r2Client client.StorageClient, hub *websocket.Hub) *RenderWorker {
	return &RenderWorker{
		renderService: renderService,
		music:         music,
		r2Client:      r2Client,
		hub:           hub,
		indirici:      &http.Client{Timeout: 30 * time.Second},
		pollAraligi:   5 * time.Second,
	}
}
```

`ProcessTask` icindeki kontrol:

```go
	// Check if Suno client is configured
	if w.music == nil || !w.music.IsConfigured() {
		return w.processWithMock(ctx, jobID, &payload)
	}
```

`processWithSuno`'yu tamamen su iki fonksiyonla degistir:

```go
// processWithSuno handles real rendering using Suno API
func (w *RenderWorker) processWithSuno(ctx context.Context, jobID string, payload *model.RenderJobPayload) error {
	result, err := w.sunoIleUret(ctx, payload, func(p int, adim string) {
		w.updateProgress(ctx, jobID, p, adim)
	})
	if err != nil {
		w.failJob(ctx, jobID, err.Error())
		return err
	}

	if err := w.renderService.CompleteJob(ctx, jobID, result); err != nil {
		w.failJob(ctx, jobID, "Failed to save result")
		return err
	}

	// Yayin icin adresler taze uretilir; kayitta anahtar durur.
	resp, err := w.renderService.BuildResult(ctx, result)
	if err != nil {
		w.failJob(ctx, jobID, fmt.Sprintf("Stem URL uretilemedi: %v", err))
		return err
	}
	w.hub.BroadcastComplete(jobID, resp)
	log.Printf("Render job %s completed", jobID)
	return nil
}

// sunoIleUret, Suno'da uretip stem'leri R2'ye aktarir. renderService ve
// hub'a dokunmaz; ilerleme geri cagrimla bildirilir.
//
// Tekrar deneme: GenerateMusic basarili olunca kredi harcanmistir. Bundan
// sonraki her hata asynq.SkipRetry ile sarilir; aksi halde asynq isi bastan
// calistirip Suno'yu yeniden ucretlendirir (MaxRetry(3) ile 4 x 62 kredi).
func (w *RenderWorker) sunoIleUret(ctx context.Context, payload *model.RenderJobPayload, ilerleme func(int, string)) (*model.RenderResultRecord, error) {
	if w.r2Client == nil {
		return nil, fmt.Errorf("depolama yapilandirilmamis, Suno cagrilmadi: %w", asynq.SkipRetry)
	}

	ilerleme(5, "Building music prompt...")
	req := &client.GenerateMusicRequest{
		Style:        buildMusicStyle(payload),
		Title:        fmt.Sprintf("%s instrumental", payload.Brief.Genre),
		Instrumental: true,
	}

	ilerleme(10, "Generating music...")
	taskID, err := w.music.GenerateMusic(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("music generation failed: %w", err)
	}
	ucretli := func(ne string, err error) error {
		return fmt.Errorf("%s: %w: %w", ne, err, asynq.SkipRetry)
	}

	ilerleme(30, "Waiting for music generation...")
	track, err := w.music.WaitMusic(ctx, taskID, w.pollAraligi, 10*time.Minute)
	if err != nil {
		return nil, ucretli("music generation failed", err)
	}

	ilerleme(60, "Splitting stems...")
	stemTaskID, err := w.music.SplitStems(ctx, taskID, track.ID)
	if err != nil {
		return nil, ucretli("stem splitting failed", err)
	}

	ilerleme(75, "Waiting for stem separation...")
	stems, err := w.music.WaitStems(ctx, stemTaskID, w.pollAraligi, 5*time.Minute)
	if err != nil {
		return nil, ucretli("stem splitting failed", err)
	}
	if len(stems) == 0 {
		return nil, ucretli("stem splitting failed", errors.New("suno hic enstruman stem'i donmedi"))
	}

	ilerleme(90, "Uploading stems...")
	records, err := w.uploadStems(ctx, payload.ProjectID, stems, track.Duration)
	if err != nil {
		return nil, ucretli("stem upload failed", err)
	}

	ilerleme(95, "Finalizing...")
	return w.generateResult(payload, track.Duration, records), nil
}
```

`buildMusicPrompt` fonksiyonunu sil.

`uploadStems`'i degistir ve altina `stemAktar` ekle:

```go
// uploadStems, Suno stem'lerini indirip R2'ye yazar. Kayda imzali URL degil
// ANAHTAR yazilir; adres okuma aninda (RenderService.GetResult) taze uretilir.
func (w *RenderWorker) uploadStems(ctx context.Context, projectID string, stems []client.Stem, duration float64) ([]model.StemResultRecord, error) {
	results := make([]model.StemResultRecord, 0, len(stems))
	for _, stem := range stems {
		stemID := uuid.New().String()
		key := fmt.Sprintf("stems/%s/%s.mp3", projectID, stemID)
		if err := w.stemAktar(ctx, stem.URL, key); err != nil {
			return nil, fmt.Errorf("%s stem'i aktarilamadi: %w", stem.Instrument, err)
		}
		results = append(results, model.StemResultRecord{
			ID:           stemID,
			Instrument:   stem.Instrument,
			FileKey:      key,
			Duration:     duration,
			WaveformData: generateWaveform(100),
		})
	}
	return results, nil
}

// stemAktar, adresi indirip anahtara yukler. Govde bellege alinir: S3
// PutObject icerik uzunlugu ister ve tavani asan dosya yarim yuklenmemeli.
func (w *RenderWorker) stemAktar(ctx context.Context, adres, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adres, nil)
	if err != nil {
		return err
	}
	resp, err := w.indirici.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("indirme durumu %d", resp.StatusCode)
	}
	veri, err := io.ReadAll(io.LimitReader(resp.Body, stemBoyutTavani+1))
	if err != nil {
		return err
	}
	if len(veri) > stemBoyutTavani {
		return fmt.Errorf("stem %d bayt tavanini asiyor", stemBoyutTavani)
	}
	_, _, err = w.r2Client.Upload(ctx, key, bytes.NewReader(veri), "audio/mpeg")
	return err
}
```

`generateResult` imzasini ve govdesindeki `Duration` satirini degistir:

```go
func (w *RenderWorker) generateResult(payload *model.RenderJobPayload, duration float64, stems []model.StemResultRecord) *model.RenderResultRecord {
```

ve donen struct'ta:

```go
		Duration:  duration,
```

- [ ] **Step 6: Tum derlemeyi ve testleri dogrula**

Run: `go build ./... && go vet ./... && go test ./internal/... && gofmt -l internal cmd`
Expected: derleme temiz (`cmd/server/main.go` `*client.SunoClient`'i arayuz olarak gecirir; `e2e/render_real_test.go` de ayni), tum paketler `ok`, gofmt bos. `main.go` derlenmezse yalnizca `sunoClient`'in eski metot cagrilarini (ornegin health'te `IsConfigured` disi bir sey) duzelt; `IsConfigured` korunuyor.

- [ ] **Step 7: Commit**

```bash
git add core-service/internal/worker core-service/cmd/server/main.go
git commit -m "feat(render): Suno yolu gercek API ile, stem'ler R2'ye, ucret sonrasi tekrar yok

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Belgeler ve son dogrulama

**Files:**
- Modify: `make-singer-backend/CLAUDE.md` (External services tablosundaki Suno satiri ve `worker/` aciklamasi)

**Interfaces:**
- Consumes: Task 1-3'un tamami.
- Produces: yok.

- [ ] **Step 1: CLAUDE.md'yi guncelle**

`worker/` maddesini su hale getir:

```markdown
- **worker/** — Asynq job processors: `render_worker.go` (Suno `/api/v1/generate` + `split_stem`, stems downloaded and uploaded to R2 as `stems/{projectId}/{stemId}.mp3`; any failure after the paid generate call is wrapped in `asynq.SkipRetry` so a retry never re-bills Suno), `master_worker.go` (audio mastering pipeline).
```

External services tablosundaki Suno satiri:

```markdown
| Suno (`api.sunoapi.org`, model `SUNO_MODEL`, default `V6`) | Instrumental rendering + 12-stem split (12 + 50 credits per render) | Simulated render steps |
```

- [ ] **Step 2: Tam dogrulama**

Run: `go build ./... && go vet ./... && go test ./internal/... -count=1 && gofmt -l internal cmd`
Expected: tum paketler `ok`, gofmt bos.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: Suno render akisi ve tekrar deneme kurali

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
