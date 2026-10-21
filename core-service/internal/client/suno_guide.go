package client

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// GuideGenerator is what the guided render needs from Suno on top of
// MusicGenerator: a guide singer over an existing instrumental, its vocal
// separated, and its word timings. Add-vocals results are polled with
// WaitMusic (same record-info endpoint).
type GuideGenerator interface {
	AddVocals(ctx context.Context, req *AddVocalsRequest) (string, error)
	WaitMusic(ctx context.Context, taskID string, interval, maxWait time.Duration) (*Track, error)
	SeparateVocal(ctx context.Context, taskID, audioID string) (string, error)
	WaitSeparated(ctx context.Context, separationTaskID string, interval, maxWait time.Duration) (*SeparatedVocal, error)
	TimestampedLyrics(ctx context.Context, taskID, audioID string) ([]AlignedWord, error)
}

// VocalSeparator is what the vocal render needs from Suno on top of
// MusicGenerator: a vocal take separated from a track or an uploaded
// recording, and the word timings of a sung track.
type VocalSeparator interface {
	SeparateVocal(ctx context.Context, taskID, audioID string) (string, error)
	SeparateVocalURL(ctx context.Context, audioURL string) (string, error)
	WaitSeparated(ctx context.Context, separationTaskID string, interval, maxWait time.Duration) (*SeparatedVocal, error)
	TimestampedLyrics(ctx context.Context, taskID, audioID string) ([]AlignedWord, error)
}

var _ GuideGenerator = (*SunoClient)(nil)
var _ VocalSeparator = (*SunoClient)(nil)

// guideNegativeTags keeps the guide sung: add-vocals rejects an empty value.
const guideNegativeTags = "rap, spoken word"

// AddVocalsRequest asks Suno to sing lyrics over the instrumental at UploadURL.
type AddVocalsRequest struct {
	UploadURL   string
	Style       string
	Title       string
	Lyrics      string
	VocalGender string
}

// SeparatedVocal is the separate_vocal result. The URLs expire.
type SeparatedVocal struct {
	VocalURL        string
	InstrumentalURL string
}

// AlignedWord is one word (sometimes with a section tag or a line break)
// of get-timestamped-lyrics.
type AlignedWord struct {
	Word    string  `json:"word"`
	StartS  float64 `json:"startS"`
	EndS    float64 `json:"endS"`
	Success bool    `json:"success"`
}

// AddVocals starts a guide singer over the instrumental and returns the task
// id. If this call succeeds, credits have been spent.
func (c *SunoClient) AddVocals(ctx context.Context, req *AddVocalsRequest) (string, error) {
	body := map[string]any{
		"uploadUrl":    req.UploadURL,
		"title":        req.Title,
		"negativeTags": guideNegativeTags,
		"style":        req.Style,
		"lyrics":       req.Lyrics,
		"vocalGender":  req.VocalGender,
		"model":        c.model,
		"callBackUrl":  c.callbackURL,
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	if err := c.post(ctx, "/api/v1/generate/add-vocals", body, &data); err != nil {
		return "", err
	}
	if data.TaskID == "" {
		return "", fmt.Errorf("suno: no taskId in add-vocals response")
	}
	return data.TaskID, nil
}

// SeparateVocal splits a track into vocal and instrumental (separate_vocal).
func (c *SunoClient) SeparateVocal(ctx context.Context, taskID, audioID string) (string, error) {
	body := map[string]any{
		"taskId":      taskID,
		"audioId":     audioID,
		"type":        "separate_vocal",
		"callBackUrl": c.callbackURL,
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	if err := c.post(ctx, "/api/v1/vocal-removal/generate", body, &data); err != nil {
		return "", err
	}
	if data.TaskID == "" {
		return "", fmt.Errorf("suno: no taskId in vocal separation response")
	}
	return data.TaskID, nil
}

// SeparateVocalURL splits an arbitrary audio URL into vocal and instrumental
// (separate_vocal, no taskId/audioId). Poll with WaitSeparated.
func (c *SunoClient) SeparateVocalURL(ctx context.Context, audioURL string) (string, error) {
	body := map[string]any{
		"audioUrl":    audioURL,
		"type":        "separate_vocal",
		"callBackUrl": c.callbackURL,
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	if err := c.post(ctx, "/api/v1/vocal-removal/generate", body, &data); err != nil {
		return "", err
	}
	if data.TaskID == "" {
		return "", fmt.Errorf("suno: no taskId in vocal separation response")
	}
	return data.TaskID, nil
}

// WaitSeparated polls until separate_vocal is SUCCESS.
func (c *SunoClient) WaitSeparated(ctx context.Context, separationTaskID string, interval, maxWait time.Duration) (*SeparatedVocal, error) {
	var result *SeparatedVocal
	err := c.waitFor(ctx, interval, maxWait, "vocal separation", func() (bool, error) {
		var data struct {
			SuccessFlag  string `json:"successFlag"`
			ErrorMessage string `json:"errorMessage"`
			Response     *struct {
				VocalURL        string `json:"vocalUrl"`
				InstrumentalURL string `json:"instrumentalUrl"`
			} `json:"response"`
		}
		if err := c.get(ctx, "/api/v1/vocal-removal/record-info?taskId="+url.QueryEscape(separationTaskID), &data); err != nil {
			return false, transientError{err}
		}
		if sunoFailureStatuses[data.SuccessFlag] {
			return false, fmt.Errorf("suno vocal separation failed (%s): %s", data.SuccessFlag, data.ErrorMessage)
		}
		if data.SuccessFlag != "SUCCESS" {
			return false, nil
		}
		if data.Response == nil || data.Response.VocalURL == "" {
			return false, fmt.Errorf("suno: SUCCESS but no vocalUrl")
		}
		result = &SeparatedVocal{VocalURL: data.Response.VocalURL, InstrumentalURL: data.Response.InstrumentalURL}
		return true, nil
	})
	return result, err
}

// TimestampedLyrics returns the word timings of a sung track.
func (c *SunoClient) TimestampedLyrics(ctx context.Context, taskID, audioID string) ([]AlignedWord, error) {
	var data struct {
		AlignedWords []AlignedWord `json:"alignedWords"`
	}
	if err := c.post(ctx, "/api/v1/generate/get-timestamped-lyrics", map[string]any{"taskId": taskID, "audioId": audioID}, &data); err != nil {
		return nil, err
	}
	return data.AlignedWords, nil
}
