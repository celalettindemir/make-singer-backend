package social

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tumSayfalar, bir zaman listesini limit'lik sayfalarla sonuna kadar
// gezer ve id'leri sirayla doner.
func tumSayfalar(t *testing.T, cek func(*ZamanImleci) ([]YayinOzeti, error), limit int) []string {
	t.Helper()
	var (
		idler []string
		imlec *ZamanImleci
	)
	for sayfa := 0; sayfa < 20; sayfa++ {
		ogeler, err := cek(imlec)
		if err != nil {
			t.Fatalf("sayfa %d: %v", sayfa, err)
		}
		bitti := len(ogeler) <= limit
		if !bitti {
			ogeler = ogeler[:limit]
		}
		for _, o := range ogeler {
			idler = append(idler, o.ID)
		}
		if bitti {
			return idler
		}
		son := ogeler[len(ogeler)-1]
		imlec = &ZamanImleci{T: son.Olusturma, ID: son.ID}
	}
	t.Fatal("sayfalama bitmedi")
	return nil
}

func esitSira(t *testing.T, ad string, got, beklenen []string) {
	t.Helper()
	if len(got) != len(beklenen) {
		t.Fatalf("%s: %d oge, beklenen %d (%v)", ad, len(got), len(beklenen), got)
	}
	for i := range got {
		if got[i] != beklenen[i] {
			t.Fatalf("%s: sira %d = %s, beklenen %s", ad, i, got[i], beklenen[i])
		}
	}
}

func akisKurulumu(t *testing.T) (*pgxpool.Pool, *PostgresStore, string, string, string) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ben, takipEdilen, yabanci := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, ben, "ben")
	testProfilEkle(t, d, takipEdilen, "takip_edilen")
	testProfilEkle(t, d, yabanci, "yabanci")
	testTakipEkle(t, havuz, ben, takipEdilen)
	return havuz, d, ben, takipEdilen, yabanci
}

func TestPostgresDepo_AkisYeniVeSayfalama(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	ayniAn := t0.Add(10 * time.Minute)
	// Beklenen sira: yeniden eskiye; ayni anda olusanlar id'ye gore azalan.
	a := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: t0.Add(30 * time.Minute)})
	b := testYayinEkle(t, havuz, testYayin{SahipID: ben, Olusturma: t0.Add(20 * time.Minute)})
	c1 := testYayinEkle(t, havuz, testYayin{ID: "ffffffff-0000-4000-8000-000000000001", SahipID: takipEdilen, Olusturma: ayniAn})
	c2 := testYayinEkle(t, havuz, testYayin{ID: "00000000-0000-4000-8000-000000000001", SahipID: takipEdilen, Olusturma: ayniAn})
	e := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: t0})
	// Akisa girmeyenler:
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Gorunurluk: GorunurlukTakipciler})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Durum: DurumHazirlaniyor})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Silindi: true})

	beklenen := []string{a, b, c1, c2, e}
	for _, limit := range []int{1, 2, 5, 50} {
		got := tumSayfalar(t, func(im *ZamanImleci) ([]YayinOzeti, error) { return d.FeedNew(ctx, ben, im, limit) }, limit)
		esitSira(t, "yeni", got, beklenen)
	}
}

func TestPostgresDepo_AkisTakip(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	herkes := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Olusturma: t0.Add(2 * time.Minute)})
	takipcilere := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukTakipciler, Olusturma: t0.Add(time.Minute)})
	testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Durum: DurumHata})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci})
	testYayinEkle(t, havuz, testYayin{SahipID: ben})

	got := tumSayfalar(t, func(im *ZamanImleci) ([]YayinOzeti, error) { return d.FeedFollowing(ctx, ben, im, 20) }, 20)
	esitSira(t, "takip", got, []string{herkes, takipcilere})
}

func TestPostgresDepo_ProfilYayinlari(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	herkes := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Olusturma: t0.Add(5 * time.Minute)})
	takipcilere := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukTakipciler, Olusturma: t0.Add(4 * time.Minute)})
	link := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Gorunurluk: GorunurlukLink, Olusturma: t0.Add(3 * time.Minute)})
	hazirlanan := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Durum: DurumHazirlaniyor, Olusturma: t0.Add(2 * time.Minute)})
	testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Silindi: true})
	cover := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, KaynakID: herkes, Olusturma: t0.Add(time.Minute)})

	liste := func(izleyici string, coverlar bool) []string {
		return tumSayfalar(t, func(im *ZamanImleci) ([]YayinOzeti, error) {
			return d.ListProfilePublications(ctx, takipEdilen, izleyici, coverlar, im, 2)
		}, 2)
	}
	esitSira(t, "sahip", liste(takipEdilen, false), []string{herkes, takipcilere, link, hazirlanan})
	esitSira(t, "takipci", liste(ben, false), []string{herkes, takipcilere})
	esitSira(t, "yabanci", liste(yabanci, false), []string{herkes})
	esitSira(t, "coverlar", liste(yabanci, true), []string{cover})

	ogeler, _ := d.ListProfilePublications(ctx, takipEdilen, yabanci, true, nil, 20)
	if ogeler[0].Kaynak == nil || ogeler[0].Kaynak.YayinID != herkes || ogeler[0].Sahip.KullaniciAdi != "takip_edilen" {
		t.Fatalf("cover ozeti = %+v", ogeler[0])
	}
}

func begeniEkle(t *testing.T, havuz *pgxpool.Pool, yayinID string, adet int, zaman time.Time) {
	t.Helper()
	for i := 0; i < adet; i++ {
		if _, err := havuz.Exec(context.Background(),
			`INSERT INTO sosyal.begeni (kullanici_id, yayin_id, olusturma) VALUES ($1, $2, $3)`,
			uuid.NewString(), yayinID, zaman); err != nil {
			t.Fatalf("begeni: %v", err)
		}
	}
}

func TestPostgresDepo_AkisSeninIcinPuanVeSayfalama(t *testing.T) {
	havuz, d, ben, takipEdilen, yabanci := akisKurulumu(t)
	ctx := context.Background()
	referans := time.Now().Truncate(time.Millisecond)

	// Ayni yasta: daha cok begeni yukarida. Eski yayin asagida.
	populer := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: referans.Add(-2 * time.Hour)})
	begeniEkle(t, havuz, populer, 5, referans.Add(-time.Hour))
	sakin := testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen, Olusturma: referans.Add(-2 * time.Hour)})
	eski := testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: referans.Add(-72 * time.Hour)})
	// 7 gunden eski begeniler ve referanstan sonraki begeniler sayilmaz.
	begeniEkle(t, havuz, eski, 50, referans.Add(-8*24*time.Hour))
	begeniEkle(t, havuz, sakin, 50, referans.Add(time.Hour))
	// Akisa girmeyenler: kendi yayinim, referanstan sonra olusan, herkes olmayan.
	testYayinEkle(t, havuz, testYayin{SahipID: ben, Olusturma: referans.Add(-time.Hour)})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Olusturma: referans.Add(time.Minute)})
	testYayinEkle(t, havuz, testYayin{SahipID: yabanci, Gorunurluk: GorunurlukLink, Olusturma: referans.Add(-time.Hour)})

	hepsi, err := d.FeedForYou(ctx, ben, referans, nil, 50)
	if err != nil {
		t.Fatalf("AkisSeninIcin: %v", err)
	}
	var idler []string
	for _, o := range hepsi {
		idler = append(idler, o.ID)
	}
	esitSira(t, "senin icin", idler, []string{populer, sakin, eski})
	// (1+5)/(2+2)^1.5 = 0.75
	if p := hepsi[0].Score; p < 0.7499 || p > 0.7501 {
		t.Fatalf("populer puani = %v, beklenen 0.75", p)
	}

	// Tek ogelik sayfalarla ayni sira, tekrar yok.
	var (
		gezilen []string
		imlec   *PuanImleci
	)
	for i := 0; i < 5; i++ {
		ogeler, err := d.FeedForYou(ctx, ben, referans, imlec, 1)
		if err != nil {
			t.Fatalf("sayfa: %v", err)
		}
		gezilen = append(gezilen, ogeler[0].ID)
		if len(ogeler) == 1 {
			break
		}
		imlec = &PuanImleci{P: ogeler[0].Score, ID: ogeler[0].ID, N: referans.UnixMilli()}
	}
	esitSira(t, "senin icin sayfalari", gezilen, []string{populer, sakin, eski})
}

func TestPostgresDepo_Begeni(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip, ben := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	id := testYayinEkle(t, havuz, testYayin{SahipID: sahip})
	for i := 0; i < 2; i++ {
		if err := d.Like(ctx, ben, id); err != nil {
			t.Fatalf("Begen %d: %v", i, err)
		}
	}
	k, _ := d.GetPublication(ctx, id, ben)
	if k.BegeniSayisi != 1 || !k.BegendimMi {
		t.Fatalf("begeni tekrari guvenli olmali: %d %v", k.BegeniSayisi, k.BegendimMi)
	}
	for i := 0; i < 2; i++ {
		if err := d.Unlike(ctx, ben, id); err != nil {
			t.Fatalf("Kaldir %d: %v", i, err)
		}
	}
	k, _ = d.GetPublication(ctx, id, ben)
	if k.BegeniSayisi != 0 || k.BegendimMi {
		t.Fatalf("begeni kaldirilmali: %d %v", k.BegeniSayisi, k.BegendimMi)
	}
}
