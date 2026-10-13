package social

import "testing"

// Spec §4.9 tablosu, sahip olmayan izleyici icin.
func TestGorunurlukTablosu(t *testing.T) {
	durumlar := []struct {
		gorunurluk  string
		takipEdiyor bool
		liste       bool
		detay       bool
	}{
		{GorunurlukHerkes, false, true, true},
		{GorunurlukHerkes, true, true, true},
		{GorunurlukTakipciler, false, false, false},
		{GorunurlukTakipciler, true, true, true},
		{GorunurlukLink, false, false, true},
		{GorunurlukLink, true, false, true},
	}
	for _, d := range durumlar {
		g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: "i", Gorunurluk: d.gorunurluk, Durum: DurumYayinda, TakipEdiyor: d.takipEdiyor}
		if got := ListedeGorebilir(g); got != d.liste {
			t.Errorf("%s takip=%v liste = %v, beklenen %v", d.gorunurluk, d.takipEdiyor, got, d.liste)
		}
		if got := DetayGorebilir(g); got != d.detay {
			t.Errorf("%s takip=%v detay = %v, beklenen %v", d.gorunurluk, d.takipEdiyor, got, d.detay)
		}
	}
}

func TestGorunurluk_SahipHerDurumuGorur(t *testing.T) {
	for _, gr := range []string{GorunurlukHerkes, GorunurlukTakipciler, GorunurlukLink} {
		for _, durum := range []string{DurumHazirlaniyor, DurumYayinda, DurumHata} {
			g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: "s", Gorunurluk: gr, Durum: durum}
			if !DetayGorebilir(g) || !ListedeGorebilir(g) {
				t.Errorf("sahip %s/%s yayinini gormeli", gr, durum)
			}
		}
	}
}

func TestGorunurluk_YabanciHazirlanmayiVeHatayiGormez(t *testing.T) {
	for _, durum := range []string{DurumHazirlaniyor, DurumHata} {
		g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: "i", Gorunurluk: GorunurlukHerkes, Durum: durum, TakipEdiyor: true}
		if DetayGorebilir(g) || ListedeGorebilir(g) {
			t.Errorf("yabanci %s yayini gormemeli", durum)
		}
	}
}

func TestGorunurluk_SilinmisiKimseGormez(t *testing.T) {
	for _, izleyici := range []string{"s", "i"} {
		g := GorunurlukGirdisi{SahipID: "s", IzleyiciID: izleyici, Gorunurluk: GorunurlukHerkes, Durum: DurumYayinda, Silindi: true}
		if DetayGorebilir(g) || ListedeGorebilir(g) {
			t.Errorf("%s silinmis yayini gormemeli", izleyici)
		}
	}
}

// Bos izleyici kimligi, sahip kimligi de bos olsa sahip sayilmaz.
func TestGorunurluk_BosIzleyiciSahipSayilmaz(t *testing.T) {
	g := GorunurlukGirdisi{SahipID: "", IzleyiciID: "", Gorunurluk: GorunurlukHerkes, Durum: DurumHazirlaniyor}
	if DetayGorebilir(g) || ListedeGorebilir(g) {
		t.Fatal("bos izleyici hazirlanan yayini gormemeli")
	}
}
