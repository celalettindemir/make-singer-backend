package social

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
		cek := s.depo.FeedNew
		if sekme == SekmeTakip {
			cek = s.depo.FeedFollowing
		}
		ogeler, err := cek(ctx, id, imlec, limit)
		if err != nil {
			return nil, err
		}
		return zamanSayfasiKur(ogeler, limit), nil
	case SekmeSeninIcin:
		return s.seninIcin(ctx, id, hamImlec, limit)
	}
	return nil, validationError("sekme senin-icin, takip ya da yeni olmali")
}

// referansPayi, uygulama ve veritabani saatleri arasindaki farka taninan paydir.
const referansPayi = time.Second

// seninIcin, puanli akisi ceker. Ilk sayfa referans zamanini "simdi"
// alir; sonraki sayfalar imlecteki ayni referansi kullanir.
func (s *Servis) seninIcin(ctx context.Context, id, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	imlec, err := puanImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	// Referans bir saat payi kadar ileri alinir ve milisaniyeye yukari
	// yuvarlanir: yayinin olusturma zamani veritabani saatinden gelir, uygulama
	// saati biraz geride kalirsa ya da kesme milisaniyenin altini atarsa yeni
	// yayin ilk sayfadan dusmesin.
	simdi := s.simdi().Add(referansPayi).Truncate(time.Millisecond)
	referans := simdi
	if imlec != nil {
		referans = time.UnixMilli(imlec.N)
	}
	// Gelecekteki bir referans puanlamayi kaydirmasin diye simdiye kirpilir.
	if referans.After(simdi) {
		referans = simdi
	}
	ogeler, err := s.depo.FeedForYou(ctx, id, referans, imlec, limit)
	if err != nil {
		return nil, err
	}
	sayfa := &Sayfa[YayinOzeti]{Ogeler: make([]YayinOzeti, 0, len(ogeler))}
	for i, o := range ogeler {
		if i == limit {
			son := ogeler[limit-1]
			sayfa.SonrakiImlec = imlecKodla(PuanImleci{P: son.Score, ID: son.ID, N: referans.UnixMilli()})
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
		return nil, validationError("tur sarkilar ya da coverlar olmali")
	}
	imlec, err := zamanImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	if !KullaniciAdiGecerli(ad) {
		return nil, ErrNotFound
	}
	p, err := s.depo.GetProfileByUsername(ctx, ad)
	if err != nil {
		return nil, err
	}
	ogeler, err := s.depo.ListProfilePublications(ctx, p.KullaniciID, id, coverlar, imlec, limit)
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
		err = s.depo.Like(ctx, id, k.ID)
	} else {
		err = s.depo.Unlike(ctx, id, k.ID)
	}
	if err != nil {
		return nil, err
	}
	guncel, err := s.depo.GetPublication(ctx, k.ID, id)
	if err != nil {
		return nil, err
	}
	return &BegeniDurumu{BegendimMi: guncel.BegendimMi, BegeniSayisi: guncel.BegeniSayisi}, nil
}
