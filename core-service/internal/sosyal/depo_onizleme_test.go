package sosyal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresDepo_OnizlemeYalnizcaHerkesVeLink(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")

	durumlar := []struct {
		y        testYayin
		gorunur  bool
		onizleme bool
	}{
		{testYayin{Slug: "Herkes01", Gorunurluk: GorunurlukHerkes}, true, true},
		{testYayin{Slug: "Linkli01", Gorunurluk: GorunurlukLink}, true, false},
		{testYayin{Slug: "Takipci1", Gorunurluk: GorunurlukTakipciler}, false, false},
		{testYayin{Slug: "Hazirla1", Durum: DurumHazirlaniyor}, false, false},
		{testYayin{Slug: "Hatali01", Durum: DurumHata}, false, false},
		{testYayin{Slug: "Silinmis", Silindi: true}, false, false},
	}
	for _, c := range durumlar {
		c.y.SahipID = sahip
		id := testYayinEkle(t, havuz, c.y)
		if c.onizleme {
			if _, err := havuz.Exec(ctx, `UPDATE sosyal.yayin SET onizleme_anahtari = $2, dalga_formu = '[0.25, 1]' WHERE id = $1`,
				id, "sosyal/"+id+"/onizleme.mp3"); err != nil {
				t.Fatal(err)
			}
		}
		k, err := d.OnizlemeGetir(ctx, c.y.Slug)
		if !c.gorunur {
			if !errors.Is(err, ErrBulunamadi) {
				t.Errorf("%s: %v, beklenen ErrBulunamadi", c.y.Slug, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.y.Slug, err)
		}
		if k.Baslik != "Baslik" || k.SureSn != 180 || k.Tur != "pop" || k.SahipID != sahip ||
			k.Sahip != (ProfilOzeti{KullaniciAdi: "sahip", GorunenAd: "sahip", AvatarRengi: AvatarRengi(sahip)}) {
			t.Errorf("%s: kayit = %+v", c.y.Slug, k)
		}
		if c.onizleme && (k.OnizlemeAnahtari != "sosyal/"+id+"/onizleme.mp3" || len(k.DalgaFormu) != 2) {
			t.Errorf("%s: onizleme = %q dalga %v", c.y.Slug, k.OnizlemeAnahtari, k.DalgaFormu)
		}
		if !c.onizleme && (k.OnizlemeAnahtari != "" || k.DalgaFormu == nil || len(k.DalgaFormu) != 0) {
			t.Errorf("%s: onizlemesiz = %q dalga %#v", c.y.Slug, k.OnizlemeAnahtari, k.DalgaFormu)
		}
	}
	if _, err := d.OnizlemeGetir(ctx, "Yok12345"); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan slug: %v", err)
	}
}
