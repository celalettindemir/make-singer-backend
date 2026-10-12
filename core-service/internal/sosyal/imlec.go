package sosyal

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"
)

const (
	varsayilanSayfa = 20
	enBuyukSayfa    = 50
)

// Sayfa, sozlesmenin liste zarfidir. SonrakiImlec son sayfada null
// olarak GORUNUR; Ogeler hicbir zaman null degildir.
type Sayfa[T any] struct {
	Ogeler       []T     `json:"ogeler"`
	SonrakiImlec *string `json:"sonrakiImlec"`
}

// SayfaBoyutu, ?limit= degerini yorumlar: bos ya da gecersizse 20, en
// cok 50. Mobil limit gondermez; web gonderebilir.
func SayfaBoyutu(ham string) int {
	n, err := strconv.Atoi(ham)
	if err != nil || n < 1 {
		return varsayilanSayfa
	}
	if n > enBuyukSayfa {
		return enBuyukSayfa
	}
	return n
}

// ZamanImleci, zamana gore azalan listelerin konumudur.
type ZamanImleci struct {
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

// PuanImleci, "senin icin" akisinin konumudur. N, ilk sayfanin referans
// zamanidir (Unix ms); sonraki sayfalar ayni N ile hesaplanir ki puanlar
// sayfalar arasinda kaymasin.
type PuanImleci struct {
	P  float64 `json:"p"`
	ID string  `json:"id"`
	N  int64   `json:"n"`
}

func imlecKodla(v any) *string {
	b, _ := json.Marshal(v)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func imlecCoz(ham string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(ham)
	if err != nil || json.Unmarshal(b, v) != nil {
		return dogrulamaHatasi("imlec gecersiz")
	}
	return nil
}

// zamanImleciCoz, bos dizgede nil doner.
func zamanImleciCoz(ham string) (*ZamanImleci, error) {
	if ham == "" {
		return nil, nil
	}
	var im ZamanImleci
	if err := imlecCoz(ham, &im); err != nil {
		return nil, err
	}
	id, ok := kimlikCoz(im.ID)
	if !ok || im.T.IsZero() {
		return nil, dogrulamaHatasi("imlec gecersiz")
	}
	im.ID = id
	return &im, nil
}

// puanImleciCoz, bos dizgede nil doner.
func puanImleciCoz(ham string) (*PuanImleci, error) {
	if ham == "" {
		return nil, nil
	}
	var im PuanImleci
	if err := imlecCoz(ham, &im); err != nil {
		return nil, err
	}
	id, ok := kimlikCoz(im.ID)
	if !ok || im.N <= 0 {
		return nil, dogrulamaHatasi("imlec gecersiz")
	}
	im.ID = id
	return &im, nil
}

// YorumImleci, yorum listesinin konumudur. Liste iki gruptur: once
// zamanli yorumlar (G=0; zaman_ms, olusturma, id artan), sonra damgasizlar
// (G=1; olusturma, id azalan). Z yalnizca G=0'da doludur.
type YorumImleci struct {
	G  int       `json:"g"`
	Z  *int      `json:"z,omitempty"`
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

// yorumImleciCoz, bos dizgede nil doner.
func yorumImleciCoz(ham string) (*YorumImleci, error) {
	if ham == "" {
		return nil, nil
	}
	var im YorumImleci
	if err := imlecCoz(ham, &im); err != nil {
		return nil, err
	}
	id, ok := kimlikCoz(im.ID)
	gecerli := ok && !im.T.IsZero() &&
		((im.G == 0 && im.Z != nil && *im.Z >= 0) || (im.G == 1 && im.Z == nil))
	if !gecerli {
		return nil, dogrulamaHatasi("imlec gecersiz")
	}
	im.ID = id
	return &im, nil
}

// yorumImleci, sayfanin son yorumundan sonraki sayfanin imlecini kurar.
func yorumImleci(son Yorum) *string {
	im := YorumImleci{G: 1, T: son.Olusturma, ID: son.ID}
	if son.ZamanMs != nil {
		im.G, im.Z = 0, son.ZamanMs
	}
	return imlecKodla(im)
}
