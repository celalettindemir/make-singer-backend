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
	"github.com/makeasinger/api/internal/model"
)

// MusicGenerator, render worker'in Suno'dan bekledigi islemler.
type MusicGenerator interface {
	GenerateMusic(ctx context.Context, req *GenerateMusicRequest) (string, error)
	WaitMusic(ctx context.Context, taskID string, interval, maxWait time.Duration) (*Track, error)
	SplitStems(ctx context.Context, taskID, audioID string) (string, error)
	WaitStems(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error)
	IsolateStem(ctx context.Context, taskID, audioID, stemName string) (string, error)
	WaitIsolated(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) (*IsolatedStem, error)
	IsConfigured() bool
}

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

// Stem, indirilmeye hazir tek bir enstruman kanali. Adres sureli; hemen
// indirilmeli.
type Stem struct {
	Instrument model.Instrument
	URL        string
}

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

// stemAlanlari, split_stem yanitindaki camelCase alanlarin Instrument
// karsiliklari. Sira sonuc sirasidir. vocalUrl ve backingVocalsUrl yok:
// uretim instrumental ve mobil enum'u vokali tanimaz. woodwindsUrl de
// ayni nedenle atlanir: mobil enstruman enum'u woodwinds'i bilmez.
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
		if p.ID == "" {
			return false, fmt.Errorf("suno: SUCCESS ama parca kimligi yok")
		}
		sonuc = &Track{ID: p.ID, AudioURL: p.AudioURL, Duration: p.Duration}
		return true, nil
	})
	return sonuc, err
}

// SplitStems, uretilen parcayi 12 kanala boler (split_stem, 50 kredi);
// 10 enstruman stem'i tutulur (bkz. stemAlanlari).
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

// geciciHata, bekle'nin tolere ettigi hata sinifi (ag, 5xx, bozuk yanit).
type geciciHata struct{ error }

func (e geciciHata) Unwrap() error { return e.error }

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
