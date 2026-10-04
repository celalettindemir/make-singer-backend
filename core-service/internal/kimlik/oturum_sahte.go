package kimlik

import (
	"context"
	"sync"
	"time"
)

// SahteOturumDepo, OturumDepo'nun bellek ici karsiligidir. Amaci
// testlerin AG OLMADAN (Redis'siz) cerez baglamasini olcebilmesi;
// kullanici/jeton depolarindaki sahte karsiliklarla ayni kalip.
type SahteOturumDepo struct {
	mu       sync.Mutex
	kayitlar map[string]sahteOturumKaydi
}

type sahteOturumKaydi struct {
	baglama OturumBaglama
	biter   time.Time
}

func NewSahteOturumDepo() *SahteOturumDepo {
	return &SahteOturumDepo{kayitlar: map[string]sahteOturumKaydi{}}
}

var _ OturumDepo = (*SahteOturumDepo)(nil)

func (d *SahteOturumDepo) OturumYaz(_ context.Context, oturum string, baglama OturumBaglama, ttl time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kayitlar[oturum] = sahteOturumKaydi{baglama: baglama, biter: time.Now().Add(ttl)}
	return nil
}

func (d *SahteOturumDepo) OturumOku(_ context.Context, oturum string) (OturumBaglama, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	kayit, varMi := d.kayitlar[oturum]
	if !varMi || time.Now().After(kayit.biter) {
		return OturumBaglama{}, ErrOturumYok
	}
	return kayit.baglama, nil
}
