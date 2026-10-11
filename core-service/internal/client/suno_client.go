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
