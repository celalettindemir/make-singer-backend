package sosyal

import (
	"context"
	"fmt"
)

// YayinlariListele, kullanicinin TUM yayin kimliklerini (yumusak
// silinmisler dahil) doner. Hesap silme her biri icin sosyal/{id}/ onekini
// temizler; satirlar ancak R2 temizligi basarili olunca silinir ki
// gecikmeli tekrar ayni kimlikleri yeniden bulabilsin.
func (d *PostgresDepo) YayinlariListele(ctx context.Context, kullaniciID string) ([]string, error) {
	satirlar, err := d.havuz.Query(ctx,
		`SELECT id::text FROM sosyal.yayin WHERE sahip_id = $1 ORDER BY olusturma`, kullaniciID)
	if err != nil {
		return nil, fmt.Errorf("yayinlar listelenemedi: %w", err)
	}
	defer satirlar.Close()
	idler := []string{}
	for satirlar.Next() {
		var id string
		if err := satirlar.Scan(&id); err != nil {
			return nil, fmt.Errorf("yayin kimligi okunamadi: %w", err)
		}
		idler = append(idler, id)
	}
	return idler, satirlar.Err()
}

// yorumSikayetleriniSil, kullanicinin yorumlarina yapilan (baskalarinin)
// sikayetleri siler. hedef_id cok bicimli oldugundan cascade gelmez;
// yorumlardan ONCE kosmalidir.
const yorumSikayetleriniSil = `DELETE FROM sosyal.sikayet
	WHERE hedef_turu = 'yorum' AND hedef_id IN (SELECT id FROM sosyal.yorum WHERE yazar_id = $1)`

// izSorgulari, yayinlar disindaki tum kullanici kayitlarini siler.
var izSorgulari = []string{
	yorumSikayetleriniSil,
	`DELETE FROM sosyal.begeni WHERE kullanici_id = $1`,
	`DELETE FROM sosyal.yorum WHERE yazar_id = $1`,
	`DELETE FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1`,
	`DELETE FROM sosyal.sikayet WHERE bildiren_id = $1`,
	`DELETE FROM sosyal.profil WHERE kullanici_id = $1`,
}

// KullaniciIzleriniSil, kullanicinin yayinlari DISINDAKI kayitlarini tek
// islemde siler: yorumlarina yapilan sikayetler, begenileri, yorumlari,
// takipleri (iki yon), sikayetleri ve profili. Okuma yollari profile
// baglandigi icin yayinlari da aninda gorunmez olur; satirlar R2 temizligi
// bitene kadar kalir ki gecikmeli tekrar kimlikleri yeniden listeleyebilsin.
func (d *PostgresDepo) KullaniciIzleriniSil(ctx context.Context, kullaniciID string) error {
	return d.islemdeSil(ctx, kullaniciID, izSorgulari)
}

// KullaniciVerisiniSil, kullanicinin sosyal kayitlarini tek islemde siler:
// izleri (KullaniciIzleriniSil ile ayni) ve yayinlari (onlara bagli begeni,
// yorum ve sikayetler cascade ile). Baskalarinin cover'lari kalir;
// kaynaklari silindi olarak gorunur.
func (d *PostgresDepo) KullaniciVerisiniSil(ctx context.Context, kullaniciID string) error {
	sorgular := append(append([]string{}, izSorgulari...), `DELETE FROM sosyal.yayin WHERE sahip_id = $1`)
	return d.islemdeSil(ctx, kullaniciID, sorgular)
}

func (d *PostgresDepo) islemdeSil(ctx context.Context, kullaniciID string, sorgular []string) error {
	islem, err := d.havuz.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sosyal silme islemi baslatilamadi: %w", err)
	}
	defer func() { _ = islem.Rollback(ctx) }()
	for _, sorgu := range sorgular {
		if _, err := islem.Exec(ctx, sorgu, kullaniciID); err != nil {
			return fmt.Errorf("sosyal veri silinemedi: %w", err)
		}
	}
	if err := islem.Commit(ctx); err != nil {
		return fmt.Errorf("sosyal silme commit edilemedi: %w", err)
	}
	return nil
}
