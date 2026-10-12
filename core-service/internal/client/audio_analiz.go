package client

import "context"

// AnalizRequest, audio-service /analiz istegidir. Anahtarlar R2 nesne
// anahtaridir; servis dosyayi S3 API ile okur ve onizlemeyi PreviewKey'e
// yazar. Imzali adres tasinmaz.
type AnalizRequest struct {
	InputKey       string `json:"input_key"`
	PreviewKey     string `json:"preview_key"`
	PeakCount      int    `json:"peak_count"`
	PreviewSeconds int    `json:"preview_seconds"`
}

// AnalizResponse: Peaks en yuksek tepeye gore 0-1 araliginda normalize
// edilmis PeakCount degerdir; PreviewURL Go tarafinda kullanilmaz (anahtar
// zaten bilinir), yalnizca servisin yazdigini teyit eder.
type AnalizResponse struct {
	Peaks      []float64 `json:"peaks"`
	Duration   float64   `json:"duration"`
	PreviewURL string    `json:"preview_url"`
}

// AudioAnalizci, dalga formu ve onizleme ureten yetenek. AudioProcessor'dan
// ayri tutulur ki mevcut AudioProcessor sahteleri kirilmasin.
type AudioAnalizci interface {
	Analiz(ctx context.Context, req *AnalizRequest) (*AnalizResponse, error)
}

var _ AudioAnalizci = (*AudioClient)(nil)

// Analiz, /analiz ucunu cagirir.
func (c *AudioClient) Analiz(ctx context.Context, req *AnalizRequest) (*AnalizResponse, error) {
	var result AnalizResponse
	if err := c.post(ctx, "/analiz", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
