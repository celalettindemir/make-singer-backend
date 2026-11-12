package client

import "context"

// HumPrepareRequest is the audio-service /mirilti-hazirla request. Keys are R2
// object keys: the uploaded source and the mp3 to write.
type HumPrepareRequest struct {
	InputKey  string `json:"input_key"`
	OutputKey string `json:"output_key"`
}

// HumPrepareResponse: the mp3 is already written to OutputKey.
type HumPrepareResponse struct {
	DurationMs int `json:"duration_ms"`
}

// HumPreparer is what the hum upload needs from audio-service. It is kept
// separate from AudioProcessor so existing fakes do not break.
type HumPreparer interface {
	PrepareHum(ctx context.Context, req *HumPrepareRequest) (*HumPrepareResponse, error)
}

var _ HumPreparer = (*AudioClient)(nil)

// PrepareHum calls /mirilti-hazirla.
func (c *AudioClient) PrepareHum(ctx context.Context, req *HumPrepareRequest) (*HumPrepareResponse, error) {
	var result HumPrepareResponse
	if err := c.post(ctx, "/mirilti-hazirla", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
