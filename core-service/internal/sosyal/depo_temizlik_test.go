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

// izSenaryosu: giden kullanicinin yorumuna kalanin sikayeti, gidenin
// yayini, begenisi, takibi ve kendi sikayeti kurulur.
type izSenaryosu struct {
	giden, kalan, gidenYayini, kalaninYayini string
}

func izSenaryosuKur(t *testing.T, d *PostgresDepo) izSenaryosu {
	t.Helper()
	havuz := d.havuz
	s := izSenaryosu{giden: uuid.NewString(), kalan: uuid.NewString()}
	testProfilEkle(t, d, s.giden, "giden")
	testProfilEkle(t, d, s.kalan, "kalan")
	s.gidenYayini = testYayinEkle(t, havuz, testYayin{SahipID: s.giden})
	s.kalaninYayini = testYayinEkle(t, havuz, testYayin{SahipID: s.kalan})
	testTakipEkle(t, havuz, s.giden, s.kalan)
	testTakipEkle(t, havuz, s.kalan, s.giden)
	gidenYorumu, kalanYorumu := uuid.NewString(), uuid.NewString()
	for _, sorgu := range []string{
		`INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ('` + s.giden + `', '` + s.kalaninYayini + `')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES ('` + gidenYorumu + `', '` + s.kalaninYayini + `', '` + s.giden + `', 'g')`,
		`INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin) VALUES ('` + kalanYorumu + `', '` + s.kalaninYayini + `', '` + s.kalan + `', 'k')`,
		// kalan, gidenin yorumunu sikayet eder: yorum silinince bu da gitmeli.
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), '` + s.kalan + `', 'yorum', '` + gidenYorumu + `', '` + s.kalaninYayini + `', 'spam')`,
		// kalanin kendi yorumuna sikayet (giden bildirdi) bildiren uzerinden gider.
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), '` + s.giden + `', 'yorum', '` + kalanYorumu + `', '` + s.kalaninYayini + `', 'spam')`,
		// ucuncu bir kisinin kalanin yayinina sikayeti kalir.
		`INSERT INTO sosyal.sikayet (id, bildiren_id, hedef_turu, hedef_id, yayin_id, neden) VALUES (gen_random_uuid(), gen_random_uuid(), 'yayin', '` + s.kalaninYayini + `', '` + s.kalaninYayini + `', 'spam')`,
	} {
		if _, err := havuz.Exec(context.Background(), sorgu); err != nil {
			t.Fatalf("kurulum: %v", err)
		}
	}
	return s
}

func sikayetSayisi(t *testing.T, d *PostgresDepo) int {
	t.Helper()
	var n int
	if err := d.havuz.QueryRow(context.Background(), `SELECT count(*) FROM sosyal.sikayet`).Scan(&n); err != nil {
		t.Fatalf("sikayet sayimi: %v", err)
	}
	return n
}

// Iz silme profili ve etkilesimleri siler, yayin satirlarina dokunmaz;
// silinen kullanicinin yorumlarina yapilan sikayetler de gider.
func TestPostgresDepo_KullaniciIzleriniSil(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	ctx := context.Background()
	s := izSenaryosuKur(t, d)

	if err := d.KullaniciIzleriniSil(ctx, s.giden); err != nil {
		t.Fatalf("KullaniciIzleriniSil: %v", err)
	}
	if err := d.KullaniciIzleriniSil(ctx, s.giden); err != nil {
		t.Fatalf("tekrar iz silme hatasiz olmali: %v", err)
	}
	if _, err := d.ProfilGetir(ctx, s.giden); err == nil {
		t.Fatal("profil silinmeli")
	}
	if idler, err := d.YayinlariListele(ctx, s.giden); err != nil || len(idler) != 1 || idler[0] != s.gidenYayini {
		t.Fatalf("yayin satirlari kalmali: %v %v", idler, err)
	}
	// Profil yokken yayin hicbir okuma yolunda gorunmez.
	if _, err := d.YayinGetir(ctx, s.gidenYayini, s.kalan); err == nil {
		t.Fatal("profili silinen kullanicinin yayini gorunmemeli")
	}
	var kalanIz int
	if err := d.havuz.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM sosyal.begeni WHERE kullanici_id = $1)
		     + (SELECT count(*) FROM sosyal.yorum WHERE yazar_id = $1)
		     + (SELECT count(*) FROM sosyal.takip WHERE takipci_id = $1 OR takip_edilen_id = $1)`, s.giden).Scan(&kalanIz); err != nil {
		t.Fatalf("sayim: %v", err)
	}
	if kalanIz != 0 {
		t.Fatalf("gidenin %d izi kaldi", kalanIz)
	}
	if n := sikayetSayisi(t, d); n != 1 {
		t.Fatalf("yalnizca ucuncu kisinin sikayeti kalmali, kalan %d", n)
	}
}

// Tam silme de silinen kullanicinin yorumlarina yapilan sikayetleri siler.
func TestPostgresDepo_KullaniciVerisiniSil_YorumSikayetleri(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	s := izSenaryosuKur(t, d)
	if err := d.KullaniciVerisiniSil(context.Background(), s.giden); err != nil {
		t.Fatalf("KullaniciVerisiniSil: %v", err)
	}
	if n := sikayetSayisi(t, d); n != 1 {
		t.Fatalf("yalnizca ucuncu kisinin sikayeti kalmali, kalan %d", n)
	}
}
