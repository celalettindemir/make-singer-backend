package client

import "context"

// AnalysisRequest is the audio-service /analiz request. The keys are R2 object
// keys; the service reads the file through the S3 API and writes the preview
// to PreviewKey. No signed URL is carried.
type AnalysisRequest struct {
	InputKey       string `json:"input_key"`
	PreviewKey     string `json:"preview_key"`
	PeakCount      int    `json:"peak_count"`
	PreviewSeconds int    `json:"preview_seconds"`
}

// AnalysisResponse: Peaks is PeakCount values normalized to the 0-1 range
// against the highest peak; PreviewURL is not used on the Go side (the key is
// already known), it only confirms that the service wrote the file.
type AnalysisResponse struct {
	Peaks      []float64 `json:"peaks"`
	Duration   float64   `json:"duration"`
	PreviewURL string    `json:"preview_url"`
}

// AudioAnalyzer is the capability that produces a waveform and a preview. It
// is kept separate from AudioProcessor so existing AudioProcessor fakes do not
// break.
type AudioAnalyzer interface {
	Analyze(ctx context.Context, req *AnalysisRequest) (*AnalysisResponse, error)
}

var _ AudioAnalyzer = (*AudioClient)(nil)

// Analyze calls the /analiz endpoint.
func (c *AudioClient) Analyze(ctx context.Context, req *AnalysisRequest) (*AnalysisResponse, error) {
	var result AnalysisResponse
	if err := c.post(ctx, "/analiz", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
