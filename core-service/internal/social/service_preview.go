package social

import (
	"context"
	"fmt"
	"time"

	"github.com/makeasinger/api/internal/client"
)

// onizlemeOmru, onizlemeUrl imzasinin gecerlilik suresi (spec §4.8: 1
// saat). R2_PRESIGN_TTL'den bagimsizdir: sozlesme sabit 1 saat der.
const onizlemeOmru = time.Hour

// Onizleme, GET /api/public/s/{slug} yaniti. OnizlemeURL onizleme
// uretilemediyse null olarak GORUNUR; DalgaFormu hicbir zaman null
// degildir.
type Onizleme struct {
	Baslik      string      `json:"baslik"`
	Sahip       ProfilOzeti `json:"sahip"`
	SureSn      int         `json:"sureSn"`
	DalgaFormu  []float64   `json:"dalgaFormu"`
	OnizlemeURL *string     `json:"onizlemeUrl"`
	Tur         string      `json:"tur"`
}

// OnizlemeKaydi, depodan okunan onizleme ve ic alanlari.
type OnizlemeKaydi struct {
	Onizleme
	SahipID          string
	OnizlemeAnahtari string // "" ise onizleme yok
}

// Onizleme, kimliksiz onizlemeyi doner. Gecersiz bicimli slug, takipciler
// yayini, hazirlanan, hatali ya da silinmis yayin ErrBulunamadi'dir.
func (s *Servis) Onizleme(ctx context.Context, slug string) (*Onizleme, error) {
	if !SlugGecerli(slug) {
		return nil, ErrNotFound
	}
	k, err := s.depo.GetPreview(ctx, slug)
	if err != nil {
		return nil, err
	}
	o := k.Onizleme
	if k.OnizlemeAnahtari != "" {
		url, err := s.onizlemeAdresi(ctx, k.OnizlemeAnahtari)
		if err != nil {
			return nil, err
		}
		o.OnizlemeURL = &url
	}
	return &o, nil
}

// onizlemeAdresi, 1 saat gecerli imzali adres uretir; R2 yoksa (dev)
// imzasiz adres doner.
func (s *Servis) onizlemeAdresi(ctx context.Context, key string) (string, error) {
	if s.depolama == nil {
		return client.UnsignedURL(key, s.r2), nil
	}
	url, err := s.depolama.GetSignedURL(ctx, key, onizlemeOmru)
	if err != nil {
		return "", fmt.Errorf("onizleme adresi imzalanamadi: %w", err)
	}
	return url, nil
}
