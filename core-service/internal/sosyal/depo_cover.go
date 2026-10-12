package sosyal

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CoverDeposu, cover listesi ve cover baslatma sorgulari (spec §4.6).
type CoverDeposu interface {
	// Coverlar, kaynagin yayinda ve silinmemis cover'larini izleyicinin
	// listede gorebildigi kadariyla yeniden eskiye en cok limit+1 tane
	// doner.
	Coverlar(ctx context.Context, kaynakID, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error)
	// StemListesi, yayinin kopyalanmis stem'lerini (ic anahtarlariyla)
	// doner; yayin yoksa ErrBulunamadi.
	StemListesi(ctx context.Context, yayinID string) ([]StemKaydi, error)
}

func (d *PostgresDepo) Coverlar(ctx context.Context, kaynakID, izleyiciID string, imlec *ZamanImleci, limit int) ([]YayinOzeti, error) {
	kosul := `y.kaynak_yayin_id = $2 AND y.durum = 'yayinda' AND ` + gorunurlukKosulu("y", "$1", false)
	return d.zamanSayfasi(ctx, kosul, imlec, limit, izleyiciID, kaynakID)
}

func (d *PostgresDepo) StemListesi(ctx context.Context, yayinID string) ([]StemKaydi, error) {
	var stemler []StemKaydi
	err := d.havuz.QueryRow(ctx, `SELECT stem_listesi FROM sosyal.yayin WHERE id = $1`, yayinID).Scan(&stemler)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBulunamadi
	}
	if err != nil {
		return nil, fmt.Errorf("stem listesi okunamadi: %w", err)
	}
	if stemler == nil {
		stemler = []StemKaydi{}
	}
	return stemler, nil
}
