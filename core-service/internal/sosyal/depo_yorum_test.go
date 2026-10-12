package sosyal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testYorumEkle, verilen zamanda bir yorum yazar ve kimligini doner.
func testYorumEkle(t *testing.T, havuz *pgxpool.Pool, id, yayin, yazar string, zamanMs *int, olusturma time.Time) string {
	t.Helper()
	if id == "" {
		id = uuid.NewString()
	}
	if _, err := havuz.Exec(context.Background(), `
		INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, zaman_ms, olusturma)
		VALUES ($1, $2, $3, 'metin', $4, $5)`, id, yayin, yazar, zamanMs, olusturma); err != nil {
		t.Fatalf("yorum eklenemedi: %v", err)
	}
	return id
}

// tumYorumSayfalari, yorum listesini limit'lik sayfalarla gezer; her
// sayfanin imlecini servisin kurdugu gibi (son ogeden) kurar.
func tumYorumSayfalari(t *testing.T, d *PostgresDepo, yayin, izleyici string, limit int) []Yorum {
	t.Helper()
	var (
		hepsi []Yorum
		imlec *YorumImleci
	)
	for sayfa := 0; sayfa < 20; sayfa++ {
		ogeler, err := d.Yorumlar(context.Background(), yayin, izleyici, imlec, limit)
		if err != nil {
			t.Fatalf("sayfa %d: %v", sayfa, err)
		}
		s := yorumSayfasiKur(ogeler, limit)
		hepsi = append(hepsi, s.Ogeler...)
		if s.SonrakiImlec == nil {
			return hepsi
		}
		if imlec, err = yorumImleciCoz(*s.SonrakiImlec); err != nil {
			t.Fatalf("imlec cozulemedi: %v", err)
		}
	}
	t.Fatal("sayfalama bitmedi")
	return nil
}

func TestPostgresDepo_YorumSirasiVeSayfalama(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	sahip, yazar, izleyici := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testProfilEkle(t, d, yazar, "yazar")
	yayin := testYayinEkle(t, havuz, testYayin{SahipID: sahip})
	baska := testYayinEkle(t, havuz, testYayin{SahipID: sahip})
	t0 := time.Now().Add(-time.Hour).Truncate(time.Microsecond)

	// Beklenen sira: zamanlilar zaman_ms artan (esitlikte olusturma, id
	// artan), sonra damgasizlar olusturma azalan (esitlikte id azalan).
	z100a := testYorumEkle(t, havuz, "00000000-0000-4000-8000-0000000000a1", yayin, yazar, intp(100), t0)
	z100b := testYorumEkle(t, havuz, "00000000-0000-4000-8000-0000000000a2", yayin, yazar, intp(100), t0)
	z100c := testYorumEkle(t, havuz, "", yayin, sahip, intp(100), t0.Add(time.Second))
	z0 := testYorumEkle(t, havuz, "", yayin, yazar, intp(0), t0.Add(5*time.Minute))
	z900 := testYorumEkle(t, havuz, "", yayin, yazar, intp(900), t0.Add(-time.Minute))
	yeni := testYorumEkle(t, havuz, "", yayin, yazar, nil, t0.Add(10*time.Minute))
	ayniB := testYorumEkle(t, havuz, "ffffffff-0000-4000-8000-0000000000b1", yayin, yazar, nil, t0.Add(5*time.Minute))
	ayniA := testYorumEkle(t, havuz, "00000000-0000-4000-8000-0000000000b1", yayin, sahip, nil, t0.Add(5*time.Minute))
	eski := testYorumEkle(t, havuz, "", yayin, yazar, nil, t0)
	// Listeye girmeyenler: silinmis yorum, baska yayinin yorumu.
	silinen := testYorumEkle(t, havuz, "", yayin, yazar, intp(50), t0)
	if err := d.YorumSil(context.Background(), silinen); err != nil {
		t.Fatal(err)
	}
	testYorumEkle(t, havuz, "", baska, yazar, nil, t0)

	beklenen := []string{z0, z100a, z100b, z100c, z900, yeni, ayniB, ayniA, eski}
	for _, limit := range []int{1, 2, 3, 4, 50} {
		var idler []string
		for _, y := range tumYorumSayfalari(t, d, yayin, izleyici, limit) {
			idler = append(idler, y.ID)
		}
		esitSira(t, "yorum sirasi", idler, beklenen)
	}

	// silebilirMiyim izleyiciye goredir: yazar kendi yorumunu, sahip hepsini.
	for izleyiciID, beklenenSilebilir := range map[string]int{izleyici: 0, yazar: 7, sahip: 9} {
		n := 0
		for _, y := range tumYorumSayfalari(t, d, yayin, izleyiciID, 50) {
			if y.SilebilirMiyim {
				n++
			}
		}
		if n != beklenenSilebilir {
			t.Errorf("izleyici %s: silinebilir %d, beklenen %d", izleyiciID, n, beklenenSilebilir)
		}
	}

	// yorumSayisi silinmisi saymaz.
	k, err := d.YayinGetir(context.Background(), yayin, izleyici)
	if err != nil || k.YorumSayisi != 9 {
		t.Fatalf("yorumSayisi = %d, %v; beklenen 9", k.YorumSayisi, err)
	}
}

func TestPostgresDepo_YorumEkleGetirSil(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, yazar := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testProfilEkle(t, d, yazar, "yazar")
	yayin := testYayinEkle(t, havuz, testYayin{SahipID: sahip})

	id := uuid.NewString()
	y, err := d.YorumEkle(ctx, YeniYorum{ID: id, YayinID: yayin, YazarID: yazar, Metin: "selam", ZamanMs: intp(1200)})
	if err != nil {
		t.Fatalf("YorumEkle: %v", err)
	}
	if y.ID != id || y.Metin != "selam" || *y.ZamanMs != 1200 || y.Yazar.KullaniciAdi != "yazar" ||
		y.Yazar.AvatarRengi != AvatarRengi(yazar) || y.Olusturma.IsZero() || y.Olusturma.Location() != time.UTC {
		t.Fatalf("yorum = %+v", y)
	}
	damgasiz, err := d.YorumEkle(ctx, YeniYorum{ID: uuid.NewString(), YayinID: yayin, YazarID: yazar, Metin: "a"})
	if err != nil || damgasiz.ZamanMs != nil {
		t.Fatalf("damgasiz = %+v, %v", damgasiz, err)
	}

	k, err := d.YorumGetir(ctx, id)
	if err != nil || k.YayinID != yayin || k.YazarID != yazar || k.YayinSahibiID != sahip || k.Silindi {
		t.Fatalf("kayit = %+v, %v", k, err)
	}
	if err := d.YorumSil(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := d.YorumSil(ctx, id); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("tekrar silme: %v", err)
	}
	if k, err := d.YorumGetir(ctx, id); err != nil || !k.Silindi {
		t.Fatalf("silinmis kayit = %+v, %v", k, err)
	}
	if _, err := d.YorumGetir(ctx, uuid.NewString()); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan yorum: %v", err)
	}
	if _, err := d.YorumEkle(ctx, YeniYorum{ID: uuid.NewString(), YayinID: yayin, YazarID: yazar, Metin: "x", ZamanMs: intp(-1)}); err == nil {
		t.Fatal("negatif zaman_ms CHECK kisitina takilmali")
	}
}
