package social

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testBegeniEkle, verilen zamanda bir begeni yazar.
func testBegeniEkle(t *testing.T, havuz *pgxpool.Pool, kullanici, yayin string, zaman time.Time) {
	t.Helper()
	if _, err := havuz.Exec(context.Background(),
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id, olusturma) VALUES ($1, $2, $3)`,
		kullanici, yayin, zaman); err != nil {
		t.Fatalf("begeni eklenemedi: %v", err)
	}
}

func TestPostgresDepo_TakipTekrariGuvenli(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()

	for i := 0; i < 2; i++ {
		if err := d.Follow(ctx, a, b); err != nil {
			t.Fatalf("TakipEt %d: %v", i, err)
		}
	}
	if err := d.Follow(ctx, c, b); err != nil {
		t.Fatal(err)
	}
	durum, err := d.FollowStatus(ctx, a, b)
	if err != nil || durum.Takipci != 2 || !durum.TakipEdiyorMuyum {
		t.Fatalf("durum = %+v, %v", durum, err)
	}
	ters, err := d.FollowStatus(ctx, b, a)
	if err != nil || ters.Takipci != 0 || ters.TakipEdiyorMuyum {
		t.Fatalf("ters yon = %+v, %v", ters, err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Unfollow(ctx, a, b); err != nil {
			t.Fatalf("TakibiBirak %d: %v", i, err)
		}
	}
	durum, err = d.FollowStatus(ctx, a, b)
	if err != nil || durum.Takipci != 1 || durum.TakipEdiyorMuyum {
		t.Fatalf("birakmadan sonra = %+v, %v", durum, err)
	}
	if err := d.Follow(ctx, a, a); err == nil {
		t.Fatal("kendini takip CHECK kisitina takilmali")
	}
}

func TestPostgresDepo_Oneriler(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	simdi := time.Now()
	gun := 24 * time.Hour

	profil := func(ad string) string {
		id := uuid.NewString()
		testProfilEkle(t, d, id, ad)
		return id
	}
	begen := func(yayin string, n int, zaman time.Time) {
		for i := 0; i < n; i++ {
			testBegeniEkle(t, havuz, uuid.NewString(), yayin, zaman)
		}
	}

	ben := profil("ben")
	takipEdilen := profil("takip_edilen")
	cok := profil("cok_begenilen")
	eski := profil("eski_begeniler")
	sifir := profil("begenisiz")
	takipcili := profil("takipcili")
	linkci := profil("yalniz_link")
	silinmis := profil("silinmis_yayin")
	hazirlanan := profil("hazirlanan")
	profil("yayinsiz")

	testTakipEkle(t, havuz, ben, takipEdilen)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: takipEdilen}), 9, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: ben}), 9, simdi)

	y := testYayinEkle(t, havuz, testYayin{SahipID: cok, Olusturma: simdi.Add(-40 * gun)})
	begen(y, 5, simdi.Add(-29*gun))
	testBegeniEkle(t, havuz, cok, y, simdi) // kendi begenisi sayilmaz

	y = testYayinEkle(t, havuz, testYayin{SahipID: eski, Olusturma: simdi.Add(-40 * gun)})
	begen(y, 10, simdi.Add(-31*gun))
	begen(y, 1, simdi)

	testYayinEkle(t, havuz, testYayin{SahipID: sifir, Olusturma: simdi.Add(-time.Hour)})
	begen(testYayinEkle(t, havuz, testYayin{SahipID: takipcili, Gorunurluk: GorunurlukTakipciler}), 3, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: linkci, Gorunurluk: GorunurlukLink}), 20, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: silinmis, Silindi: true}), 20, simdi)
	begen(testYayinEkle(t, havuz, testYayin{SahipID: hazirlanan, Durum: DurumHazirlaniyor}), 20, simdi)

	oneriler, err := d.Suggestions(ctx, ben, 10)
	if err != nil {
		t.Fatalf("Oneriler: %v", err)
	}
	var adlar []string
	for _, o := range oneriler {
		adlar = append(adlar, o.KullaniciAdi)
	}
	esitSira(t, "oneriler", adlar, []string{"cok_begenilen", "takipcili", "eski_begeniler", "begenisiz"})

	ilk, err := d.Suggestions(ctx, ben, 2)
	if err != nil || len(ilk) != 2 || ilk[0].KullaniciAdi != "cok_begenilen" {
		t.Fatalf("limit 2 = %+v, %v", ilk, err)
	}
}

func TestPostgresDepo_OnerilerEsitlikteYeniYayinOnde(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	simdi := time.Now()
	ben := uuid.NewString()
	var beklenen []string
	for i := 0; i < 12; i++ {
		id, ad := uuid.NewString(), fmt.Sprintf("yayinci_%02d", i)
		testProfilEkle(t, d, id, ad)
		testYayinEkle(t, havuz, testYayin{SahipID: id, Olusturma: simdi.Add(-time.Duration(i) * time.Minute)})
		if i < 10 {
			beklenen = append(beklenen, ad)
		}
	}
	oneriler, err := d.Suggestions(context.Background(), ben, 10)
	if err != nil {
		t.Fatal(err)
	}
	var adlar []string
	for _, o := range oneriler {
		adlar = append(adlar, o.KullaniciAdi)
	}
	esitSira(t, "esitlik", adlar, beklenen)
}
