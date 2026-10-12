package sosyal

import (
	"context"
	"errors"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
)

// Kuyruk, *asynq.Client'in burada kullanilan yuzu.
type Kuyruk interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// Servis, Kesfet uclarinin is kurallarini uygular. Gorunurluk detayda
// burada (DetayGorebilir), listelerde depoda (gorunurlukKosulu) uygulanir.
type Servis struct {
	depo     Depo
	kuyruk   Kuyruk
	depolama client.StorageClient // nil: R2 yok, imzasiz adres uretilir
	r2       *config.R2Config
	simdi    func() time.Time

	// kopyalayici, cover-baslat'in stem kopyalayicisi (KopyalayiciAyarla);
	// nil: R2 yok, kopya atlanir.
	kopyalayici client.Kopyalayici
}

// NewServis kurar. depolama nil olabilir (R2 yapilandirilmamis); main
// tipli nil tuzagina dusmemek icin yalnizca gercek istemciyi gecirir.
func NewServis(depo Depo, kuyruk Kuyruk, depolama client.StorageClient, r2 *config.R2Config) *Servis {
	return &Servis{depo: depo, kuyruk: kuyruk, depolama: depolama, r2: r2, simdi: time.Now}
}

// kullaniciCoz, istegi yapanin kimligini dogrular. Auth middleware her
// zaman uuid yazar; aksi bir yapilandirma hatasidir ve 401'e doner.
func kullaniciCoz(ham string) (string, error) {
	id, ok := kimlikCoz(ham)
	if !ok {
		return "", ErrKimlik
	}
	return id, nil
}

// ozet, kaydi ProfilOzeti'ne cevirir.
func (k *ProfilKaydi) ozet() ProfilOzeti {
	return ProfilOzeti{
		KullaniciAdi: k.KullaniciAdi,
		GorunenAd:    k.GorunenAd,
		AvatarRengi:  AvatarRengi(k.KullaniciID),
	}
}

// profilKur, kaydi izleyiciye gore tam Profil'e cevirir.
func (s *Servis) profilKur(ctx context.Context, k *ProfilKaydi, izleyiciID string) (*Profil, error) {
	sayilar, err := s.depo.ProfilSayilari(ctx, k.KullaniciID, izleyiciID)
	if err != nil {
		return nil, err
	}
	return &Profil{
		ProfilOzeti:      k.ozet(),
		Bio:              k.Bio,
		Istatistik:       sayilar.Istatistik,
		TakipEdiyorMuyum: sayilar.TakipEdiyorMuyum,
		BenMiyim:         k.KullaniciID == izleyiciID,
	}, nil
}

// ProfilBen, istegi yapanin profilini doner; yoksa ErrProfilYok.
func (s *Servis) ProfilBen(ctx context.Context, kullaniciID string) (*Profil, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	k, err := s.depo.ProfilGetir(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.profilKur(ctx, k, id)
}

// ProfilKaydet, profili olusturur ya da gunceller (PUT /profil/ben).
func (s *Servis) ProfilKaydet(ctx context.Context, kullaniciID string, g ProfilGirdisi) (*Profil, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	g, err = profilGirdisiniDuzenle(g)
	if err != nil {
		return nil, err
	}
	k, err := s.depo.ProfilKaydet(ctx, ProfilKaydi{
		KullaniciID: id, KullaniciAdi: g.KullaniciAdi, GorunenAd: g.GorunenAd, Bio: g.Bio,
	})
	if err != nil {
		return nil, err
	}
	return s.profilKur(ctx, k, id)
}

// AdUygunMu, adin istegi yapan icin alinabilir olup olmadigini soyler.
// Kendi adi uygundur. Alinmissa ad2..ad99 arasindan ilk bos olani onerir.
func (s *Servis) AdUygunMu(ctx context.Context, kullaniciID, ad string) (*AdUygunlugu, error) {
	id, err := kullaniciCoz(kullaniciID)
	if err != nil {
		return nil, err
	}
	if !KullaniciAdiGecerli(ad) {
		return nil, ErrGecersizKullaniciAdi
	}
	sahipler, err := s.depo.AdSahipleri(ctx, []string{ad})
	if err != nil {
		return nil, err
	}
	if sahip, alinmis := sahipler[ad]; !alinmis || sahip == id {
		return &AdUygunlugu{Uygun: true}, nil
	}
	adaylar := adAdaylari(ad)
	sahipler, err = s.depo.AdSahipleri(ctx, adaylar)
	if err != nil {
		return nil, err
	}
	for _, aday := range adaylar {
		if sahip, alinmis := sahipler[aday]; !alinmis || sahip == id {
			return &AdUygunlugu{Uygun: false, Oneri: &aday}, nil
		}
	}
	return &AdUygunlugu{Uygun: false}, nil
}

// ProfilGoster, kullanici adiyla bir profili izleyiciye gore doner.
// Gecersiz bicimli ad ErrBulunamadi'dir: boyle bir profil olamaz.
func (s *Servis) ProfilGoster(ctx context.Context, izleyiciID, ad string) (*Profil, error) {
	id, err := kullaniciCoz(izleyiciID)
	if err != nil {
		return nil, err
	}
	if !KullaniciAdiGecerli(ad) {
		return nil, ErrBulunamadi
	}
	k, err := s.depo.ProfilAdlaGetir(ctx, ad)
	if err != nil {
		if errors.Is(err, ErrProfilYok) {
			return nil, ErrBulunamadi
		}
		return nil, err
	}
	return s.profilKur(ctx, k, id)
}
