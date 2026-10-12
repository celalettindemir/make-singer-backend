package sosyal

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresDepo_KullaniciVerisiniSil(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	giden, kalan := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, giden, "giden")
	testProfilEkle(t, d, kalan, "kalan")

	canli := testYayinEkle(t, havuz, testYayin{SahipID: giden, Baslik: "Orijinal"})
	silinmis := testYayinEkle(t, havuz, testYayin{SahipID: giden, Silindi: true})
	kalaninYayini := testYayinEkle(t, havuz, testYayin{SahipID: kalan})
	cover := testYayinEkle(t, havuz, testYayin{SahipID: kalan, KaynakID: canli})
	testTakipEkle(t, havuz, giden, kalan)
	testTakipEkle(t, havuz, kalan, giden)
	for _, sorgu := range []string{
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + giden + `', '` + kalaninYayini + `')`,
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + kalan + `', '` + canli + `')`,
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + kalan + `', '` + kalaninYayini + `')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), '` + kalaninYayini + `', '` + giden + `', 'g')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES (gen_random_uuid(), '` + kalaninYayini + `', '` + kalan + `', 'k')`,
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), '` + giden + `', 'yayin', '` + kalaninYayini + `', '` + kalaninYayini + `', 'spam')`,
	} {
		if _, err := havuz.Exec(ctx, sorgu); err != nil {
			t.Fatalf("kurulum: %v", err)
		}
	}

	idler, err := d.YayinlariListele(ctx, giden)
	if err != nil {
		t.Fatalf("YayinlariListele: %v", err)
	}
	sort.Strings(idler)
	beklenen := []string{canli, silinmis}
	sort.Strings(beklenen)
	if len(idler) != 2 || idler[0] != beklenen[0] || idler[1] != beklenen[1] {
		t.Fatalf("silinmisler dahil iki yayin bekleniyordu: %v", idler)
	}

	if err := d.KullaniciVerisiniSil(ctx, giden); err != nil {
		t.Fatalf("KullaniciVerisiniSil: %v", err)
	}
	if err := d.KullaniciVerisiniSil(ctx, giden); err != nil {
		t.Fatalf("tekrar silme hatasiz olmali: %v", err)
	}

	var gidenKalan int
	if err := havuz.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.profil WHERE kullanici_id = $1)
		     + (SELECT count(*) FROM sosyal.yayin WHERE sahip_id = $1)
		     + (SELECT count(*) FROM sosyal.begeni WHERE kullanici_id = $1 OR yayin_id = $2)
		     + (SELECT count(*) FROM sosyal.yorum WHERE yazar_id = $1)
		     + (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1)
		     + (SELECT count(*) FROM sosyal.sikayet WHERE bildiren_id = $1)`, giden, canli).Scan(&gidenKalan); err != nil {
		t.Fatalf("sayim: %v", err)
	}
	if gidenKalan != 0 {
		t.Fatalf("silinen kullanicinin %d kaydi kaldi", gidenKalan)
	}

	// Baskalarinin verisi yerinde; cover kalir ve kaynagi silindi gorunur.
	if _, err := d.ProfilGetir(ctx, kalan); err != nil {
		t.Fatalf("kalan profil silinmemeli: %v", err)
	}
	k, err := d.YayinGetir(ctx, kalaninYayini, kalan)
	if err != nil || k.BegeniSayisi != 1 || k.YorumSayisi != 1 {
		t.Fatalf("kalanin yayini = %+v, %v", k, err)
	}
	c, err := d.YayinGetir(ctx, cover, kalan)
	if err != nil || c.Kaynak == nil || !c.Kaynak.Silindi || c.Kaynak.YayinID != canli {
		t.Fatalf("cover kalmali ve kaynagi silindi olmali: %+v, %v", c, err)
	}
	if bos, err := d.YayinlariListele(ctx, giden); err != nil || len(bos) != 0 {
		t.Fatalf("silinen kullanicinin yayini kalmamali: %v %v", bos, err)
	}
}
