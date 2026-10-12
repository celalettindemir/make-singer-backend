package sosyal

import (
	"context"
	"fmt"
)

// TakipDeposu, sosyal.takip islemleri ve oneriler (spec §4.4).
type TakipDeposu interface {
	// TakipEt ve TakibiBirak, tekrari guvenlidir.
	TakipEt(ctx context.Context, takipciID, takipEdilenID string) error
	TakibiBirak(ctx context.Context, takipciID, takipEdilenID string) error
	// TakipDurumu, takip edilenin takipci sayisini ve takipcinin onu
	// takip edip etmedigini doner.
	TakipDurumu(ctx context.Context, takipciID, takipEdilenID string) (*TakipDurumu, error)
	// Oneriler, izleyicinin takip etmedigi (kendisi haric) yayincilari son
	// 30 gunde aldiklari begeniye gore en cok limit tane doner.
	Oneriler(ctx context.Context, izleyiciID string, limit int) ([]ProfilKaydi, error)
}

func (d *PostgresDepo) TakipEt(ctx context.Context, takipciID, takipEdilenID string) error {
	if _, err := d.havuz.Exec(ctx, `
		INSERT INTO sosyal.takip (takipci_id, takip_edilen_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, takipciID, takipEdilenID); err != nil {
		return fmt.Errorf("takip eklenemedi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) TakibiBirak(ctx context.Context, takipciID, takipEdilenID string) error {
	if _, err := d.havuz.Exec(ctx, `
		DELETE FROM sosyal.takip WHERE takipci_id = $1 AND takip_edilen_id = $2`,
		takipciID, takipEdilenID); err != nil {
		return fmt.Errorf("takip kaldirilamadi: %w", err)
	}
	return nil
}

func (d *PostgresDepo) TakipDurumu(ctx context.Context, takipciID, takipEdilenID string) (*TakipDurumu, error) {
	var t TakipDurumu
	if err := d.havuz.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.takip WHERE takip_edilen_id = $2),
		       EXISTS (SELECT 1 FROM sosyal.takip WHERE takipci_id = $1 AND takip_edilen_id = $2)`,
		takipciID, takipEdilenID).Scan(&t.Takipci, &t.TakipEdiyorMuyum); err != nil {
		return nil, fmt.Errorf("takip durumu okunamadi: %w", err)
	}
	return &t, nil
}

// Oneriler: yayinci, herkes ya da takipciler gorunurlugunde en az bir
// yayinda ve silinmemis yayini olan profildir (link yayinlari onu
// onerilere sokmaz: varligini sizdirirdi). Puan, bu yayinlarin son 30
// gunde baskalarindan aldigi begenilerdir (sahibin kendi begenisi
// sayilmaz). Esitlikte son yayini yeni olan, sonra kimlik one gecer.
func (d *PostgresDepo) Oneriler(ctx context.Context, izleyiciID string, limit int) ([]ProfilKaydi, error) {
	satirlar, err := d.havuz.Query(ctx, `
		SELECT `+profilKolonlariP+`
		  FROM sosyal.profil p
		  JOIN LATERAL (
		    SELECT count(b.yayin_id) AS begeni, max(y.olusturma) AS son
		      FROM sosyal.yayin y
		      LEFT JOIN sosyal.begeni b
		        ON b.yayin_id = y.id AND b.kullanici_id <> y.sahip_id
		       AND b.olusturma > now() - interval '30 days'
		     WHERE y.sahip_id = p.kullanici_id AND y.durum = 'yayinda' AND y.silinme IS NULL
		       AND y.gorunurluk IN ('herkes', 'takipciler')
		  ) s ON s.son IS NOT NULL
		 WHERE p.kullanici_id <> $1
		   AND NOT EXISTS (SELECT 1 FROM sosyal.takip t WHERE t.takipci_id = $1 AND t.takip_edilen_id = p.kullanici_id)
		 ORDER BY s.begeni DESC, s.son DESC, p.kullanici_id
		 LIMIT $2`, izleyiciID, limit)
	if err != nil {
		return nil, fmt.Errorf("oneriler okunamadi: %w", err)
	}
	defer satirlar.Close()
	kayitlar := []ProfilKaydi{}
	for satirlar.Next() {
		k, err := profilTara(satirlar)
		if err != nil {
			return nil, fmt.Errorf("oneri okunamadi: %w", err)
		}
		kayitlar = append(kayitlar, *k)
	}
	return kayitlar, satirlar.Err()
}
