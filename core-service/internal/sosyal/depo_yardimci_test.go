package sosyal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testYayin, ham SQL ile eklenecek bir yayin satiridir. Bos alanlar
// makul varsayilanlarla doldurulur; depo testleri boylece yayin ekleme
// ucunu beklemeden kurulabilir.
type testYayin struct {
	ID          string
	Slug        string
	SahipID     string
	Baslik      string
	Gorunurluk  string
	Durum       string
	AltyapiAcik bool
	KaynakID    string
	Olusturma   time.Time
	Silindi     bool
	Hazirlik    string
}

var slugSayaci int

func testYayinEkle(t *testing.T, havuz *pgxpool.Pool, y testYayin) string {
	t.Helper()
	if y.ID == "" {
		y.ID = uuid.NewString()
	}
	if y.Slug == "" {
		slugSayaci++
		y.Slug = fmt.Sprintf("T%07d", slugSayaci)
	}
	if y.Baslik == "" {
		y.Baslik = "Baslik"
	}
	if y.Gorunurluk == "" {
		y.Gorunurluk = GorunurlukHerkes
	}
	if y.Durum == "" {
		y.Durum = DurumYayinda
	}
	if y.Olusturma.IsZero() {
		y.Olusturma = time.Now()
	}
	if y.Hazirlik == "" {
		y.Hazirlik = `{"masterKey":"masters/u/p/m.wav","stems":[]}`
	}
	var kaynak any
	if y.KaynakID != "" {
		kaynak = y.KaynakID
	}
	var silinme any
	if y.Silindi {
		silinme = time.Now()
	}
	_, err := havuz.Exec(context.Background(), `
		INSERT INTO sosyal.yayin (id, slug, sahip_id, baslik, tur, bpm, ton_tonik, ton_mod, sure_sn,
			gorunurluk, altyapi_acik, yorum_acik, sozler, yapi, hazirlik, kaynak_yayin_id,
			kaynak_baslik, kaynak_sahip_adi, durum, olusturma, silinme)
		VALUES ($1, $2, $3, $4, 'pop', 120, 'A', 'minor', 180, $5, $6, true,
			'{"bolumler":[]}', '[]', $7, $8, CASE WHEN $8::uuid IS NULL THEN NULL ELSE 'Eski baslik' END,
			CASE WHEN $8::uuid IS NULL THEN NULL ELSE 'eski_ad' END, $9, $10, $11)`,
		y.ID, y.Slug, y.SahipID, y.Baslik, y.Gorunurluk, y.AltyapiAcik, y.Hazirlik, kaynak, y.Durum, y.Olusturma, silinme)
	if err != nil {
		t.Fatalf("test yayini eklenemedi: %v", err)
	}
	return y.ID
}

func testTakipEkle(t *testing.T, havuz *pgxpool.Pool, takipci, takipEdilen string) {
	t.Helper()
	if _, err := havuz.Exec(context.Background(),
		`INSERT INTO sosyal.takip (takipci_id, takip_edilen_id) VALUES ($1, $2)`, takipci, takipEdilen); err != nil {
		t.Fatalf("takip eklenemedi: %v", err)
	}
}

func testProfilEkle(t *testing.T, d *PostgresDepo, id, ad string) {
	t.Helper()
	if _, err := d.ProfilKaydet(context.Background(), ProfilKaydi{KullaniciID: id, KullaniciAdi: ad, GorunenAd: ad}); err != nil {
		t.Fatalf("profil eklenemedi: %v", err)
	}
}
