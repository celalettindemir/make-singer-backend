package sosyal

import "context"

// oneriSayisi, GET /oneriler'in en cok kac profil dondurdugu (spec §4.4).
const oneriSayisi = 10

// TakipDurumu, PUT/DELETE /profiller/{ad}/takip yaniti.
type TakipDurumu struct {
	Takipci          int  `json:"takipci"`
	TakipEdiyorMuyum bool `json:"takipEdiyorMuyum"`
}

// Takip, PUT (takip=true) ve DELETE (false) /profiller/{ad}/takip.
// Tekrari guvenlidir. Kendini takip (ya da birakma) ErrKendiniTakip,
// olmayan ya da gecersiz bicimli ad ErrBulunamadi'dir. Takip eden icin
// profil gerekmez (begeni gibi).
func (s *Servis) Takip(ctx context.Context, kullaniciID, ad string, takip bool) (*TakipDurumu, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if !KullaniciAdiGecerli(ad) {
		return nil, ErrBulunamadi
	}
	p, err := s.depo.ProfilAdlaGetir(ctx, ad)
	if err != nil {
		return nil, err
	}
	if p.KullaniciID == id {
		return nil, ErrKendiniTakip
	}
	if takip {
		err = s.depo.TakipEt(ctx, id, p.KullaniciID)
	} else {
		err = s.depo.TakibiBirak(ctx, id, p.KullaniciID)
	}
	if err != nil {
		return nil, err
	}
	return s.depo.TakipDurumu(ctx, id, p.KullaniciID)
}

// Oneriler, GET /oneriler: en cok 10 ProfilOzeti, sayfasiz. Bos liste
// null degil [] olarak doner.
func (s *Servis) Oneriler(ctx context.Context, kullaniciID string) ([]ProfilOzeti, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	kayitlar, err := s.depo.Oneriler(ctx, id, oneriSayisi)
	if err != nil {
		return nil, err
	}
	ozetler := make([]ProfilOzeti, 0, len(kayitlar))
	for i := range kayitlar {
		ozetler = append(ozetler, kayitlar[i].ozet())
	}
	return ozetler, nil
}
