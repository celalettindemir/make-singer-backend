package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// YorumDeposu, sosyal.yorum islemleri (spec §4.5).
type YorumDeposu interface {
	// YorumEkle, yorumu yazar ve yazarin ozetiyle doner.
	YorumEkle(ctx context.Context, y YeniYorum) (*Yorum, error)
	// Yorumlar, yayinin silinmemis yorumlarini sozlesme sirasiyla en cok
	// limit+1 tane doner. SilebilirMiyim izleyiciye goredir (yazar ya da
	// yayin sahibi).
	Yorumlar(ctx context.Context, yayinID, izleyiciID string, imlec *YorumImleci, limit int) ([]Yorum, error)
	// YorumGetir, silme karari icin yorumu doner; yoksa ErrBulunamadi.
	YorumGetir(ctx context.Context, id string) (*YorumKaydi, error)
	// YorumSil, yumusak siler. Yoksa ya da zaten silinmisse ErrBulunamadi.
	YorumSil(ctx context.Context, id string) error
}

// yorumKolonlari, Yorum'un kolonlaridir: c yorum, p yazar profili.
const yorumKolonlari = `c.id::text, c.metin, c.zaman_ms, c.olusturma, c.yazar_id::text, p.kullanici_adi, p.gorunen_ad`

func yorumTara(satir pgx.Row, ekler ...any) (*Yorum, error) {
	var (
		y       Yorum
		yazarID string
	)
	hedefler := append([]any{&y.ID, &y.Metin, &y.ZamanMs, &y.Olusturma, &yazarID,
		&y.Yazar.KullaniciAdi, &y.Yazar.GorunenAd}, ekler...)
	if err := satir.Scan(hedefler...); err != nil {
		return nil, err
	}
	y.Yazar.AvatarRengi = AvatarRengi(yazarID)
	y.Olusturma = y.Olusturma.UTC()
	return &y, nil
}

func (d *PostgresDepo) YorumEkle(ctx context.Context, y YeniYorum) (*Yorum, error) {
	yorum, err := yorumTara(d.havuz.QueryRow(ctx, `
		WITH c AS (
		  INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, zaman_ms)
		  VALUES ($1, $2, $3, $4, $5)
		  RETURNING *
		)
		SELECT `+yorumKolonlari+` FROM c JOIN sosyal.profil p ON p.kullanici_id = c.yazar_id`,
		y.ID, y.YayinID, y.YazarID, y.Metin, y.ZamanMs))
	if err != nil {
		return nil, fmt.Errorf("yorum eklenemedi: %w", err)
	}
	return yorum, nil
}

// Yorumlar, iki grubu tek sorguda siralar: zaman_ms dolu olanlar once
// (zaman_ms, olusturma, id artan), damgasizlar sonra (olusturma, id
// azalan). Imlec grubu G=0 ise kalan zamanlilar ve tum damgasizlar,
// G=1 ise yalnizca kalan damgasizlar gelir.
func (d *PostgresDepo) Yorumlar(ctx context.Context, yayinID, izleyiciID string, imlec *YorumImleci, limit int) ([]Yorum, error) {
	var grup, z, t, id any
	if imlec != nil {
		grup, t, id = imlec.G, imlec.T, imlec.ID
		if imlec.Z != nil {
			z = *imlec.Z
		}
	}
	satirlar, err := d.havuz.Query(ctx, `
		SELECT `+yorumKolonlari+`, (c.yazar_id = $2 OR y.sahip_id = $2)
		  FROM sosyal.yorum c
		  JOIN sosyal.yayin y ON y.id = c.yayin_id
		  JOIN sosyal.profil p ON p.kullanici_id = c.yazar_id
		 WHERE c.yayin_id = $1 AND c.silinme IS NULL
		   AND ($3::int IS NULL
		        OR ($3::int = 0 AND (c.zaman_ms IS NULL
		            OR (c.zaman_ms, c.olusturma, c.id) > ($4::int, $5::timestamptz, $6::uuid)))
		        OR ($3::int = 1 AND c.zaman_ms IS NULL
		            AND (c.olusturma, c.id) < ($5::timestamptz, $6::uuid)))
		 ORDER BY (c.zaman_ms IS NULL), c.zaman_ms,
		          CASE WHEN c.zaman_ms IS NULL THEN c.olusturma END DESC,
		          CASE WHEN c.zaman_ms IS NULL THEN c.id END DESC,
		          c.olusturma, c.id
		 LIMIT $7`, yayinID, izleyiciID, grup, z, t, id, limit+1)
	if err != nil {
		return nil, fmt.Errorf("yorumlar okunamadi: %w", err)
	}
	defer satirlar.Close()
	yorumlar := []Yorum{}
	for satirlar.Next() {
		var silebilir bool
		y, err := yorumTara(satirlar, &silebilir)
		if err != nil {
			return nil, fmt.Errorf("yorum okunamadi: %w", err)
		}
		y.SilebilirMiyim = silebilir
		yorumlar = append(yorumlar, *y)
	}
	return yorumlar, satirlar.Err()
}

func (d *PostgresDepo) YorumGetir(ctx context.Context, id string) (*YorumKaydi, error) {
	var k YorumKaydi
	err := d.havuz.QueryRow(ctx, `
		SELECT c.id::text, c.yayin_id::text, c.yazar_id::text, y.sahip_id::text, c.silinme IS NOT NULL
		  FROM sosyal.yorum c JOIN sosyal.yayin y ON y.id = c.yayin_id
		 WHERE c.id = $1`, id).Scan(&k.ID, &k.YayinID, &k.YazarID, &k.YayinSahibiID, &k.Silindi)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("yorum okunamadi: %w", err)
	}
	return &k, nil
}

func (d *PostgresDepo) YorumSil(ctx context.Context, id string) error {
	sonuc, err := d.havuz.Exec(ctx,
		`UPDATE sosyal.yorum SET silinme = now() WHERE id = $1 AND silinme IS NULL`, id)
	if err != nil {
		return fmt.Errorf("yorum silinemedi: %w", err)
	}
	if sonuc.RowsAffected() == 0 {
		return ErrBulunamadi
	}
	return nil
}
