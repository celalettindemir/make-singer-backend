package sosyal

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AkisDeposu, liste uclarinin sorgulari. Her yontem en cok limit+1 oge
// doner; fazlalik servisin "sonraki sayfa var" isaretidir.
type AkisDeposu interface {
	// ProfilYayinlari, sahibin orijinallerini ya da cover'larini
	// izleyicinin gorebildigi kadariyla yeniden eskiye listeler.
	ProfilYayinlari(ctx context.Context, sahipID, izleyiciID string, coverlar bool, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// AkisYeni: herkes, yayinda, silinmemis; yeniden eskiye.
	AkisYeni(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// AkisTakip: takip edilenlerin herkes ve takipciler yayinlari.
	AkisTakip(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// AkisSeninIcin: izleyicinin olmayan herkes yayinlari, referans
	// zamana gore puanla azalan.
	AkisSeninIcin(ctx context.Context, izleyiciID string, referans time.Time, imlec *PuanImleci, limit int) ([]PuanliOzet, error)
}

// BegeniDeposu, sosyal.begeni islemleri. Ikisi de tekrari guvenlidir.
type BegeniDeposu interface {
	Begen(ctx context.Context, kullaniciID, yayinID string) error
	BegeniyiKaldir(ctx context.Context, kullaniciID, yayinID string) error
}

// PuanliOzet, puanla siralanan bir akis ogesidir; puan imlece girer.
type PuanliOzet struct {
	YayinOzeti
	Puan float64
}

// zamanSayfasi, $1 izleyici olmak uzere verilen kosul ve parametrelerle
// zamana gore azalan bir sayfa ceker. Imlec parametreleri sona eklenir.
func (d *PostgresDepo) zamanSayfasi(ctx context.Context, kosul string, imlec *ZamanImleci, limit int, args ...any) ([]YayinOzeti, error) {
	var imlecT, imlecID any
	if imlec != nil {
		imlecT, imlecID = imlec.T, imlec.ID
	}
	n := len(args)
	args = append(args, imlecT, imlecID, limit+1)
	sorgu := fmt.Sprintf(`SELECT %s %s
		WHERE %s
		  AND ($%d::timestamptz IS NULL OR (y.olusturma, y.id) < ($%d::timestamptz, $%d::uuid))
		ORDER BY y.olusturma DESC, y.id DESC
		LIMIT $%d`, ozetSecimi, ozetKaynagi, kosul, n+1, n+1, n+2, n+3)
	satirlar, err := d.havuz.Query(ctx, sorgu, args...)
	if err != nil {
		return nil, fmt.Errorf("liste okunamadi: %w", err)
	}
	return ozetleriTara(satirlar)
}

func ozetleriTara(satirlar pgx.Rows) ([]YayinOzeti, error) {
	defer satirlar.Close()
	ogeler := []YayinOzeti{}
	for satirlar.Next() {
		var t ozetTarayici
		if err := satirlar.Scan(t.hedefler()...); err != nil {
			return nil, fmt.Errorf("liste ogesi okunamadi: %w", err)
		}
		ogeler = append(ogeler, t.ozet())
	}
	return ogeler, satirlar.Err()
}

func (d *PostgresDepo) ProfilYayinlari(ctx context.Context, sahipID, izleyiciID string, coverlar bool, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.sahip_id = $2 AND (y.kaynak_yayin_id IS NOT NULL) = $3 AND ` + gorunurlukKosulu("y", "$1", false)
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID, sahipID, coverlar)
}

func (d *PostgresDepo) AkisYeni(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.gorunurluk = 'herkes' AND y.durum = 'yayinda' AND y.silinme IS NULL`
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID)
}

func (d *PostgresDepo) AkisTakip(ctx context.Context, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.gorunurluk IN ('herkes', 'takipciler') AND y.durum = 'yayinda' AND y.silinme IS NULL
		AND EXISTS (SELECT 1 FROM sosyal.takip tk WHERE tk.takipci_id = $1 AND tk.takip_edilen_id = y.sahip_id)`
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID)
}

// puanIfadesi, spec §5.1: (1 + b + 2y + 3c) / (saat + 2)^1.5. b, y, c
// referanstan onceki 7 gunun begeni, yorum ve cover sayilaridir; saat
// yayindan referansa gecen suredir. Referanstan sonraki etkilesim sayilmaz
// ki ayni referansla cekilen sayfalarda puan sabit kalsin.
const puanIfadesi = `
	(1
	 + (SELECT count(*) FROM sosyal.begeni b
	     WHERE b.yayin_id = y.id AND b.olusturma > $2::timestamptz - interval '7 days' AND b.olusturma <= $2::timestamptz)
	 + 2 * (SELECT count(*) FROM sosyal.yorum c
	     WHERE c.yayin_id = y.id AND c.silinme IS NULL
	       AND c.olusturma > $2::timestamptz - interval '7 days' AND c.olusturma <= $2::timestamptz)
	 + 3 * (SELECT count(*) FROM sosyal.yayin cv
	     WHERE cv.kaynak_yayin_id = y.id AND cv.durum = 'yayinda' AND cv.silinme IS NULL
	       AND cv.olusturma > $2::timestamptz - interval '7 days' AND cv.olusturma <= $2::timestamptz)
	)::float8
	/ power(extract(epoch FROM ($2::timestamptz - y.olusturma))::float8 / 3600.0 + 2, 1.5)`

func (d *PostgresDepo) AkisSeninIcin(ctx context.Context, izleyiciID string, referans time.Time, imlec *PuanImleci, limit int) ([]PuanliOzet, error) {
	var imlecP, imlecID any
	if imlec != nil {
		imlecP, imlecID = imlec.P, imlec.ID
	}
	// Referanstan sonra olusan yayin bu akis oturumuna girmez: hem puani
	// tanimsiz olur (negatif saat) hem de sayfalar kayar.
	satirlar, err := d.havuz.Query(ctx, `
		SELECT s.* FROM (
		  SELECT `+ozetSecimi+`, `+puanIfadesi+` AS puan, y.id AS sira_id
		  `+ozetKaynagi+`
		  WHERE y.gorunurluk = 'herkes' AND y.durum = 'yayinda' AND y.silinme IS NULL
		    AND y.sahip_id <> $1 AND y.olusturma <= $2::timestamptz
		) s
		WHERE ($3::float8 IS NULL OR (s.puan, s.sira_id) < ($3::float8, $4::uuid))
		ORDER BY s.puan DESC, s.sira_id DESC
		LIMIT $5`, izleyiciID, referans, imlecP, imlecID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("akis okunamadi: %w", err)
	}
	defer satirlar.Close()
	ogeler := []PuanliOzet{}
	for satirlar.Next() {
		var (
			t      ozetTarayici
			puan   float64
			siraID string
		)
		if err := satirlar.Scan(append(t.hedefler(), &puan, &siraID)...); err != nil {
			return nil, fmt.Errorf("akis ogesi okunamadi: %w", err)
		}
		ogeler = append(ogeler, PuanliOzet{YayinOzeti: t.ozet(), Puan: puan})
	}
	return ogeler, satirlar.Err()
}

func (d *PostgresDepo) Begen(ctx context.Context, kullaniciID, yayinID string) error {
	if _, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, kullaniciID, yayinID); err != nil {
		return fmt.Errorf("begeni eklenemedi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) BegeniyiKaldir(ctx context.Context, kullaniciID, yayinID string) error {
	if _, err := d.havuz.Exec(ctx, `
		DELETE FROM sosyal.begeni WHERE kullanici_id = $1 AND yayin_id = $2`, kullaniciID, yayinID); err != nil {
		return fmt.Errorf("begeni kaldirilamadi: %w", err)
	}
	return nil
}
