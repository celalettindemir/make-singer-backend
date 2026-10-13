package social

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresDepo_ProfilKaydetVeGetir(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	ctx := context.Background()
	id := uuid.NewString()

	if _, err := d.GetProfile(ctx, id); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("profil yokken ErrProfilYok bekleniyordu: %v", err)
	}
	k, err := d.UpsertProfile(ctx, ProfilKaydi{KullaniciID: id, KullaniciAdi: "ali", GorunenAd: "Ali", Bio: "b"})
	if err != nil || k.KullaniciAdi != "ali" || k.Olusturma.IsZero() {
		t.Fatalf("kaydet = %+v, %v", k, err)
	}
	// Ikinci kayit gunceller (PUT hem olusturur hem gunceller).
	if _, err := d.UpsertProfile(ctx, ProfilKaydi{KullaniciID: id, KullaniciAdi: "ali2", GorunenAd: "Ali Veli", Bio: ""}); err != nil {
		t.Fatalf("guncelle: %v", err)
	}
	k, err = d.GetProfile(ctx, id)
	if err != nil || k.KullaniciAdi != "ali2" || k.GorunenAd != "Ali Veli" || k.Bio != "" {
		t.Fatalf("getir = %+v, %v", k, err)
	}
	if _, err := d.GetProfileByUsername(ctx, "ali"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("eski ad bulunmamali: %v", err)
	}
	if k, err := d.GetProfileByUsername(ctx, "ali2"); err != nil || k.KullaniciID != id {
		t.Fatalf("adla getir = %+v, %v", k, err)
	}
}

func TestPostgresDepo_AlinmisAd(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, a, "ayse")
	_, err := d.UpsertProfile(ctx, ProfilKaydi{KullaniciID: b, KullaniciAdi: "ayse", GorunenAd: "Ayse"})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("ErrKullaniciAdiAlinmis bekleniyordu: %v", err)
	}
	sahipler, err := d.UsernameOwners(ctx, []string{"ayse", "ayse2", "yok"})
	if err != nil || len(sahipler) != 1 || sahipler["ayse"] != a {
		t.Fatalf("AdSahipleri = %v, %v", sahipler, err)
	}
	if bos, err := d.UsernameOwners(ctx, nil); err != nil || len(bos) != 0 {
		t.Fatalf("bos liste = %v, %v", bos, err)
	}
}

// sarki/cover sayaci gorunurluk kuralini ve durum=yayinda'yi uygular.
func TestPostgresDepo_ProfilSayilari(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	sahip, takipci, yabanci := uuid.NewString(), uuid.NewString(), uuid.NewString()

	orijinal := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: GorunurlukHerkes})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: GorunurlukTakipciler})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHata})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, Silindi: true})
	testYayinEkle(t, havuz, testYayin{SahipID: sahip, KaynakID: orijinal})
	testTakipEkle(t, havuz, takipci, sahip)
	testTakipEkle(t, havuz, sahip, yabanci)

	durumlar := []struct {
		ad        string
		izleyici  string
		sarki     int
		cover     int
		takipEder bool
	}{
		{"sahip", sahip, 3, 1, false},
		{"takipci", takipci, 2, 1, true},
		{"yabanci", yabanci, 1, 1, false},
	}
	for _, dr := range durumlar {
		s, err := d.ProfileCounts(ctx, sahip, dr.izleyici)
		if err != nil {
			t.Fatalf("%s: %v", dr.ad, err)
		}
		if s.Istatistik.Sarki != dr.sarki || s.Istatistik.Cover != dr.cover || s.TakipEdiyorMuyum != dr.takipEder {
			t.Errorf("%s: %+v", dr.ad, s)
		}
		if s.Istatistik.Takipci != 1 || s.Istatistik.Takip != 1 {
			t.Errorf("%s takip sayaclari: %+v", dr.ad, s.Istatistik)
		}
	}
}
