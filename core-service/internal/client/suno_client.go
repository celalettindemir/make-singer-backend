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

// MusicGenerator is the set of operations the render worker expects from Suno.
type MusicGenerator interface {
	GenerateMusic(ctx context.Context, req *GenerateMusicRequest) (string, error)
	WaitMusic(ctx context.Context, taskID string, interval, maxWait time.Duration) (*Track, error)
	SplitStems(ctx context.Context, taskID, audioID string) (string, error)
	WaitStems(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error)
	IsolateStem(ctx context.Context, taskID, audioID, stemName string) (string, error)
	WaitIsolated(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) (*IsolatedStem, error)
	IsConfigured() bool
}

// SunoClient is the api.sunoapi.org client. The provider returns every response
// in a {code, msg, data} envelope; code != 200 is an error even on HTTP 200.
type SunoClient struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	model       string
	callbackURL string
}

// GenerateMusicRequest is the worker's generation request. customMode, model
// and callBackUrl are added by the client.
type GenerateMusicRequest struct {
	Style        string
	Title        string
	Instrumental bool
}

// Track is the first track of a completed generation. ID is the audioId when
// splitting stems.
type Track struct {
	ID       string
	AudioURL string
	Duration float64
}

// Stem is a single instrument channel ready to download. The URL expires, so
// it must be downloaded right away.
type Stem struct {
	Instrument model.Instrument
	URL        string
}

// IsolatedStem is the split_stem_advanced result: the selected instrument
// (Extract) and the track with that instrument removed (Remove). The URLs
// expire and may contain spaces; net/http encodes them in the request.
type IsolatedStem struct {
	ExtractURL string
	RemoveURL  string
	Duration   float64
}

// sunoStemNames maps our instruments to the provider's stemName enum values
// (verbatim; case and spaces matter).
var sunoStemNames = map[model.Instrument]string{
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

// SunoStemName returns the instrument's split_stem_advanced stemName.
func SunoStemName(i model.Instrument) (string, bool) {
	name, ok := sunoStemNames[i]
	return name, ok
}

// stemFields maps the camelCase fields of the split_stem response to
// Instrument. The order is the result order. There is no vocalUrl or
// backingVocalsUrl: generation is instrumental and the mobile enum does not
// know vocals. woodwindsUrl is skipped for the same reason: the mobile
// instrument enum does not know woodwinds.
var stemFields = []struct {
	field      string
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

// sunoFailureStatuses are the failure statuses that end the wait immediately.
var sunoFailureStatuses = map[string]bool{
	"CREATE_TASK_FAILED":    true,
	"GENERATE_AUDIO_FAILED": true,
	"CALLBACK_EXCEPTION":    true,
	"SENSITIVE_WORD_ERROR":  true,
}

// pollErrorTolerance: how many consecutive transient errors are swallowed
// while polling. A single network error must not waste the job after credits
// have been spent.
const pollErrorTolerance = 3

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

// GenerateMusic starts a generation in custom mode and returns the task id.
// If this call succeeds, credits have been spent.
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
		return "", fmt.Errorf("suno: no taskId in response")
	}
	return data.TaskID, nil
}

// WaitMusic polls until the generation is SUCCESS and returns the first track.
func (c *SunoClient) WaitMusic(ctx context.Context, taskID string, interval, maxWait time.Duration) (*Track, error) {
	var result *Track
	err := c.waitFor(ctx, interval, maxWait, "music", func() (bool, error) {
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
			return false, transientError{err}
		}
		if sunoFailureStatuses[data.Status] {
			return false, fmt.Errorf("suno generation failed (%s): %s", data.Status, data.ErrorMessage)
		}
		if data.Status != "SUCCESS" {
			return false, nil
		}
		if data.Response == nil || len(data.Response.SunoData) == 0 {
			return false, fmt.Errorf("suno: SUCCESS but no track")
		}
		p := data.Response.SunoData[0]
		if p.ID == "" {
			return false, fmt.Errorf("suno: SUCCESS but no track ID")
		}
		result = &Track{ID: p.ID, AudioURL: p.AudioURL, Duration: p.Duration}
		return true, nil
	})
	return result, err
}

// SplitStems splits the generated track into 12 channels (split_stem, 50
// credits); 10 instrument stems are kept (see stemFields).
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
		return "", fmt.Errorf("suno: no taskId in stem response")
	}
	return data.TaskID, nil
}

// WaitStems polls until the stem split is SUCCESS and returns the instrument
// channels. Empty fields are skipped.
func (c *SunoClient) WaitStems(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) ([]Stem, error) {
	var result []Stem
	err := c.waitFor(ctx, interval, maxWait, "stem", func() (bool, error) {
		var data struct {
			SuccessFlag  string                     `json:"successFlag"`
			ErrorMessage string                     `json:"errorMessage"`
			Response     map[string]json.RawMessage `json:"response"`
		}
		if err := c.get(ctx, "/api/v1/vocal-removal/record-info?taskId="+url.QueryEscape(stemTaskID), &data); err != nil {
			return false, transientError{err}
		}
		if sunoFailureStatuses[data.SuccessFlag] {
			return false, fmt.Errorf("suno stem split failed (%s): %s", data.SuccessFlag, data.ErrorMessage)
		}
		if data.SuccessFlag != "SUCCESS" {
			return false, nil
		}
		for _, f := range stemFields {
			var stemURL string
			if raw, ok := data.Response[f.field]; ok {
				_ = json.Unmarshal(raw, &stemURL) // null and non-string values stay empty
			}
			if stemURL != "" {
				result = append(result, Stem{Instrument: f.instrument, URL: stemURL})
			}
		}
		return true, nil
	})
	return result, err
}

// transientError is the error class waitFor tolerates (network, 5xx, malformed
// response).
type transientError struct{ error }

func (e transientError) Unwrap() error { return e.error }

// IsolateStem separates a single instrument from the track
// (split_stem_advanced, 20 credits). The response returns both the instrument
// and the track without it.
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
		return "", fmt.Errorf("suno: no taskId in separation response")
	}
	return data.TaskID, nil
}

// WaitIsolated polls until split_stem_advanced is SUCCESS. The provider
// returns one originData element for each variant of the generation; we use
// the first variant (the sunoData[0] that WaitMusic returns).
func (c *SunoClient) WaitIsolated(ctx context.Context, stemTaskID string, interval, maxWait time.Duration) (*IsolatedStem, error) {
	type part struct {
		AudioURL string  `json:"audio_url"`
		Duration float64 `json:"duration"`
	}
	var result *IsolatedStem
	err := c.waitFor(ctx, interval, maxWait, "separation", func() (bool, error) {
		var data struct {
			SuccessFlag  string `json:"successFlag"`
			ErrorMessage string `json:"errorMessage"`
			Response     *struct {
				OriginData []struct {
					Extract part `json:"extract"`
					Remove  part `json:"remove"`
				} `json:"originData"`
			} `json:"response"`
		}
		if err := c.get(ctx, "/api/v1/vocal-removal/record-info?taskId="+url.QueryEscape(stemTaskID), &data); err != nil {
			return false, transientError{err}
		}
		if sunoFailureStatuses[data.SuccessFlag] {
			return false, fmt.Errorf("suno separation failed (%s): %s", data.SuccessFlag, data.ErrorMessage)
		}
		if data.SuccessFlag != "SUCCESS" {
			return false, nil
		}
		if data.Response == nil || len(data.Response.OriginData) == 0 {
			return false, fmt.Errorf("suno: SUCCESS but no originData")
		}
		first := data.Response.OriginData[0]
		if first.Extract.AudioURL == "" || first.Remove.AudioURL == "" {
			return false, fmt.Errorf("suno: SUCCESS but separation URL missing")
		}
		result = &IsolatedStem{ExtractURL: first.Extract.AudioURL, RemoveURL: first.Remove.AudioURL, Duration: first.Extract.Duration}
		return true, nil
	})
	return result, err
}

// waitFor calls check at interval spacing until it returns true. check's
// transientError is swallowed up to pollErrorTolerance times in a row; other
// errors return immediately.
func (c *SunoClient) waitFor(ctx context.Context, interval, maxWait time.Duration, label string, check func() (bool, error)) error {
	deadline := time.Now().Add(maxWait)
	consecutiveErrors := 0
	for time.Now().Before(deadline) {
		done, err := check()
		if err != nil {
			var transient transientError
			if !asTransient(err, &transient) {
				return err
			}
			consecutiveErrors++
			log.Printf("[Suno API] %s poll transient error %d/%d: %v", label, consecutiveErrors, pollErrorTolerance, err)
			if consecutiveErrors >= pollErrorTolerance {
				return fmt.Errorf("suno %s poll abandoned after %d consecutive errors: %w", label, consecutiveErrors, transient.error)
			}
		} else {
			consecutiveErrors = 0
			if done {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("suno %s wait timed out (%v)", label, maxWait)
}

func asTransient(err error, target *transientError) bool {
	te, ok := err.(transientError)
	if ok {
		*target = te
	}
	return ok
}

// post sends a POST with a JSON body and decodes the envelope's data field into
// out.
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

// get sends a GET and decodes the envelope's data field into out.
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
		return fmt.Errorf("suno request could not be sent: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("suno response could not be read: %w", err)
	}
	log.Printf("[Suno API] %d %s %s", resp.StatusCode, req.Method, req.URL.Path)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("suno API error (status %d): %s", resp.StatusCode, string(raw))
	}

	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("suno response could not be decoded: %w", err)
	}
	if envelope.Code != 200 {
		return fmt.Errorf("suno API error (code %d): %s", envelope.Code, envelope.Msg)
	}
	if out == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("suno data field could not be decoded: %w", err)
	}
	return nil
}
