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

// KullaniciVerisiniSil, kullanicinin sosyal kayitlarini tek islemde siler:
// begenileri, yorumlari, takipleri (iki yon), sikayetleri, yayinlari
// (onlara bagli begeni, yorum ve sikayetler cascade ile) ve profili.
// Baskalarinin cover'lari kalir; kaynaklari silindi olarak gorunur.
func (d *PostgresDepo) KullaniciVerisiniSil(ctx context.Context, kullaniciID string) error {
	islem, err := d.havuz.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sosyal silme islemi baslatilamadi: %w", err)
	}
	defer func() { _ = islem.Rollback(ctx) }()
	for _, sorgu := range []string{
		`DELETE FROM sosyal.begeni WHERE kullanici_id = $1`,
		`DELETE FROM sosyal.yorum WHERE yazar_id = $1`,
		`DELETE FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1`,
		`DELETE FROM sosyal.sikayet WHERE bildiren_id = $1`,
		`DELETE FROM sosyal.yayin WHERE sahip_id = $1`,
		`DELETE FROM sosyal.profil WHERE kullanici_id = $1`,
	} {
		if _, err := islem.Exec(ctx, sorgu, kullaniciID); err != nil {
			return fmt.Errorf("sosyal veri silinemedi: %w", err)
		}
	}
	if err := islem.Commit(ctx); err != nil {
		return fmt.Errorf("sosyal silme commit edilemedi: %w", err)
	}
	return nil
}
