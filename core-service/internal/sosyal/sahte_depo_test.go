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
	once       func() // dolu ise Enqueue basinda cagrilir (or. istek baglamini iptal eder)
}

func (q *sahteKuyruk) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.once != nil {
		q.once()
	}
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

	akisOgeleri []YayinOzeti
	sonAkis     akisCagrisi

	takipler      map[[2]string]bool // {takipci, takip edilen}
	oneriKayitlar []ProfilKaydi
	sonOneri      [2]any // {izleyici, limit}

	yorumKayitlari  map[string]*YorumKaydi
	yorumOgeleri    []Yorum
	eklenenYorumlar []YeniYorum
	silinenYorumlar []string
	sonYorumImleci  *YorumImleci
	sonYorumLimiti  int
}

func yeniSahteDepo() *sahteDepo {
	return &sahteDepo{
		profiller:   map[string]*ProfilKaydi{},
		yayinlar:    map[string]*YayinKaydi{},
		hazirliklar: map[string]*HazirlikKaydi{},
		yayinlanan:  map[string]HazirlikSonucu{},
		takipler:    map[[2]string]bool{},

		yorumKayitlari: map[string]*YorumKaydi{},
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

func (d *sahteDepo) YayinKaldir(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err // gercek havuz da olu baglamda sorgu calistirmaz
	}
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

func (d *sahteDepo) HataYap(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err // gercek havuz da olu baglamda sorgu calistirmaz
	}
	d.hataYapilan = append(d.hataYapilan, id)
	if k, ok := d.yayinlar[id]; ok && k.Durum == DurumHazirlaniyor {
		k.Durum = DurumHata
	}
	return d.hata
}

// akisCagrisi, sahte akis yontemlerine son gelen girdilerdir.
type akisCagrisi struct {
	tur      string
	izleyici string
	sahip    string
	coverlar bool
	zaman    *ZamanImleci
	puan     *PuanImleci
	referans time.Time
	limit    int
}

func (d *sahteDepo) zamanListesi(c akisCagrisi) ([]YayinOzeti, error) {
	d.sonAkis = c
	if d.hata != nil {
		return nil, d.hata
	}
	n := len(d.akisOgeleri)
	if n > c.limit+1 {
		n = c.limit + 1
	}
	return d.akisOgeleri[:n], nil
}

func (d *sahteDepo) ProfilYayinlari(_ context.Context, sahipID, izleyiciID string, coverlar bool, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	return d.zamanListesi(akisCagrisi{tur: "profil", izleyici: izleyiciID, sahip: sahipID, coverlar: coverlar, zaman: imlec, limit: limit})
}

func (d *sahteDepo) AkisYeni(_ context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	return d.zamanListesi(akisCagrisi{tur: "yeni", izleyici: izleyiciID, zaman: imlec, limit: limit})
}

func (d *sahteDepo) AkisTakip(_ context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	return d.zamanListesi(akisCagrisi{tur: "takip", izleyici: izleyiciID, zaman: imlec, limit: limit})
}

func (d *sahteDepo) AkisSeninIcin(_ context.Context, izleyiciID string, referans time.Time, imlec *PuanImleci, limit int) ([]PuanliOzet, error) {
	d.sonAkis = akisCagrisi{tur: "senin-icin", izleyici: izleyiciID, puan: imlec, referans: referans, limit: limit}
	if d.hata != nil {
		return nil, d.hata
	}
	var ogeler []PuanliOzet
	for i, o := range d.akisOgeleri {
		if i > limit {
			break
		}
		ogeler = append(ogeler, PuanliOzet{YayinOzeti: o, Puan: float64(100 - i)})
	}
	return ogeler, nil
}

func (d *sahteDepo) Begen(_ context.Context, kullaniciID, yayinID string) error {
	if d.hata != nil {
		return d.hata
	}
	if k, ok := d.yayinlar[yayinID]; ok && !k.BegendimMi {
		k.BegendimMi = true
		k.BegeniSayisi++
	}
	return nil
}

func (d *sahteDepo) BegeniyiKaldir(_ context.Context, kullaniciID, yayinID string) error {
	if d.hata != nil {
		return d.hata
	}
	if k, ok := d.yayinlar[yayinID]; ok && k.BegendimMi {
		k.BegendimMi = false
		k.BegeniSayisi--
	}
	return nil
}

func (d *sahteDepo) TakipEt(_ context.Context, takipci, edilen string) error {
	if d.hata != nil {
		return d.hata
	}
	d.takipler[[2]string{takipci, edilen}] = true
	return nil
}

func (d *sahteDepo) TakibiBirak(_ context.Context, takipci, edilen string) error {
	if d.hata != nil {
		return d.hata
	}
	delete(d.takipler, [2]string{takipci, edilen})
	return nil
}

func (d *sahteDepo) TakipDurumu(_ context.Context, takipci, edilen string) (*TakipDurumu, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	t := &TakipDurumu{TakipEdiyorMuyum: d.takipler[[2]string{takipci, edilen}]}
	for k := range d.takipler {
		if k[1] == edilen {
			t.Takipci++
		}
	}
	return t, nil
}

func (d *sahteDepo) Oneriler(_ context.Context, izleyiciID string, limit int) ([]ProfilKaydi, error) {
	d.sonOneri = [2]any{izleyiciID, limit}
	if d.hata != nil {
		return nil, d.hata
	}
	return d.oneriKayitlar, nil
}

func (d *sahteDepo) YorumEkle(_ context.Context, y YeniYorum) (*Yorum, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	d.eklenenYorumlar = append(d.eklenenYorumlar, y)
	p := d.profiller[y.YazarID]
	return &Yorum{ID: y.ID, Yazar: p.ozet(), Metin: y.Metin, ZamanMs: y.ZamanMs, Olusturma: time.Now()}, nil
}

func (d *sahteDepo) Yorumlar(_ context.Context, _, _ string, imlec *YorumImleci, limit int) ([]Yorum, error) {
	d.sonYorumImleci, d.sonYorumLimiti = imlec, limit
	if d.hata != nil {
		return nil, d.hata
	}
	n := len(d.yorumOgeleri)
	if n > limit+1 {
		n = limit + 1
	}
	return d.yorumOgeleri[:n], nil
}

func (d *sahteDepo) YorumGetir(_ context.Context, id string) (*YorumKaydi, error) {
	if d.hata != nil {
		return nil, d.hata
	}
	k, ok := d.yorumKayitlari[id]
	if !ok {
		return nil, ErrBulunamadi
	}
	c := *k
	return &c, nil
}

func (d *sahteDepo) YorumSil(_ context.Context, id string) error {
	if d.hata != nil {
		return d.hata
	}
	k, ok := d.yorumKayitlari[id]
	if !ok || k.Silindi {
		return ErrBulunamadi
	}
	k.Silindi = true
	d.silinenYorumlar = append(d.silinenYorumlar, id)
	return nil
}
