package sosyal

import (
	"context"
	"time"
)

// sahteDepo, servis testleri icin bellek ici Depo'dur. Yalnizca servisin
// kararlarini sinamaya yetecek kadar davranir; SQL davranisi canli
// Postgres testlerinde sinanir.
type sahteDepo struct {
	profiller map[string]*ProfilKaydi // kullanici id -> kayit
	sayilar   ProfilSayilari
	hata      error // dolu ise her cagri bu hatayla doner
}

func yeniSahteDepo() *sahteDepo {
	return &sahteDepo{profiller: map[string]*ProfilKaydi{}}
}

func (d *sahteDepo) ProfilGetir(_ context.Context, id string) (*ProfilKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.profiller[id]
	if !ok {
		return nil, ErrProfilYok
	}
	return k, nil
}

func (d *sahteDepo) ProfilAdlaGetir(_ context.Context, ad string) (*ProfilKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for _, k := range d.profiller {
		if k.KullaniciAdi == ad {
			return k, nil
		}
	}
	return nil, ErrBulunamadi
}

func (d *sahteDepo) ProfilKaydet(_ context.Context, k ProfilKaydi) (*ProfilKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for id, v := range d.profiller {
		if v.KullaniciAdi == k.KullaniciAdi && id != k.KullaniciID {
			return nil, ErrKullaniciAdiAlinmis
		}
	}
	k.Olusturma = time.Now()
	d.profiller[k.KullaniciID] = &k
	return &k, nil
}

func (d *sahteDepo) AdSahipleri(_ context.Context, adlar []string) (map[string]string, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	m := map[string]string{}
	for _, ad := range adlar {
		for id, k := range d.profiller {
			if k.KullaniciAdi == ad {
				m[ad] = id
			}
		}
	}
	return m, nil
}

func (d *sahteDepo) ProfilSayilari(context.Context, string, string) (*ProfilSayilari, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	s := d.sayilar
	return &s, nil
}
