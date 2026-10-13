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
		{VisibilityEveryone, false, true, true},
		{VisibilityEveryone, true, true, true},
		{VisibilityFollowers, false, false, false},
		{VisibilityFollowers, true, true, true},
		{VisibilityLink, false, false, true},
		{VisibilityLink, true, false, true},
	}
	for _, d := range durumlar {
		g := VisibilityInput{OwnerID: "s", ViewerID: "i", Visibility: d.gorunurluk, Status: StatusPublished, ViewerFollows: d.takipEdiyor}
		if got := CanSeeInList(g); got != d.liste {
			t.Errorf("%s takip=%v liste = %v, beklenen %v", d.gorunurluk, d.takipEdiyor, got, d.liste)
		}
		if got := CanSeeDetail(g); got != d.detay {
			t.Errorf("%s takip=%v detay = %v, beklenen %v", d.gorunurluk, d.takipEdiyor, got, d.detay)
		}
	}
}

func TestGorunurluk_SahipHerDurumuGorur(t *testing.T) {
	for _, gr := range []string{VisibilityEveryone, VisibilityFollowers, VisibilityLink} {
		for _, durum := range []string{StatusPreparing, StatusPublished, StatusFailed} {
			g := VisibilityInput{OwnerID: "s", ViewerID: "s", Visibility: gr, Status: durum}
			if !CanSeeDetail(g) || !CanSeeInList(g) {
				t.Errorf("sahip %s/%s yayinini gormeli", gr, durum)
			}
		}
	}
}

func TestGorunurluk_YabanciHazirlanmayiVeHatayiGormez(t *testing.T) {
	for _, durum := range []string{StatusPreparing, StatusFailed} {
		g := VisibilityInput{OwnerID: "s", ViewerID: "i", Visibility: VisibilityEveryone, Status: durum, ViewerFollows: true}
		if CanSeeDetail(g) || CanSeeInList(g) {
			t.Errorf("yabanci %s yayini gormemeli", durum)
		}
	}
}

func TestGorunurluk_SilinmisiKimseGormez(t *testing.T) {
	for _, izleyici := range []string{"s", "i"} {
		g := VisibilityInput{OwnerID: "s", ViewerID: izleyici, Visibility: VisibilityEveryone, Status: StatusPublished, Deleted: true}
		if CanSeeDetail(g) || CanSeeInList(g) {
			t.Errorf("%s silinmis yayini gormemeli", izleyici)
		}
	}
}

// Bos izleyici kimligi, sahip kimligi de bos olsa sahip sayilmaz.
func TestGorunurluk_BosIzleyiciSahipSayilmaz(t *testing.T) {
	g := VisibilityInput{OwnerID: "", ViewerID: "", Visibility: VisibilityEveryone, Status: StatusPreparing}
	if CanSeeDetail(g) || CanSeeInList(g) {
		t.Fatal("bos izleyici hazirlanan yayini gormemeli")
	}
}
