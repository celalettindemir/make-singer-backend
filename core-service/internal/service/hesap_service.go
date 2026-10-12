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
	"github.com/makeasinger/api/internal/deletionmark"
	"github.com/makeasinger/api/internal/identity"
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
	DeleteAccount(ctx context.Context, userID string) error
}

// Kuyruk, *asynq.Client'in burada kullanilan yuzu.
type Kuyruk interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// SosyalTemizleyici, Kesfet verisinin hesap silmede temizlenen yuzu
// (*sosyal.PostgresDepo uygular).
type SosyalTemizleyici interface {
	// YayinlariListele, yumusak silinmisler dahil tum yayin kimlikleri.
	YayinlariListele(ctx context.Context, userID string) ([]string, error)
	// KullaniciIzleriniSil, yayinlar DISINDAKI her seyi (profil, begeni,
	// yorum, takip, sikayet) tek islemde siler. Okuma yollari profile
	// baglandigi icin kullanici aninda gorunmez olur.
	KullaniciIzleriniSil(ctx context.Context, userID string) error
	// KullaniciVerisiniSil, profil, yayin, begeni, yorum, takip ve
	// sikayetleri tek islemde siler.
	KullaniciVerisiniSil(ctx context.Context, userID string) error
}

type HesapService struct {
	kimlik KimlikSilici
	silici client.PrefixDeleter // nil: R2 yapilandirilmamis
	redis  *redis.Client
	kuyruk Kuyruk
	sosyal SosyalTemizleyici // nil: sosyal modul yok
}

func NewHesapService(kimlikSilici KimlikSilici, silici client.PrefixDeleter, rdb *redis.Client, kuyruk Kuyruk, sosyal SosyalTemizleyici) *HesapService {
	return &HesapService{kimlik: kimlikSilici, silici: silici, redis: rdb, kuyruk: kuyruk, sosyal: sosyal}
}

// Sil, hesabi ve tum dosyalarini siler. Kimlik kaydi silindikten sonra
// kullanici istegi tekrarlayamaz (token'i artik reddedilir); bu yuzden
// sonraki adimlarin hatalari loglanir ama istegi basarisiz kilmaz.
// Eksik kalan R2 temizligini gecikmeli gorev tamamlar.
func (s *HesapService) Sil(ctx context.Context, userID string) error {
	if _, err := client.UserPrefixes(userID); err != nil {
		return ErrHesapYok
	}
	if err := s.kimlik.DeleteAccount(ctx, userID); err != nil {
		if errors.Is(err, identity.ErrUserNotFound) {
			return ErrHesapYok
		}
		return fmt.Errorf("hesap silinemedi: %w", err)
	}

	if err := s.redis.Set(ctx, deletionmark.Key(userID), "1", deletionmark.TTL).Err(); err != nil {
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

// Temizle, kullanicinin tum R2 oneklerini ve Kesfet verisini siler. R2
// yoksa onekler atlanir. Bir onek basarisiz olsa da digerleri denenir.
func (s *HesapService) Temizle(ctx context.Context, userID string) error {
	onekler, err := client.UserPrefixes(userID)
	if err != nil {
		return err
	}
	var hatalar []error
	if s.silici != nil {
		for _, onek := range onekler {
			if _, err := s.silici.DeletePrefix(ctx, onek); err != nil {
				hatalar = append(hatalar, fmt.Errorf("%s: %w", onek, err))
			}
		}
	}
	if s.sosyal != nil {
		if err := s.sosyalTemizle(ctx, userID); err != nil {
			hatalar = append(hatalar, err)
		}
	}
	return errors.Join(hatalar...)
}

// sosyalTemizle, once profili ve diger izleri siler (kullanici R2 kesintisi
// suresince de gorunmez olur), sonra her yayinin sosyal/{id}/ onekini,
// en son yayin satirlarini siler. Bir onek bile basarisizsa yayin
// satirlari KALIR: gecikmeli gorev ayni kimlikleri yeniden listeleyip
// temizligi tamamlar (idempotent). Iz silme hatasi R2 dongusunu durdurmaz.
func (s *HesapService) sosyalTemizle(ctx context.Context, userID string) error {
	var hatalar []error
	if err := s.sosyal.KullaniciIzleriniSil(ctx, userID); err != nil {
		hatalar = append(hatalar, fmt.Errorf("sosyal izler silinemedi: %w", err))
	}
	idler, err := s.sosyal.YayinlariListele(ctx, userID)
	if err != nil {
		hatalar = append(hatalar, fmt.Errorf("sosyal yayinlar listelenemedi: %w", err))
		return errors.Join(hatalar...)
	}
	if s.silici != nil {
		onekHatasi := false
		for _, id := range idler {
			onek, err := client.SocialPrefix(id)
			if err != nil {
				hatalar = append(hatalar, fmt.Errorf("sosyal onek %s: %w", id, err))
				onekHatasi = true
				continue
			}
			if _, err := s.silici.DeletePrefix(ctx, onek); err != nil {
				hatalar = append(hatalar, fmt.Errorf("%s: %w", onek, err))
				onekHatasi = true
			}
		}
		if onekHatasi {
			return errors.Join(hatalar...)
		}
	}
	if err := s.sosyal.KullaniciVerisiniSil(ctx, userID); err != nil {
		hatalar = append(hatalar, fmt.Errorf("sosyal veri silinemedi: %w", err))
	}
	return errors.Join(hatalar...)
}
