package social

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/client"
)

// CoverStemi, cover projesine verilen stem: fileUrl, kullanicinin kendi
// stems/ onekindeki kopyanin adresidir (/api/files/sign yeniden imzalar).
type CoverStemi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	FileURL   string  `json:"fileUrl"`
	Sure      float64 `json:"sure"`
}

// CoverBaslangici, POST /yayinlar/{id}/cover-baslat yaniti (spec §4.6).
type CoverBaslangici struct {
	Kaynak  KaynakOzeti  `json:"kaynak"`
	Tur     string       `json:"tur"`
	Bpm     int          `json:"bpm"`
	Ton     Ton          `json:"ton"`
	SureSn  int          `json:"sureSn"`
	Yapi    []YapiOgesi  `json:"yapi"`
	Sozler  Sozler       `json:"sozler"`
	Stemler []CoverStemi `json:"stemler"`
}

// KopyalayiciAyarla, cover-baslat'in stem kopyalayicisini baglar ve
// servisi doner. nil (R2 yapilandirilmamis, dev) ise kopya atlanir ve
// adresler yine uretilir. main tipli nil tuzagina dusmemek icin yalnizca
// gercek istemciyi gecirir.
func (s *Servis) KopyalayiciAyarla(k client.Copier) *Servis {
	s.kopyalayici = k
	return s
}

// CoverStemHedefi, cover'a kopyalanan stem'in anahtaridir:
// stems/{kullaniciId}/{klasor}/{stemId}.{ext}. Render stem duzeniyle
// aynidir; uzanti kaynak anahtardan gelir.
func CoverStemHedefi(kullaniciID, klasor, stemID, kaynak string) (string, error) {
	return client.UserKey("stems", kullaniciID, klasor, stemID+uzanti(kaynak))
}

// Coverlar, GET /yayinlar/{id}/coverlar. Kaynak gorunmuyorsa
// ErrBulunamadi; altyapisi kapaliysa (cover yayini dahil) bos sayfa.
func (s *Servis) Coverlar(ctx context.Context, kullaniciID, yayinID, hamImlec string, limit int) (*Sayfa[YayinOzeti], error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	imlec, err := zamanImleciCoz(hamImlec)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if !k.AltyapiAcik {
		return zamanSayfasiKur(nil, limit), nil
	}
	ogeler, err := s.depo.ListCovers(ctx, k.ID, id, imlec, limit)
	if err != nil {
		return nil, err
	}
	return zamanSayfasiKur(ogeler, limit), nil
}

// CoverBaslat, POST /yayinlar/{id}/cover-baslat: yayinin stem'lerini
// istegi yapanin stems/ onekine es zamanli kopyalar ve cover projesinin
// baslangicini doner. Gorunmeyen yayin ErrBulunamadi; cover yayini,
// altyapisi kapali ya da stem kopyasi olmayan yayin ErrAltyapiKapali;
// sahibinin henuz yayinda olmayan yayini ErrDurum.
func (s *Servis) CoverBaslat(ctx context.Context, kullaniciID, yayinID string) (*CoverBaslangici, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.gorunurYayin(ctx, id, yayinID)
	if err != nil {
		return nil, err
	}
	if k.KaynakYayinID != "" || !k.AltyapiAcik {
		return nil, ErrBackingDisabled
	}
	if k.Durum != DurumYayinda {
		return nil, ErrInvalidStatus
	}
	kaynakStemler, err := s.depo.ListStems(ctx, k.ID)
	if err != nil {
		return nil, err
	}
	if len(kaynakStemler) == 0 {
		return nil, ErrBackingDisabled
	}

	klasor := uuid.NewString()
	stemler := make([]CoverStemi, 0, len(kaynakStemler))
	for _, st := range kaynakStemler {
		hedef, err := CoverStemHedefi(id, klasor, st.ID, st.Anahtar)
		if err != nil {
			return nil, fmt.Errorf("cover stem anahtari kurulamadi: %w", err)
		}
		if s.kopyalayici != nil {
			if err := s.kopyalayici.Copy(ctx, st.Anahtar, hedef); err != nil {
				return nil, fmt.Errorf("cover stem kopyalanamadi: %w", err)
			}
		}
		url, _, err := client.ResolveURL(ctx, s.depolama, s.r2, hedef)
		if err != nil {
			return nil, fmt.Errorf("cover stem adresi uretilemedi: %w", err)
		}
		stemler = append(stemler, CoverStemi{ID: st.ID, Enstruman: st.Enstruman, FileURL: url, Sure: st.Sure})
	}
	return &CoverBaslangici{
		Kaynak: KaynakOzeti{YayinID: k.ID, Baslik: k.Baslik, SahipKullaniciAdi: k.Sahip.KullaniciAdi},
		Tur:    k.Tur, Bpm: k.Bpm, Ton: k.Ton, SureSn: k.SureSn,
		Yapi: k.Yapi, Sozler: k.Sozler, Stemler: stemler,
	}, nil
}
