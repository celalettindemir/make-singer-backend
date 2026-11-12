package client

import (
	"context"
	"errors"
	"fmt"
)

// UploadCoverRequest asks Suno to rebuild an uploaded audio file (the user's
// hum) as a song. Style carries no BPM or key: the melody sets them.
type UploadCoverRequest struct {
	UploadURL   string
	Style       string
	Title       string
	Lyrics      string
	VocalGender string
	AudioWeight float64
	StyleWeight float64
}

// CoverGenerator is the optional Suno capability the hum render needs.
// Callers type-assert for it so music fakes without it need no change.
type CoverGenerator interface {
	UploadCover(ctx context.Context, req *UploadCoverRequest) (string, error)
}

// ErrCoverRejected marks a permanent Suno rejection of an upload-cover call
// (bad request, file too large or unprocessable). Nothing was charged and a
// retry would be rejected again.
var ErrCoverRejected = errors.New("suno rejected the cover request")

var _ CoverGenerator = (*SunoClient)(nil)

// UploadCover starts an Upload and Cover generation and returns the task id.
// The result is waited for with WaitMusic (same record-info endpoint). If
// this call succeeds, credits have been spent.
func (c *SunoClient) UploadCover(ctx context.Context, req *UploadCoverRequest) (string, error) {
	body := map[string]any{
		"uploadUrl":    req.UploadURL,
		"customMode":   true,
		"instrumental": false,
		"model":        c.model,
		"callBackUrl":  c.callbackURL,
		"style":        req.Style,
		"title":        req.Title,
		"lyrics":       req.Lyrics,
		"audioWeight":  req.AudioWeight,
		"styleWeight":  req.StyleWeight,
	}
	if req.VocalGender != "" {
		body["vocalGender"] = req.VocalGender
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	if err := c.post(ctx, "/api/v1/generate/upload-cover", body, &data); err != nil {
		var apiErr *SunoAPIError
		if errors.As(err, &apiErr) && apiErr.permanent() {
			return "", fmt.Errorf("%w: %w", ErrCoverRejected, err)
		}
		return "", err
	}
	if data.TaskID == "" {
		return "", fmt.Errorf("suno: no taskId in response")
	}
	return data.TaskID, nil
}
