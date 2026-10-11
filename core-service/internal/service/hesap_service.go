package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/hesapisaret"
	"github.com/makeasinger/api/internal/kimlik"
	"github.com/redis/go-redis/v9"
)

// TaskTypeHesapTemizle, silinen hesabin R2 oneklerini gecikmeli olarak
// bir kez daha temizleyen gorevdir.
const TaskTypeHesapTemizle = "hesap:temizle"

// ErrHesapYok, silinecek hesap bulunamadi (ya da id gecersiz).
var ErrHesapYok = errors.New("hesap bulunamadi")

// gecikmeliTemizlik, ikinci R2 temizliginin silmeden ne kadar sonra
// kosacagidir. En uzun is denemesinden (render varsayilan 30dk zaman
// asimi) fazlasiyla uzundur. Silmeden sonraki denemeler zaten hicbir sey
// yuklemez: isciler is sahibinin silinmis isaretini (24 saat yasar)
// ucretli cagridan ve her R2 yaziminda once denetler.
const gecikmeliTemizlik = time.Hour

// KimlikSilici, kimlik saglayicisinin hesap silme yetenegi
// (*kimlik.Sunucu uygular).
type KimlikSilici interface {
	HesapSil(ctx context.Context, userID string) error
}

// Kuyruk, *asynq.Client'in burada kullanilan yuzu.
type Kuyruk interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

type HesapService struct {
	kimlik KimlikSilici
	silici client.OnekSilici // nil: R2 yapilandirilmamis
	redis  *redis.Client
	kuyruk Kuyruk
}

func NewHesapService(kimlikSilici KimlikSilici, silici client.OnekSilici, rdb *redis.Client, kuyruk Kuyruk) *HesapService {
	return &HesapService{kimlik: kimlikSilici, silici: silici, redis: rdb, kuyruk: kuyruk}
}

// Sil, hesabi ve tum dosyalarini siler. Kimlik kaydi silindikten sonra
// kullanici istegi tekrarlayamaz (token'i artik reddedilir); bu yuzden
// sonraki adimlarin hatalari loglanir ama istegi basarisiz kilmaz.
// Eksik kalan R2 temizligini gecikmeli gorev tamamlar.
func (s *HesapService) Sil(ctx context.Context, userID string) error {
	if _, err := client.KullaniciOnekleri(userID); err != nil {
		return ErrHesapYok
	}
	if err := s.kimlik.HesapSil(ctx, userID); err != nil {
		if errors.Is(err, kimlik.ErrKullaniciYok) {
			return ErrHesapYok
		}
		return fmt.Errorf("hesap silinemedi: %w", err)
	}

	if err := s.redis.Set(ctx, hesapisaret.Anahtar(userID), "1", hesapisaret.Omur).Err(); err != nil {
		log.Printf("Uyari: silinmis isareti yazilamadi (kullanici %s): %v", userID, err)
	}
	if err := s.Temizle(ctx, userID); err != nil {
		log.Printf("Uyari: hesap R2 temizligi eksik kaldi, gecikmeli gorev tamamlayacak (kullanici %s): %v", userID, err)
	}

	yuk, err := json.Marshal(map[string]string{"userId": userID})
	if err != nil {
		log.Printf("Uyari: temizlik gorevi hazirlanamadi (kullanici %s): %v", userID, err)
		return nil
	}
	if _, err := s.kuyruk.Enqueue(asynq.NewTask(TaskTypeHesapTemizle, yuk),
		asynq.Queue("bakim"),
		asynq.ProcessIn(gecikmeliTemizlik),
		asynq.MaxRetry(10),
	); err != nil {
		log.Printf("Uyari: gecikmeli temizlik gorevi kurulamadi (kullanici %s): %v", userID, err)
	}
	return nil
}

// Temizle, kullanicinin tum R2 oneklerini siler. R2 yoksa bir sey
// yapmaz. Bir onek basarisiz olsa da digerleri denenir.
func (s *HesapService) Temizle(ctx context.Context, userID string) error {
	if s.silici == nil {
		return nil
	}
	onekler, err := client.KullaniciOnekleri(userID)
	if err != nil {
		return err
	}
	var hatalar []error
	for _, onek := range onekler {
		if _, err := s.silici.DeletePrefix(ctx, onek); err != nil {
			hatalar = append(hatalar, fmt.Errorf("%s: %w", onek, err))
		}
	}
	return errors.Join(hatalar...)
}
