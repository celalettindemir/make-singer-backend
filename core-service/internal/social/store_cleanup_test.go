package social

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresDepo_KullaniciVerisiniSil(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
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

	idler, err := d.ListPublicationIDs(ctx, giden)
	if err != nil {
		t.Fatalf("YayinlariListele: %v", err)
	}
	sort.Strings(idler)
	beklenen := []string{canli, silinmis}
	sort.Strings(beklenen)
	if len(idler) != 2 || idler[0] != beklenen[0] || idler[1] != beklenen[1] {
		t.Fatalf("silinmisler dahil iki yayin bekleniyordu: %v", idler)
	}

	if err := d.DeleteUserData(ctx, giden); err != nil {
		t.Fatalf("KullaniciVerisiniSil: %v", err)
	}
	if err := d.DeleteUserData(ctx, giden); err != nil {
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
	if _, err := d.GetProfile(ctx, kalan); err != nil {
		t.Fatalf("kalan profil silinmemeli: %v", err)
	}
	k, err := d.GetPublication(ctx, kalaninYayini, kalan)
	if err != nil || k.BegeniSayisi != 1 || k.YorumSayisi != 1 {
		t.Fatalf("kalanin yayini = %+v, %v", k, err)
	}
	c, err := d.GetPublication(ctx, cover, kalan)
	if err != nil || c.Kaynak == nil || !c.Kaynak.Silindi || c.Kaynak.YayinID != canli {
		t.Fatalf("cover kalmali ve kaynagi silindi olmali: %+v, %v", c, err)
	}
	if bos, err := d.ListPublicationIDs(ctx, giden); err != nil || len(bos) != 0 {
		t.Fatalf("silinen kullanicinin yayini kalmamali: %v %v", bos, err)
	}
}

// izSenaryosu: giden kullanicinin yorumuna kalanin sikayeti, gidenin
// yayini, begenisi, takibi ve kendi sikayeti kurulur.
type izSenaryosu struct {
	giden, kalan, gidenYayini, kalaninYayini string
}

func izSenaryosuKur(t *testing.T, d *PostgresStore) izSenaryosu {
	t.Helper()
	havuz := d.pool
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

func sikayetSayisi(t *testing.T, d *PostgresStore) int {
	t.Helper()
	var n int
	if err := d.pool.QueryRow(context.Background(), `SELECT count(*) FROM sosyal.sikayet`).Scan(&n); err != nil {
		t.Fatalf("sikayet sayimi: %v", err)
	}
	return n
}

// Iz silme profili ve etkilesimleri siler, yayin satirlarina dokunmaz;
// silinen kullanicinin yorumlarina yapilan sikayetler de gider.
func TestPostgresDepo_KullaniciIzleriniSil(t *testing.T) {
	d := NewPostgresStore(testHavuz(t))
	ctx := context.Background()
	s := izSenaryosuKur(t, d)

	if err := d.DeleteUserTraces(ctx, s.giden); err != nil {
		t.Fatalf("KullaniciIzleriniSil: %v", err)
	}
	if err := d.DeleteUserTraces(ctx, s.giden); err != nil {
		t.Fatalf("tekrar iz silme hatasiz olmali: %v", err)
	}
	if _, err := d.GetProfile(ctx, s.giden); err == nil {
		t.Fatal("profil silinmeli")
	}
	if idler, err := d.ListPublicationIDs(ctx, s.giden); err != nil || len(idler) != 1 || idler[0] != s.gidenYayini {
		t.Fatalf("yayin satirlari kalmali: %v %v", idler, err)
	}
	// Profil yokken yayin hicbir okuma yolunda gorunmez.
	if _, err := d.GetPublication(ctx, s.gidenYayini, s.kalan); err == nil {
		t.Fatal("profili silinen kullanicinin yayini gorunmemeli")
	}
	var kalanIz int
	if err := d.pool.QueryRow(ctx, `
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
	d := NewPostgresStore(testHavuz(t))
	s := izSenaryosuKur(t, d)
	if err := d.DeleteUserData(context.Background(), s.giden); err != nil {
		t.Fatalf("KullaniciVerisiniSil: %v", err)
	}
	if n := sikayetSayisi(t, d); n != 1 {
		t.Fatalf("yalnizca ucuncu kisinin sikayeti kalmali, kalan %d", n)
	}
}

// Silinen kullanicinin yorumlarina (baskasinin yayininda, soft-delete
// edilmis olanlar dahil) yapilan sikayetler gider; yayin cascade'i ile
// gidenler gider; baskalarinin yorum ve yayinlarina yapilanlar kalir.
func TestPostgresDepo_KullaniciVerisiniSil_SilinmisYorumVeKalanSikayetler(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresStore(havuz)
	ctx := context.Background()
	giden, kalan, ucuncu := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, giden, "giden")
	testProfilEkle(t, d, kalan, "kalan")
	kalaninYayini := testYayinEkle(t, havuz, testYayin{SahipID: kalan})
	gidenYayini := testYayinEkle(t, havuz, testYayin{SahipID: giden})

	gidenYorumu := testYorumEkle(t, havuz, "", kalaninYayini, giden, nil, time.Now())
	silinmisYorum := testYorumEkle(t, havuz, "", kalaninYayini, giden, intp(5), time.Now())
	if err := d.DeleteComment(ctx, silinmisYorum); err != nil {
		t.Fatal(err)
	}
	kalanYorumu := testYorumEkle(t, havuz, "", kalaninYayini, kalan, nil, time.Now())
	yorumAltinda := testYorumEkle(t, havuz, "", gidenYayini, kalan, nil, time.Now())

	sikayet := func(bildiren, tur, hedef, yayin string) {
		t.Helper()
		if err := d.InsertReport(ctx, YeniSikayet{ID: uuid.NewString(), BildirenID: bildiren, HedefTuru: tur,
			HedefID: hedef, YayinID: yayin, Neden: "spam"}); err != nil {
			t.Fatal(err)
		}
	}
	sikayet(kalan, HedefYorum, gidenYorumu, kalaninYayini)    // gider
	sikayet(ucuncu, HedefYorum, silinmisYorum, kalaninYayini) // gider
	sikayet(kalan, HedefYayin, gidenYayini, gidenYayini)      // cascade ile gider
	sikayet(ucuncu, HedefYorum, yorumAltinda, gidenYayini)    // cascade ile gider
	sikayet(ucuncu, HedefYorum, kalanYorumu, kalaninYayini)   // kalir
	sikayet(ucuncu, HedefYayin, kalaninYayini, kalaninYayini) // kalir

	if err := d.DeleteUserData(ctx, giden); err != nil {
		t.Fatalf("KullaniciVerisiniSil: %v", err)
	}
	satirlar, err := havuz.Query(ctx, `SELECT hedef_id::text FROM sosyal.sikayet ORDER BY hedef_turu`)
	if err != nil {
		t.Fatal(err)
	}
	defer satirlar.Close()
	var hedefler []string
	for satirlar.Next() {
		var h string
		if err := satirlar.Scan(&h); err != nil {
			t.Fatal(err)
		}
		hedefler = append(hedefler, h)
	}
	esitSira(t, "kalan sikayetler", hedefler, []string{kalaninYayini, kalanYorumu})
}
