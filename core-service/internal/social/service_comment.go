package social

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const yorumEnUzun = 500

// yorumSayfasiKur, limit+1 yorumdan sayfayi ve sonraki imleci kurar.
func yorumSayfasiKur(ogeler []Yorum, limit int) *Sayfa[Yorum] {
	sayfa := &Sayfa[Yorum]{Ogeler: ogeler}
	if len(ogeler) > limit {
		sayfa.Ogeler = ogeler[:limit]
		sayfa.SonrakiImlec = yorumImleci(sayfa.Ogeler[limit-1])
	}
	if sayfa.Ogeler == nil {
		sayfa.Ogeler = []Yorum{}
	}
	return sayfa
}

// Yorumlar, GET /yayinlar/{id}/yorumlar. Yayin gorunmuyorsa
// ErrBulunamadi, yorumlari kapaliysa (sahibi dahil) ErrYorumKapali.
func (s *Servis) Yorumlar(ctx context.Context, kullaniciID, yayinID, hamImlec string, limit int) (*Sayfa[Yorum], error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	imlec, err := yorumImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if !k.YorumAcik {
		return nil, ErrCommentsDisabled
	}
	ogeler, err := s.depo.ListComments(ctx, k.ID, id, imlec, limit)
	if err != nil {
		return nil, err
	}
	return yorumSayfasiKur(ogeler, limit), nil
}

// YorumYaz, POST /yayinlar/{id}/yorumlar (201). Sira: govde (422),
// gorunurluk (404), yorumAcik (403 yorum_kapali), profil (409
// profil_yok), zaman araligi (422). metin kirpilir, 1-500 karakter;
// zamanMs null ya da 0 <= zamanMs <= sureSn*1000.
func (s *Servis) YorumYaz(ctx context.Context, kullaniciID, yayinID string, g YorumGirdisi) (*Yorum, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	metin := strings.TrimSpace(g.Metin)
	if nulIceriyor(metin) {
		return nil, validationError("metin NUL karakteri iceremez")
	}
	if n := utf8.RuneCountInString(metin); n < 1 || n > yorumEnUzun {
		return nil, validationError("metin 1-500 karakter olmali")
	}
	if g.ZamanMs != nil && *g.ZamanMs < 0 {
		return nil, validationError("zamanMs negatif olamaz")
	}
	// zaman_ms sutunu integer'dir; sureSn ust sinirsiz oldugundan sure
	// denetimi tek basina INSERT tasmasini (500) onlemez.
	if g.ZamanMs != nil && *g.ZamanMs > math.MaxInt32 {
		return nil, validationError("zamanMs cok buyuk")
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if !k.YorumAcik {
		return nil, ErrCommentsDisabled
	}
	if _, err := s.depo.GetProfile(ctx, id); err != nil {
		return nil, err
	}
	if g.ZamanMs != nil && *g.ZamanMs > k.SureSn*1000 {
		return nil, validationError("zamanMs sarkinin suresini asiyor")
	}
	y, err := s.depo.InsertComment(ctx, YeniYorum{
		ID: uuid.NewString(), YayinID: k.ID, YazarID: id, Metin: metin, ZamanMs: g.ZamanMs,
	})
	if err != nil {
		return nil, err
	}
	y.SilebilirMiyim = true
	return y, nil
}

// YorumSil, DELETE /yorumlar/{id} (204). Yalnizca yazar ya da yayin
// sahibi siler; baska biri yayini gorebiliyorsa ErrYetki (403),
// goremiyorsa ErrBulunamadi (varligi sizdirilmaz). Silinmis yorum
// ErrBulunamadi'dir (tekrar 404).
func (s *Servis) YorumSil(ctx context.Context, kullaniciID, yorumID string) error {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return err
	}
	yid, ok := kimlikCoz(yorumID)
	if !ok {
		return ErrNotFound
	}
	k, err := s.depo.GetComment(ctx, yid)
	if err != nil {
		return err
	}
	if k.Silindi {
		return ErrNotFound
	}
	if k.YazarID != id && k.YayinSahibiID != id {
		if _, err := s.gorunurYayin(ctx, id, k.YayinID); err != nil {
			return err
		}
		return ErrForbidden
	}
	return s.depo.DeleteComment(ctx, k.ID)
}
