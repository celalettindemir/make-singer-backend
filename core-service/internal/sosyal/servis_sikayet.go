package sosyal

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Sikayet hedefleri ve nedenleri (spec §3 sikayet).
const (
	HedefYayin = "yayin"
	HedefYorum = "yorum"

	sikayetAciklamaEnUzun = 300
)

var sikayetNedenleri = map[string]bool{
	"spam": true, "taciz": true, "telif": true, "uygunsuz": true, "diger": true,
}

// SikayetGirdisi, POST /sikayetler govdesi. aciklama istege baglidir.
type SikayetGirdisi struct {
	HedefTuru string `json:"hedefTuru"`
	HedefID   string `json:"hedefId"`
	Neden     string `json:"neden"`
	Aciklama  string `json:"aciklama"`
}

// YeniSikayet, depoya yazilacak dogrulanmis sikayet. YayinID hedefin ait
// oldugu yayindir (hedef yorumsa yorumun yayini).
type YeniSikayet struct {
	ID         string
	BildirenID string
	HedefTuru  string
	HedefID    string
	YayinID    string
	Neden      string
	Aciklama   string
}

// Sikayet, POST /sikayetler (201). Hedef bildirene gorunur olmalidir:
// yayin detay kuralina gore, yorum silinmemis ve yayini gorunur. Aksi ya
// da bozuk kimlik ErrBulunamadi'dir. Tekrar sikayet hata degildir.
// Profil gerekmez; kendi icerigini sikayet etmek reddedilmez.
func (s *Servis) Sikayet(ctx context.Context, kullaniciID string, g SikayetGirdisi) error {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return err
	}
	if g.HedefTuru != HedefYayin && g.HedefTuru != HedefYorum {
		return dogrulamaHatasi("hedefTuru yayin ya da yorum olmali")
	}
	if !sikayetNedenleri[g.Neden] {
		return dogrulamaHatasi("neden spam, taciz, telif, uygunsuz ya da diger olmali")
	}
	aciklama := strings.TrimSpace(g.Aciklama)
	if utf8.RuneCountInString(aciklama) > sikayetAciklamaEnUzun {
		return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
	}
	if nulIceriyor(aciklama) {
		return dogrulamaHatasi("aciklama gecersiz karakter iceriyor")
	}
	hedefID, ok := kimlikCoz(g.HedefID)
	if !ok {
		return ErrBulunamadi
	}

	yayinID := hedefID
	if g.HedefTuru == HedefYorum {
		y, err := s.depo.YorumGetir(ctx, hedefID)
		if err != nil {
			return err
		}
		if y.Silindi {
			return ErrBulunamadi
		}
		yayinID = y.YayinID
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return err
	}
	return s.depo.SikayetEkle(ctx, YeniSikayet{
		ID: uuid.NewString(), BildirenID: id, HedefTuru: g.HedefTuru, HedefID: hedefID,
		YayinID: k.ID, Neden: g.Neden, Aciklama: aciklama,
	})
}
