package sosyal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func ornekYeniYayin(sahipID string) YeniYayin {
	return YeniYayin{
		ID: uuid.NewString(), Slug: "Ab3dEf9h", SahipID: sahipID,
		Baslik: "Gece", Aciklama: "aciklama", Tur: "pop", Bpm: 120,
		Ton: Ton{Tonik: "A", Mod: "minor"}, SureSn: 184,
		Gorunurluk: GorunurlukHerkes, AltyapiAcik: true, YorumAcik: true,
		Sozler: Sozler{Bolumler: []SozBolumu{{BolumID: "b1", Tur: "verse", Satirlar: []string{"bir", "iki"}}}},
		Yapi:   []YapiOgesi{{ID: "s1", Tur: "verse", Olcu: 8}},
		Hazirlik: Hazirlik{MasterKey: "masters/u/p/m.wav", Stems: []HazirlikStemi{
			{ID: "s1", Enstruman: "drums", Sure: 184, Key: "stems/u/p/s1.mp3"},
		}},
	}
}

func TestPostgresDepo_YayinEkleVeGetir(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	if err := d.YayinEkle(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	k, err := d.YayinGetir(ctx, y.ID, sahip)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if k.Baslik != "Gece" || k.Durum != DurumHazirlaniyor || !k.BenimMi || k.Gorunurluk != GorunurlukHerkes {
		t.Fatalf("kayit = %+v", k)
	}
	if k.Sahip.KullaniciAdi != "sahip" || k.Sahip.AvatarRengi != AvatarRengi(sahip) {
		t.Fatalf("sahip = %+v", k.Sahip)
	}
	if k.Ton != (Ton{Tonik: "A", Mod: "minor"}) || k.SureSn != 184 || k.Bpm != 120 {
		t.Fatalf("muzik alanlari = %+v", k.YayinOzeti)
	}
	if k.DalgaFormu == nil || len(k.DalgaFormu) != 0 || k.Kaynak != nil {
		t.Fatalf("dalga formu bos dizi, kaynak nil olmali: %v %v", k.DalgaFormu, k.Kaynak)
	}
	if len(k.Sozler.Bolumler) != 1 || k.Sozler.Bolumler[0].Satirlar[1] != "iki" || len(k.Yapi) != 1 || k.Yapi[0].Olcu != 8 {
		t.Fatalf("sozler/yapi = %+v %+v", k.Sozler, k.Yapi)
	}
	if !k.StemVar || k.MasterAnahtari != "" || k.Silindi || k.Olusturma.Location().String() != "UTC" {
		t.Fatalf("ic alanlar = stem %v master %q silindi %v", k.StemVar, k.MasterAnahtari, k.Silindi)
	}
	if s, err := d.YayinSlugIleGetir(ctx, "Ab3dEf9h", uuid.NewString()); err != nil || s.ID != y.ID || s.BenimMi {
		t.Fatalf("slug ile = %+v, %v", s, err)
	}
	if _, err := d.YayinGetir(ctx, uuid.NewString(), sahip); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan yayin: %v", err)
	}
}

func TestPostgresDepo_SlugCakismasi(t *testing.T) {
	d := NewPostgresDepo(testHavuz(t))
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	if err := d.YayinEkle(ctx, ornekYeniYayin(sahip)); err != nil {
		t.Fatalf("ilk: %v", err)
	}
	if err := d.YayinEkle(ctx, ornekYeniYayin(sahip)); !errors.Is(err, errSlugCakisti) {
		t.Fatalf("errSlugCakisti bekleniyordu: %v", err)
	}
}

// SQL kosulu ile Go kurali her kombinasyonda ayni karari vermeli.
func TestPostgresDepo_GorunurlukSQLVeGoAyni(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, takipci, yabanci := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testTakipEkle(t, havuz, takipci, sahip)

	var yayinlar []string
	for _, gr := range []string{GorunurlukHerkes, GorunurlukTakipciler, GorunurlukLink} {
		for _, durum := range []string{DurumHazirlaniyor, DurumYayinda, DurumHata} {
			for _, silindi := range []bool{false, true} {
				yayinlar = append(yayinlar, testYayinEkle(t, havuz, testYayin{SahipID: sahip, Gorunurluk: gr, Durum: durum, Silindi: silindi}))
			}
		}
	}
	for _, izleyici := range []string{sahip, takipci, yabanci} {
		for _, id := range yayinlar {
			k, err := d.YayinGetir(ctx, id, izleyici)
			if err != nil {
				t.Fatalf("YayinGetir: %v", err)
			}
			for _, detay := range []bool{true, false} {
				var sqlKarari bool
				if err := havuz.QueryRow(ctx, fmt.Sprintf(
					`SELECT EXISTS (SELECT 1 FROM sosyal.yayin y WHERE y.id = $2 AND %s)`,
					gorunurlukKosulu("y", "$1", detay)), izleyici, id).Scan(&sqlKarari); err != nil {
					t.Fatalf("kosul: %v", err)
				}
				goKarari := gorebilir(k.gorunurlukGirdisi(izleyici), detay)
				if sqlKarari != goKarari {
					t.Errorf("izleyici %s yayin %s/%s silindi=%v detay=%v: SQL %v, Go %v",
						izleyici[:4], k.Gorunurluk, k.Durum, k.Silindi, detay, sqlKarari, goKarari)
				}
			}
		}
	}
}

func TestPostgresDepo_SayaclarVeKaynak(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, coverci, izleyici := uuid.NewString(), uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	testProfilEkle(t, d, coverci, "coverci")
	orijinal := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Baslik: "Orijinal", AltyapiAcik: true})
	cover := testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Gorunurluk: GorunurlukLink})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Durum: DurumHata})
	testYayinEkle(t, havuz, testYayin{SahipID: coverci, KaynakID: orijinal, Silindi: true})
	for _, kisi := range []string{izleyici, coverci} {
		if _, err := havuz.Exec(ctx, `INSERT INTO sosyal.begeni (kullanici_id, yayin_id) VALUES ($1, $2)`, kisi, orijinal); err != nil {
			t.Fatalf("begeni: %v", err)
		}
	}
	if _, err := havuz.Exec(ctx, `
		INSERT INTO sosyal.yorum (id, yayin_id, yazar_id, metin, silinme)
		VALUES (gen_random_uuid(), $1, $2, 'a', NULL), (gen_random_uuid(), $1, $2, 'b', now())`, orijinal, izleyici); err != nil {
		t.Fatalf("yorum: %v", err)
	}

	k, err := d.YayinGetir(ctx, orijinal, izleyici)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	// Cover sayaci: yayinda ve silinmemis (gorunurluk fark etmez) = 2.
	if k.BegeniSayisi != 2 || k.YorumSayisi != 1 || k.CoverSayisi != 2 || !k.BegendimMi {
		t.Fatalf("sayaclar = begeni %d yorum %d cover %d begendim %v", k.BegeniSayisi, k.YorumSayisi, k.CoverSayisi, k.BegendimMi)
	}

	c, err := d.YayinGetir(ctx, cover, izleyici)
	if err != nil || c.Kaynak == nil {
		t.Fatalf("cover = %+v, %v", c, err)
	}
	if *c.Kaynak != (KaynakOzeti{YayinID: orijinal, Baslik: "Orijinal", SahipKullaniciAdi: "sahip", Silindi: false}) {
		t.Fatalf("canli kaynak = %+v", *c.Kaynak)
	}
	if c.KaynakYayinID != orijinal {
		t.Fatalf("KaynakYayinID = %q", c.KaynakYayinID)
	}

	// Yumusak silinen orijinal: son degerler + silindi.
	if err := d.YayinSil(ctx, orijinal, sahip); err != nil {
		t.Fatalf("YayinSil: %v", err)
	}
	c, _ = d.YayinGetir(ctx, cover, izleyici)
	if !c.Kaynak.Silindi || c.Kaynak.Baslik != "Orijinal" {
		t.Fatalf("silinen kaynak = %+v", *c.Kaynak)
	}
	// Kalici silinen orijinal (hesap silme gibi): cover'daki anlik goruntu.
	// YayinKaldir yalnizca hazirlaniyor satiri sildigi icin ham SQL.
	if _, err := havuz.Exec(ctx, `DELETE FROM sosyal.yayin WHERE id = $1`, orijinal); err != nil {
		t.Fatalf("kalici silme: %v", err)
	}
	c, _ = d.YayinGetir(ctx, cover, izleyici)
	if *c.Kaynak != (KaynakOzeti{YayinID: orijinal, Baslik: "Eski baslik", SahipKullaniciAdi: "eski_ad", Silindi: true}) {
		t.Fatalf("kalici silinen kaynak = %+v", *c.Kaynak)
	}
}

func TestPostgresDepo_GuncelleSilTekrarDene(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip, baskasi := uuid.NewString(), uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	id := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHata, AltyapiAcik: true})

	aciklama, kapali := "yeni", false
	if err := d.YayinGuncelle(ctx, id, sahip, YayinYamasi{Aciklama: &aciklama, AltyapiAcik: &kapali}); err != nil {
		t.Fatalf("guncelle: %v", err)
	}
	k, _ := d.YayinGetir(ctx, id, sahip)
	if k.Aciklama != "yeni" || k.AltyapiAcik || !k.YorumAcik || k.Gorunurluk != GorunurlukHerkes {
		t.Fatalf("yalnizca gonderilen alanlar degismeli: %+v", k)
	}
	if err := d.YayinGuncelle(ctx, id, baskasi, YayinYamasi{Aciklama: &aciklama}); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasinin yamasi: %v", err)
	}

	if err := d.TekrarDenemeyeAl(ctx, id, baskasi); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasi tekrar deneyemez: %v", err)
	}
	if err := d.TekrarDenemeyeAl(ctx, id, sahip); err != nil {
		t.Fatalf("hata -> hazirlaniyor: %v", err)
	}
	if err := d.TekrarDenemeyeAl(ctx, id, sahip); !errors.Is(err, ErrDurum) {
		t.Fatalf("hazirlaniyor iken ErrDurum bekleniyordu: %v", err)
	}

	if err := d.YayinSil(ctx, id, baskasi); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("baskasi silemez: %v", err)
	}
	if err := d.YayinSil(ctx, id, sahip); err != nil {
		t.Fatalf("sil: %v", err)
	}
	if err := d.YayinSil(ctx, id, sahip); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("ikinci silme 404 olmali: %v", err)
	}
	if k, err := d.YayinGetir(ctx, id, sahip); err != nil || !k.Silindi {
		t.Fatalf("silinmis yayin isaretli donmeli: %+v, %v", k, err)
	}
	if err := d.YayinGuncelle(ctx, id, sahip, YayinYamasi{Aciklama: &aciklama}); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("silinmis yayin guncellenemez: %v", err)
	}
	if err := d.TekrarDenemeyeAl(ctx, id, sahip); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("silinmis yayin tekrar denenemez: %v", err)
	}
}

func TestPostgresDepo_Hazirlik(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	if err := d.YayinEkle(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	h, err := d.HazirlikGetir(ctx, y.ID)
	if err != nil || h.SahipID != sahip || h.Durum != DurumHazirlaniyor || h.Silindi {
		t.Fatalf("hazirlik = %+v, %v", h, err)
	}
	if h.Hazirlik.MasterKey != "masters/u/p/m.wav" || len(h.Hazirlik.Stems) != 1 || h.Hazirlik.Stems[0].Key != "stems/u/p/s1.mp3" {
		t.Fatalf("hazirlik girdisi = %+v", h.Hazirlik)
	}
	dalga := make([]float64, 64)
	dalga[3] = 1
	sonuc := HazirlikSonucu{
		MasterAnahtari: "sosyal/" + y.ID + "/master.wav",
		StemListesi:    []StemKaydi{{ID: "s1", Enstruman: "drums", Sure: 184, Anahtar: "sosyal/" + y.ID + "/stems/s1.mp3"}},
		DalgaFormu:     dalga,
	}
	if err := d.Yayinla(ctx, y.ID, sonuc); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	k, _ := d.YayinGetir(ctx, y.ID, sahip)
	if k.Durum != DurumYayinda || len(k.DalgaFormu) != 64 || k.DalgaFormu[3] != 1 || k.MasterAnahtari != sonuc.MasterAnahtari {
		t.Fatalf("yayinlanmis = durum %s dalga %d master %q", k.Durum, len(k.DalgaFormu), k.MasterAnahtari)
	}
	var stemler []StemKaydi
	var onizleme *string
	if err := havuz.QueryRow(ctx, `SELECT stem_listesi, onizleme_anahtari FROM sosyal.yayin WHERE id = $1`, y.ID).Scan(&stemler, &onizleme); err != nil {
		t.Fatalf("stem_listesi: %v", err)
	}
	if len(stemler) != 1 || stemler[0].Anahtar != sonuc.StemListesi[0].Anahtar || onizleme != nil {
		t.Fatalf("stemler = %+v onizleme = %v", stemler, onizleme)
	}
	// yayinda iken HataYap bir sey yapmaz.
	if err := d.HataYap(ctx, y.ID); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.YayinGetir(ctx, y.ID, sahip); k.Durum != DurumYayinda {
		t.Fatalf("yayinda yayin hataya dusmemeli: %s", k.Durum)
	}
	if _, err := d.HazirlikGetir(ctx, uuid.NewString()); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("olmayan hazirlik: %v", err)
	}
}

func TestPostgresDepo_HataYapVeSilinmisYayinYayinlanmaz(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	hatali := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor})
	if err := d.HataYap(ctx, hatali); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.YayinGetir(ctx, hatali, sahip); k.Durum != DurumHata {
		t.Fatalf("durum = %s", k.Durum)
	}
	silinen := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor, Silindi: true})
	if err := d.Yayinla(ctx, silinen, HazirlikSonucu{MasterAnahtari: "m"}); err != nil {
		t.Fatalf("Yayinla: %v", err)
	}
	if k, _ := d.YayinGetir(ctx, silinen, sahip); k.Durum != DurumHazirlaniyor {
		t.Fatalf("silinmis yayin yayinlanmamali: %s", k.Durum)
	}
}

// Stems nil olsa bile kayit eklenir ve detay okunur.
func TestPostgresDepo_NilStemlerleEkleVeGetir(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	y := ornekYeniYayin(sahip)
	y.Hazirlik.Stems = nil
	if err := d.YayinEkle(ctx, y); err != nil {
		t.Fatalf("YayinEkle: %v", err)
	}
	k, err := d.YayinGetir(ctx, y.ID, sahip)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if k.StemVar {
		t.Fatal("stem yokken StemVar false olmali")
	}
	var tip string
	if err := havuz.QueryRow(ctx, `SELECT jsonb_typeof(hazirlik->'stems') FROM sosyal.yayin WHERE id = $1`, y.ID).Scan(&tip); err != nil || tip != "array" {
		t.Fatalf("stems dizi olarak saklanmali: %q, %v", tip, err)
	}
}

// Ham {"stems":null} ve null satirlar iceren satir da detayda okunur.
func TestPostgresDepo_NullStemVeSatirlarDetayiBozmaz(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	id := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Hazirlik: `{"masterKey":"masters/u/p/m.wav","stems":null}`})
	if _, err := havuz.Exec(ctx, `UPDATE sosyal.yayin SET sozler = '{"bolumler":[{"bolumId":"b1","tur":"verse","satirlar":null}]}' WHERE id = $1`, id); err != nil {
		t.Fatalf("sozler: %v", err)
	}
	k, err := d.YayinGetir(ctx, id, sahip)
	if err != nil {
		t.Fatalf("YayinGetir: %v", err)
	}
	if k.StemVar {
		t.Fatal("null stems StemVar false olmali")
	}
	if len(k.Sozler.Bolumler) != 1 || k.Sozler.Bolumler[0].Satirlar == nil {
		t.Fatalf("satirlar bos dizi olmali: %+v", k.Sozler)
	}
}

// HataYap silinmis yayina, YayinKaldir hazirlaniyor disindaki yayina dokunmaz.
func TestPostgresDepo_HataYapVeKaldirKorumalari(t *testing.T) {
	havuz := testHavuz(t)
	d := NewPostgresDepo(havuz)
	ctx := context.Background()
	sahip := uuid.NewString()
	testProfilEkle(t, d, sahip, "sahip")
	silinen := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor, Silindi: true})
	if err := d.HataYap(ctx, silinen); err != nil {
		t.Fatalf("HataYap: %v", err)
	}
	if k, _ := d.YayinGetir(ctx, silinen, sahip); k.Durum != DurumHazirlaniyor {
		t.Fatalf("silinmis yayin hataya alinmamali: %s", k.Durum)
	}
	yayinda := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumYayinda})
	if err := d.YayinKaldir(ctx, yayinda); err != nil {
		t.Fatalf("YayinKaldir: %v", err)
	}
	if _, err := d.YayinGetir(ctx, yayinda, sahip); err != nil {
		t.Fatalf("yayinda yayin kaldirilmamali: %v", err)
	}
	hazir := testYayinEkle(t, havuz, testYayin{SahipID: sahip, Durum: DurumHazirlaniyor})
	if err := d.YayinKaldir(ctx, hazir); err != nil {
		t.Fatalf("YayinKaldir: %v", err)
	}
	if _, err := d.YayinGetir(ctx, hazir, sahip); !errors.Is(err, ErrBulunamadi) {
		t.Fatalf("hazirlaniyor yayin kaldirilmali: %v", err)
	}
}
