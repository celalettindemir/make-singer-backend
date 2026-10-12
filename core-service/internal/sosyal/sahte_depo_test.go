package sosyal

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/config"
)

// sahteKuyruk, kuyruga konan gorevleri ve seceneklerini kaydeder.
type sahteKuyruk struct {
	gorevler   []*asynq.Task
	secenekler [][]asynq.Option
	hata       error
}

func (q *sahteKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.hata != nil {
		return nil, q.hata
	}
	q.gorevler = append(q.gorevler, t)
	q.secenekler = append(q.secenekler, opts)
	return &asynq.TaskInfo{}, nil
}

// testR2, client testlerindeki yapilandirmanin aynisidir.
func testR2() *config.R2Config {
	return &config.R2Config{
		AccountID: "hesap123", PublicBucket: "makeasinger-public", PrivateBucket: "makeasinger-private",
		PublicURL: "https://makesinger-cdn.celalettindemir.dev", PresignTTL: time.Hour,
	}
}

// testServisi, sahte depo ve kuyrukla, R2 istemcisi olmadan servis kurar.
func testServisi(d *sahteDepo) (*Servis, *sahteKuyruk) {
	q := &sahteKuyruk{}
	return NewServis(d, q, nil, testR2()), q
}

// sahteDepo, servis testleri icin bellek ici Depo'dur. Yalnizca servisin
// kararlarini sinamaya yetecek kadar davranir; SQL davranisi canli
// Postgres testlerinde sinanir.
type sahteDepo struct {
	profiller map[string]*ProfilKaydi // kullanici id -> kayit
	sayilar   ProfilSayilari
	hata      error // dolu ise her cagri bu hatayla doner

	yayinlar      map[string]*YayinKaydi // yayin id -> kayit
	eklenen       []YeniYayin
	slugCakismasi int // ilk n YayinEkle errSlugCakisti doner
	yamalar       []YayinYamasi
	kaldirilan    []string

	hazirliklar map[string]*HazirlikKaydi
	yayinlanan  map[string]HazirlikSonucu
	hataYapilan []string
}

func yeniSahteDepo() *sahteDepo {
	return &sahteDepo{
		profiller:   map[string]*ProfilKaydi{},
		yayinlar:    map[string]*YayinKaydi{},
		hazirliklar: map[string]*HazirlikKaydi{},
		yayinlanan:  map[string]HazirlikSonucu{},
	}
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

func (d *sahteDepo) YayinEkle(_ context.Context, y YeniYayin) error {
	if d.hata != nil {
		return d.hata
	}
	if d.slugCakismasi > 0 {
		d.slugCakismasi--
		return errSlugCakisti
	}
	d.eklenen = append(d.eklenen, y)
	return nil
}

// kopya, kaydin izleyiciye gore bir kopyasini doner (depo BenimMi'yi
// doldurur).
func (d *sahteDepo) kopya(k *YayinKaydi, izleyiciID string) *YayinKaydi {
	c := *k
	c.BenimMi = c.SahipID == izleyiciID
	return &c
}

func (d *sahteDepo) YayinGetir(_ context.Context, id, izleyiciID string) (*YayinKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.yayinlar[id]
	if !ok {
		return nil, ErrBulunamadi
	}
	return d.kopya(k, izleyiciID), nil
}

func (d *sahteDepo) YayinSlugIleGetir(_ context.Context, slug, izleyiciID string) (*YayinKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	for _, k := range d.yayinlar {
		if k.Slug == slug {
			return d.kopya(k, izleyiciID), nil
		}
	}
	return nil, ErrBulunamadi
}

// sahibinCanliYayini, sahibin silinmemis yayinini bulur.
func (d *sahteDepo) sahibinCanliYayini(id, sahipID string) (*YayinKaydi, bool) {
	k, ok := d.yayinlar[id]
	if !ok || k.SahipID != sahipID || k.Silindi {
		return nil, false
	}
	return k, true
}

func (d *sahteDepo) YayinGuncelle(_ context.Context, id, sahipID string, yama YayinYamasi) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.sahibinCanliYayini(id, sahipID)
	if !ok {
		return ErrBulunamadi
	}
	d.yamalar = append(d.yamalar, yama)
	if yama.Aciklama != nil {
		k.Aciklama = *yama.Aciklama
	}
	if yama.AltyapiAcik != nil {
		k.AltyapiAcik = *yama.AltyapiAcik
	}
	if yama.Gorunurluk != nil {
		k.Gorunurluk = *yama.Gorunurluk
	}
	if yama.YorumAcik != nil {
		k.YorumAcik = *yama.YorumAcik
	}
	return nil
}

func (d *sahteDepo) YayinSil(_ context.Context, id, sahipID string) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.sahibinCanliYayini(id, sahipID)
	if !ok {
		return ErrBulunamadi
	}
	k.Silindi = true
	return nil
}

func (d *sahteDepo) YayinKaldir(_ context.Context, id string) error {
	d.kaldirilan = append(d.kaldirilan, id)
	return d.hata
}

func (d *sahteDepo) TekrarDenemeyeAl(_ context.Context, id, sahipID string) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.sahibinCanliYayini(id, sahipID)
	if !ok {
		return ErrBulunamadi
	}
	if k.Durum != DurumHata {
		return ErrDurum
	}
	k.Durum = DurumHazirlaniyor
	return nil
}

func (d *sahteDepo) HazirlikGetir(_ context.Context, id string) (*HazirlikKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.hazirliklar[id]
	if !ok {
		return nil, ErrBulunamadi
	}
	return k, nil
}

func (d *sahteDepo) Yayinla(_ context.Context, id string, s HazirlikSonucu) error {
	if d.hata != nil {
		return d.hata
	}
	d.yayinlanan[id] = s
	return nil
}

func (d *sahteDepo) HataYap(_ context.Context, id string) error {
	d.hataYapilan = append(d.hataYapilan, id)
	if k, ok := d.yayinlar[id]; ok && k.Durum == DurumHazirlaniyor {
		k.Durum = DurumHata
	}
	return d.hata
}
