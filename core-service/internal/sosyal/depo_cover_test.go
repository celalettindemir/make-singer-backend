package sosyal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresDepo_CoverlarGorunurlukVeSira(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	kaynak := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, AltyapiAcik: true})
	baska := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, AltyapiAcik: true})

	c1 := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Olusturma: t0.Add(4 * time.Minute)})
	c2 := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, KaynakID: kaynak, Gorunurluk: GorunurlukTakipciler, Olusturma: t0.Add(3 * time.Minute)})
	c3 := testYayinEkle(t, havuz, testYayin{SahipID: ben, KaynakID: kaynak, Gorunurluk: GorunurlukLink, Olusturma: t0.Add(2 * time.Minute)})
	c4 := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Olusturma: t0.Add(time.Minute)})
	// Girmeyenler: yabancinin takipciler ve link cover'i, hazirlanan
	// (sahibine bile), silinmis, baska kaynagin cover'i.
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Gorunurluk: GorunurlukTakipciler})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: ben, KaynakID: kaynak, Durum: DurumHazirlaniyor})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak, Silindi: true})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: baska})

	cek := func(izleyici string) func(*ZamanImleci) ([]YayinOzeti, error) {
		return func(im *ZamanImleci) ([]YayinOzeti, error) { return d.Coverlar(ctx, kaynak, izleyici, im, 2) }
	}
	esitSira(t, "ben", tumSayfalar(t, cek(ben), 2), []string{c1, c2, c3, c4})
	esitSira(t, "yabanci izleyici", tumSayfalar(t, cek(uuid.NewString()), 2), []string{c1, c4})

	// Kaynak ozeti cover'larda doludur.
	ogeler, err := d.Coverlar(ctx, kaynak, ben, nil, 1)
	if err != nil || ogeler[0].Kaynak == nil || ogeler[0].Kaynak.YayinID != kaynak || ogeler[0].Kaynak.Silindi {
		t.Fatalf("kaynak ozeti = %+v, %v", ogeler, err)
	}
}

func TestPostgresDepo_StemListesi(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	id := testYayinEkle(t, havuz, testYayin{SahipID: uuid.NewString(), AltyapiAcik: true})

	bos, err := d.StemListesi(ctx, id)
	if err != nil || bos == nil || len(bos) != 0 {
		t.Fatalf("bos liste = %#v, %v", bos, err)
	}
	beklenen := []StemKaydi{{ID: "drums", Enstruman: "Davul", Sure: 12.5, Anahtar: "sosyal/" + id + "/stems/drums.mp3"}}
	if _, err := havuz.Exec(ctx, `UPDATE sosyal.yayin SET stem_listesi = $2 WHERE id = $1`, id, beklenen); err != nil {
		t.Fatal(err)
	}
	stemler, err := d.StemListesi(ctx, id)
	if err != nil || len(stemler) != 1 || stemler[0] != beklenen[0] {
		t.Fatalf("stemler = %+v, %v", stemler, err)
	}
	if _, err := d.StemListesi(ctx, uuid.NewString()); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan yayin: %v", err)
	}
}

// Profili olmayan sahibin cover'i listeye girmez (ozet profil birlesimi).
func TestPostgresDepo_CoverlarProfilsizSahibiGizler(t *testing.T) {
	havuz, d, ben, _, yabanci := akisKurulumu(t)
	ctx := context.Background()
	kaynak := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, AltyapiAcik: true})
	gorunen := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, KaynakID: kaynak})
	testYayinEkle(t, havuz, testYayin{SahipID: uuid.NewString(), KaynakID: kaynak})

	ogeler, err := d.Coverlar(ctx, kaynak, ben, nil, 10)
	if err != nil || len(ogeler) != 1 || ogeler[0].ID != gorunen {
		t.Fatalf("coverlar = %+v, %v; beklenen yalnizca %s", ogeler, err, gorunen)
	}
}
