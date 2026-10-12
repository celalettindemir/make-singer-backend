package sosyal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// YayinDeposu, sosyal.yayin uzerindeki uc islemleri.
type YayinDeposu interface {
	// YayinEkle, yayini hazirlaniyor durumunda yazar. Slug baskasindaysa
	// errSlugCakisti doner; servis yeni slug ile yeniden dener.
	YayinEkle(ctx context.Context, y YeniYayin) error
	// YayinGetir ve YayinSlugIleGetir, yayini izleyiciye gore sayaclarla
	// doner. Silinmis yayin da doner (Silindi true); yoksa ErrBulunamadi.
	YayinGetir(ctx context.Context, id, izleyiciID string) (*YayinKaydi, error)
	YayinSlugIleGetir(ctx context.Context, slug, izleyiciID string) (*YayinKaydi, error)
	// YayinGuncelle, sahibin silinmemis yayininda yalnizca dolu alanlari
	// degistirir. Satir yoksa ErrBulunamadi.
	YayinGuncelle(ctx context.Context, id, sahipID string, yama YayinYamasi) error
	// YayinSil, yumusak siler (silinme = now()). Satir yoksa, sahibi
	// degilse ya da zaten silinmisse ErrBulunamadi.
	YayinSil(ctx context.Context, id, sahipID string) error
	// YayinKaldir, yayini kalici siler. Yalnizca kuyruga hic girmemis bir
	// yayini geri almak icindir.
	YayinKaldir(ctx context.Context, id string) error
	// TekrarDenemeyeAl, sahibin hata durumundaki yayinini hazirlaniyor
	// yapar. Yayin var ama hata degilse ErrDurum, yoksa ErrBulunamadi.
	TekrarDenemeyeAl(ctx context.Context, id, sahipID string) error
}

// HazirlikDeposu, sosyal:hazirla isinin kullandigi islemler.
type HazirlikDeposu interface {
	// HazirlikGetir, satiri doner; yoksa ErrBulunamadi.
	HazirlikGetir(ctx context.Context, id string) (*HazirlikKaydi, error)
	// Yayinla, hazirlaniyor durumundaki silinmemis yayini sonucla yayinda
	// yapar. Durum degismisse (silindi, zaten yayinda) hicbir sey yapmaz.
	Yayinla(ctx context.Context, id string, sonuc HazirlikSonucu) error
	// HataYap, hazirlaniyor durumundaki yayini hata yapar.
	HataYap(ctx context.Context, id string) error
}

// ozetSecimi, YayinOzeti'nin kolonlari. $1 her zaman izleyicidir. Sahip
// profili INNER JOIN'dir: profilsiz yayin olusturulamaz ve hesap silme
// ikisini birlikte siler. Kaynak LEFT JOIN'dir: orijinal kalici silinmisse
// cover'daki son bilinen degerler kullanilir.
const ozetSecimi = `
	y.id::text, y.slug, y.baslik, y.sahip_id::text, p.kullanici_adi, p.gorunen_ad,
	y.tur, y.bpm, y.ton_tonik, y.ton_mod, y.sure_sn, y.dalga_formu, y.altyapi_acik,
	y.kaynak_yayin_id::text,
	COALESCE(k.baslik, y.kaynak_baslik, ''),
	COALESCE(kp.kullanici_adi, y.kaynak_sahip_adi, ''),
	(k.id IS NULL OR k.silinme IS NOT NULL),
	y.olusturma,
	(SELECT count(*) FROM sosyal.begeni b WHERE b.yayin_id = y.id),
	(SELECT count(*) FROM sosyal.yorum c WHERE c.yayin_id = y.id AND c.silinme IS NULL),
	(SELECT count(*) FROM sosyal.yayin cv
	  WHERE cv.kaynak_yayin_id = y.id AND cv.durum = 'yayinda' AND cv.silinme IS NULL),
	EXISTS (SELECT 1 FROM sosyal.begeni b WHERE b.yayin_id = y.id AND b.kullanici_id = $1)`

const ozetKaynagi = `
	FROM sosyal.yayin y
	JOIN sosyal.profil p ON p.kullanici_id = y.sahip_id
	LEFT JOIN sosyal.yayin k ON k.id = y.kaynak_yayin_id
	LEFT JOIN sosyal.profil kp ON kp.kullanici_id = k.sahip_id`

// ozetTarayici, ozetSecimi kolonlarinin tarama hedefleridir.
type ozetTarayici struct {
	o              YayinOzeti
	sahipID        string
	kaynakID       *string
	kaynakBaslik   string
	kaynakAdi      string
	kaynakSilindi  bool
	dalgaFormu     []float64
	olusturma      time.Time
	begeni, yorum  int
	cover          int
	begendimMi     bool
	tonik, tonModu string
}

func (t *ozetTarayici) hedefler() []any {
	return []any{
		&t.o.ID, &t.o.Slug, &t.o.Baslik, &t.sahipID, &t.o.Sahip.KullaniciAdi, &t.o.Sahip.GorunenAd,
		&t.o.Tur, &t.o.Bpm, &t.tonik, &t.tonModu, &t.o.SureSn, &t.dalgaFormu, &t.o.AltyapiAcik,
		&t.kaynakID, &t.kaynakBaslik, &t.kaynakAdi, &t.kaynakSilindi,
		&t.olusturma, &t.begeni, &t.yorum, &t.cover, &t.begendimMi,
	}
}

func (t *ozetTarayici) ozet() YayinOzeti {
	o := t.o
	o.Sahip.AvatarRengi = AvatarRengi(t.sahipID)
	o.Ton = Ton{Tonik: t.tonik, Mod: t.tonModu}
	o.DalgaFormu = t.dalgaFormu
	if o.DalgaFormu == nil {
		o.DalgaFormu = []float64{}
	}
	o.BegeniSayisi, o.YorumSayisi, o.CoverSayisi, o.BegendimMi = t.begeni, t.yorum, t.cover, t.begendimMi
	o.Olusturma = t.olusturma.UTC()
	if t.kaynakID != nil {
		o.Kaynak = &KaynakOzeti{
			YayinID: *t.kaynakID, Baslik: t.kaynakBaslik,
			SahipKullaniciAdi: t.kaynakAdi, Silindi: t.kaynakSilindi,
		}
	}
	return o
}

// yayinGetir, detay sorgusunu verilen kosulla ($2) kosar.
func (d *PostgresDepo) yayinGetir(ctx context.Context, kosul, deger, izleyiciID string) (*YayinKaydi, error) {
	var (
		t       ozetTarayici
		k       YayinKaydi
		sozler  Sozler
		yapi    []YapiOgesi
		master  *string
		stemVar bool
	)
	hedefler := append(t.hedefler(),
		&k.Aciklama, &k.YorumAcik, &sozler, &yapi, &k.Durum, &k.Gorunurluk,
		&k.Silindi, &stemVar, &master, &k.TakipEdiyor)
	err := d.havuz.QueryRow(ctx, `
		SELECT `+ozetSecimi+`,
		  y.aciklama, y.yorum_acik, y.sozler, y.yapi, y.durum, y.gorunurluk,
		  y.silinme IS NOT NULL,
		  (jsonb_typeof(y.hazirlik->'stems') = 'array' AND jsonb_array_length(y.hazirlik->'stems') > 0),
		  y.master_anahtari,
		  EXISTS (SELECT 1 FROM sosyal.takip tk WHERE tk.takipci_id = $1 AND tk.takip_edilen_id = y.sahip_id)
		`+ozetKaynagi+`
		WHERE `+kosul+` = $2`, izleyiciID, deger).Scan(hedefler...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("yayin okunamadi: %w", err)
	}
	k.YayinOzeti = t.ozet()
	k.SahipID = t.sahipID
	if t.kaynakID != nil {
		k.KaynakYayinID = *t.kaynakID
	}
	k.Sozler = sozler
	if k.Sozler.Bolumler == nil {
		k.Sozler.Bolumler = []SozBolumu{}
	}
	for i := range k.Sozler.Bolumler {
		if k.Sozler.Bolumler[i].Satirlar == nil {
			k.Sozler.Bolumler[i].Satirlar = []string{}
		}
	}
	k.Yapi = yapi
	if k.Yapi == nil {
		k.Yapi = []YapiOgesi{}
	}
	k.StemVar = stemVar
	if master != nil {
		k.MasterAnahtari = *master
	}
	k.BenimMi = k.SahipID == izleyiciID
	return &k, nil
}

func (d *PostgresDepo) YayinGetir(ctx context.Context, id, izleyiciID string) (*YayinKaydi, error) {
	return d.yayinGetir(ctx, "y.id", id, izleyiciID)
}

func (d *PostgresDepo) YayinSlugIleGetir(ctx context.Context, slug, izleyiciID string) (*YayinKaydi, error) {
	return d.yayinGetir(ctx, "y.slug", slug, izleyiciID)
}

func (d *PostgresDepo) YayinEkle(ctx context.Context, y YeniYayin) error {
	var kaynak any
	if y.KaynakYayinID != "" {
		kaynak = y.KaynakYayinID
	}
	if y.Hazirlik.Stems == nil {
		y.Hazirlik.Stems = []HazirlikStemi{}
	}
	_, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, aciklama, tur, bpm, ton_tonik, ton_mod,
			sure_sn, gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik,
			kaynak_yayin_id, kaynak_baslik, kaynak_sahip_adi, durum)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, NULLIF($18, ''), NULLIF($19, ''), 'hazirlaniyor')`,
		y.ID, y.Slug, y.SahipID, y.Baslik, y.Aciklama, y.Tur, y.Bpm, y.Ton.Tonik, y.Ton.Mod,
		y.SureSn, y.Gorunurluk, y.AltyapiAcik, y.YorumAcik, y.Sozler, y.Yapi, y.Hazirlik,
		kaynak, y.KaynakBaslik, y.KaynakSahipAdi)
	if kisit, ok := tekilIhlali(err); ok && kisit == "yayin_slug_key" {
		return errSlugCakisti
	}
	if err != nil {
		return fmt.Errorf("yayin eklenemedi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) YayinGuncelle(ctx context.Context, id, sahipID string, yama YayinYamasi) error {
	sonuc, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin
		   SET aciklama     = COALESCE($3, aciklama),
		       altyapi_acik = COALESCE($4, altyapi_acik),
		       gorunurluk   = COALESCE($5, gorunurluk),
		       yorum_acik   = COALESCE($6, yorum_acik)
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL`,
		id, sahipID, yama.Aciklama, yama.AltyapiAcik, yama.Gorunurluk, yama.YorumAcik)
	if err != nil {
		return fmt.Errorf("yayin guncellenemedi: %w", err)
	}
	if sonuc.RowsAffected() == 0 {
		return ErrBulunamadi
	}
	return nil
}

func (d *PostgresDepo) YayinSil(ctx context.Context, id, sahipID string) error {
	sonuc, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin SET silinme = now()
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL`, id, sahipID)
	if err != nil {
		return fmt.Errorf("yayin silinemedi: %w", err)
	}
	if sonuc.RowsAffected() == 0 {
		return ErrBulunamadi
	}
	return nil
}

func (d *PostgresDepo) YayinKaldir(ctx context.Context, id string) error {
	if _, err := d.havuz.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1 AND durum = 'hazirlaniyor'`, id); err != nil {
		return fmt.Errorf("yayin kaldirilamadi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) TekrarDenemeyeAl(ctx context.Context, id, sahipID string) error {
	sonuc, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin SET durum = 'hazirlaniyor'
		 WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL AND durum = 'hata'`, id, sahipID)
	if err != nil {
		return fmt.Errorf("yayin tekrar denemeye alinamadi: %w", err)
	}
	if sonuc.RowsAffected() == 1 {
		return nil
	}
	var varMi bool
	if err := d.havuz.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM sosyal.yayin WHERE id = $1 AND sahip_id = $2 AND silinme IS NULL)`,
		id, sahipID).Scan(&varMi); err != nil {
		return fmt.Errorf("yayin okunamadi: %w", err)
	}
	if varMi {
		return ErrDurum
	}
	return ErrBulunamadi
}

func (d *PostgresDepo) HazirlikGetir(ctx context.Context, id string) (*HazirlikKaydi, error) {
	var k HazirlikKaydi
	err := d.havuz.QueryRow(ctx, `
		SELECT id::text, sahip_id::text, durum, silinme IS NOT NULL, hazirlik
		  FROM sosyal.yayin WHERE id = $1`, id,
	).Scan(&k.ID, &k.SahipID, &k.Durum, &k.Silindi, &k.Hazirlik)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("hazirlik okunamadi: %w", err)
	}
	return &k, nil
}

func (d *PostgresDepo) Yayinla(ctx context.Context, id string, s HazirlikSonucu) error {
	stemler := s.StemListesi
	if stemler == nil {
		stemler = []StemKaydi{}
	}
	dalga := s.DalgaFormu
	if dalga == nil {
		dalga = []float64{}
	}
	_, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin
		   SET durum = 'yayinda', master_anahtari = $2, stem_listesi = $3,
		       dalga_formu = $4, onizleme_anahtari = NULLIF($5, '')
		 WHERE id = $1 AND durum = 'hazirlaniyor' AND silinme IS NULL`,
		id, s.MasterAnahtari, stemler, dalga, s.OnizlemeAnahtari)
	if err != nil {
		return fmt.Errorf("yayin yayinlanamadi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) HataYap(ctx context.Context, id string) error {
	_, err := d.havuz.Exec(ctx, `
		UPDATE sosyal.yayin SET durum = 'hata' WHERE id = $1 AND durum = 'hazirlaniyor' AND silinme IS NULL`, id)
	if err != nil {
		return fmt.Errorf("yayin hata durumuna alinamadi: %w", err)
	}
	return nil
}
