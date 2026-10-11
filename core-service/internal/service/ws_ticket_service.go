package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/makeasinger/api/internal/model"
	"github.com/redis/go-redis/v9"
)

// wsBiletTTL: bilet alma ile baglanma arasi icin yeterli, sizarsa kisa.
const wsBiletTTL = 30 * time.Second

// ErrBiletGecersiz: bilet yok, suresi dolmus, kullanilmis ya da baska bir
// ise ait. Durumlar bilerek ayirt edilmez.
var ErrBiletGecersiz = errors.New("websocket bileti gecersiz")

// biletDeseni: 32 bayt base64url (dolgusuz) = 43 karakter. Bicimsiz girdi
// Redis'e hic gitmez.
var biletDeseni = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// WSTicketService, WebSocket baglantisi icin tek kullanimlik bilet uretir.
// React Native'in WebSocket'i iOS'ta ozel baslik gonderemiyor; access
// token'i sorgu dizesine koymak onu loglara tasirdi. Bilet 30 saniyelik ve
// tek kullanimlik oldugu icin sorgu dizesinde durabilir.
type WSTicketService struct {
	redis *redis.Client
}

func NewWSTicketService(rdb *redis.Client) *WSTicketService {
	return &WSTicketService{redis: rdb}
}

func biletAnahtari(bilet string) string { return "wsbilet:" + bilet }

// Issue, isin sahibine bilet verir. Sahiplik burada denetlenir; bilet o
// ise baglidir.
func (s *WSTicketService) Issue(ctx context.Context, userID, jobID string) (*model.WSTicketResponse, error) {
	if _, err := sahipliJobOku(ctx, s.redis, userID, jobID); err != nil {
		return nil, err
	}
	ham := make([]byte, 32)
	if _, err := rand.Read(ham); err != nil {
		return nil, fmt.Errorf("bilet uretilemedi: %w", err)
	}
	bilet := base64.RawURLEncoding.EncodeToString(ham)
	if err := s.redis.Set(ctx, biletAnahtari(bilet), jobID, wsBiletTTL).Err(); err != nil {
		return nil, fmt.Errorf("bilet kaydedilemedi: %w", err)
	}
	return &model.WSTicketResponse{Ticket: bilet, ExpiresAt: time.Now().Add(wsBiletTTL)}, nil
}

// Redeem, bileti tuketir. GETDEL atomiktir: ayni bilet iki baglantida
// kullanilamaz. Yanlis ise sunulan bilet de tuketilir.
func (s *WSTicketService) Redeem(ctx context.Context, bilet, jobID string) error {
	if !biletDeseni.MatchString(bilet) {
		return ErrBiletGecersiz
	}
	isID, err := s.redis.GetDel(ctx, biletAnahtari(bilet)).Result()
	if errors.Is(err, redis.Nil) {
		return ErrBiletGecersiz
	}
	if err != nil {
		return fmt.Errorf("bilet okunamadi: %w", err)
	}
	if isID != jobID {
		return ErrBiletGecersiz
	}
	return nil
}
