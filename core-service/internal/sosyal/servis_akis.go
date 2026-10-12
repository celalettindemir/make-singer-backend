package sosyal

import (
	"context"
	"time"
)

// Akis sekmeleri (GET /akis?sekme=).
const (
	SekmeSeninIcin = "senin-icin"
	SekmeTakip     = "takip"
	SekmeYeni      = "yeni"
)

// BegeniDurumu, PUT/DELETE /yayinlar/{id}/begeni yaniti.
type BegeniDurumu struct {
	BegendimMi   bool `json:"begendimMi"`
	BegeniSayisi int  `json:"begeniSayisi"`
}

// zamanSayfasiKur, limit+1 ogeden sayfayi ve sonraki imleci kurar.
func zamanSayfasiKur(ogeler []YayinOzeti, limit int) *Sayfa[YayinOzeti] {
	sayfa := &Sayfa[YayinOzeti]{Ogeler: ogeler}
	if len(ogeler) > limit {
		sayfa.Ogeler = ogeler[:limit]
		son := sayfa.Ogeler[limit-1]
		sayfa.SonrakiImlec = imlecKodla(ZamanImleci{T: son.Olusturma, ID: son.ID})
	}
	if sayfa.Ogeler == nil {
		sayfa.Ogeler = []YayinOzeti{}
	}
	return sayfa
}

// Akis, GET /akis. Gecersiz sekme ya da imlec ErrDogrulama'dir.
func (s *Servis) Akis(ctx context.Context, kullaniciID, sekme, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	switch sekme {
	case SekmeYeni, SekmeTakip:
		imlec, err := zamanImleciCoz(hamImlec)
		if err != nil {
			return nil, err
		}
		cek := s.depo.AkisYeni
		if sekme == SekmeTakip {
			cek = s.depo.AkisTakip
		}
		ogeler, err := cek(ctx, id, imlec, limit)
		if err != nil {
			return nil, err
		}
		return zamanSayfasiKur(ogeler, limit), nil
	case SekmeSeninIcin:
		return s.seninIcin(ctx, id, hamImlec, limit)
	}
	return nil, dogrulamaHatasi("sekme senin-icin, takip ya da yeni olmali")
}

// seninIcin, puanli akisi ceker. Ilk sayfa referans zamanini "simdi"
// alir; sonraki sayfalar imlecteki ayni referansi kullanir.
func (s *Servis) seninIcin(ctx context.Context, id, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	imlec, err := puanImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	referans := s.simdi().Truncate(time.Millisecond)
	if imlec != nil {
		referans = time.UnixMilli(imlec.N)
	}
	ogeler, err := s.depo.AkisSeninIcin(ctx, id, referans, imlec, limit)
	if err != nil {
		return nil, err
	}
	sayfa := &Sayfa[YayinOzeti]{Ogeler: make([]YayinOzeti, 0, len(ogeler))}
	for i, o := range ogeler {
		if i == limit {
			son := ogeler[limit-1]
			sayfa.SonrakiImlec = imlecKodla(PuanImleci{P: son.Puan, ID: son.ID, N: referans.UnixMilli()})
			break
		}
		sayfa.Ogeler = append(sayfa.Ogeler, o.YayinOzeti)
	}
	return sayfa, nil
}

// ProfilYayinlari, GET /profiller/{ad}/yayinlar?tur=sarkilar|coverlar.
// tur bossa sarkilar'dir.
func (s *Servis) ProfilYayinlari(ctx context.Context, izleyiciID, ad, tur, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	id, err := kullaniciCoz(izleyiciID)
	if err != nil {
		return nil, err
	}
	var coverlar bool
	switch tur {
	case "", "sarkilar":
	case "coverlar":
		coverlar = true
	default:
		return nil, dogrulamaHatasi("tur sarkilar ya da coverlar olmali")
	}
	imlec, err := zamanImleciCoz(hamImlec)
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
	ogeler, err := s.depo.ProfilYayinlari(ctx, p.KullaniciID, id, coverlar, imlec, limit)
	if err != nil {
		return nil, err
	}
	return zamanSayfasiKur(ogeler, limit), nil
}

// Begeni, PUT (begen=true) ve DELETE (false) /yayinlar/{id}/begeni.
// Tekrari guvenlidir; yalnizca gorulebilen yayin begenilir.
func (s *Servis) Begeni(ctx context.Context, kullaniciID, yayinID string, begen bool) (*BegeniDurumu, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if begen {
		err = s.depo.Begen(ctx, id, k.ID)
	} else {
		err = s.depo.BegeniyiKaldir(ctx, id, k.ID)
	}
	if err != nil {
		return nil, err
	}
	guncel, err := s.depo.YayinGetir(ctx, k.ID, id)
	if err != nil {
		return nil, err
	}
	return &BegeniDurumu{BegendimMi: guncel.BegendimMi, BegeniSayisi: guncel.BegeniSayisi}, nil
}
