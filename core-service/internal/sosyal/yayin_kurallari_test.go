package sosyal

import (
	"errors"
	"strings"
	"testing"
)

func gecerliIstek() YayinIstegi {
	return YayinIstegi{
		Baslik: "Gece", Aciklama: "", Gorunurluk: GorunurlukHerkes, AltyapiAcik: true, YorumAcik: true,
		MasterURL: "masters/u/p/m.wav",
		Stemler:   []StemGirdisi{{ID: "drums", Enstruman: "drums", URL: "stems/u/p/drums.mp3", Sure: 184}},
		Tur:       "pop", Bpm: 120, Ton: Ton{Tonik: "A", Mod: "minor"}, SureSn: 184,
	}
}

func TestYayinIsteginiDuzenle_Gecerli(t *testing.T) {
	ist := gecerliIstek()
	ist.Baslik = "  Gece  "
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("gecerli istek reddedildi: %v", err)
	}
	if ist.Baslik != "Gece" || ist.Yapi == nil || ist.Sozler.Bolumler == nil {
		t.Fatalf("duzenleme = %+v", ist)
	}
}

func TestYayinIsteginiDuzenle_Hatalar(t *testing.T) {
	durumlar := map[string]func(*YayinIstegi){
		"bos baslik":       func(i *YayinIstegi) { i.Baslik = "  " },
		"uzun baslik":      func(i *YayinIstegi) { i.Baslik = strings.Repeat("ş", 81) },
		"uzun aciklama":    func(i *YayinIstegi) { i.Aciklama = strings.Repeat("a", 301) },
		"gorunurluk":       func(i *YayinIstegi) { i.Gorunurluk = "gizli" },
		"ton mod":          func(i *YayinIstegi) { i.Ton.Mod = "dorian" },
		"ton tonik":        func(i *YayinIstegi) { i.Ton.Tonik = "" },
		"tur":              func(i *YayinIstegi) { i.Tur = "" },
		"sure":             func(i *YayinIstegi) { i.SureSn = 0 },
		"bpm dusuk":        func(i *YayinIstegi) { i.Bpm = 19 },
		"bpm yuksek":       func(i *YayinIstegi) { i.Bpm = 301 },
		"master yok":       func(i *YayinIstegi) { i.MasterURL = "" },
		"stem yok":         func(i *YayinIstegi) { i.Stemler = nil },
		"stem id noktali":  func(i *YayinIstegi) { i.Stemler[0].ID = "a.b" },
		"stem id tirmanma": func(i *YayinIstegi) { i.Stemler[0].ID = "../x" },
		"stem sure":        func(i *YayinIstegi) { i.Stemler[0].Sure = 0 },
		"stem url":         func(i *YayinIstegi) { i.Stemler[0].URL = "" },
		"stem enstruman":   func(i *YayinIstegi) { i.Stemler[0].Enstruman = "" },
		"stem tekrar":      func(i *YayinIstegi) { i.Stemler = append(i.Stemler, i.Stemler[0]) },
		"yapi ogesi":       func(i *YayinIstegi) { i.Yapi = []YapiOgesi{{ID: "", Olcu: 4}} },
		"cok stem":         func(i *YayinIstegi) { i.Stemler = make([]StemGirdisi, 33) },
		"cok satir":        func(i *YayinIstegi) { i.Sozler.Bolumler = []SozBolumu{{BolumID: "b", Satirlar: make([]string, 201)}} },
	}
	for ad, boz := range durumlar {
		ist := gecerliIstek()
		boz(&ist)
		if err := yayinIsteginiDuzenle(&ist); !errors.Is(err, ErrDogrulama) {
			t.Errorf("%s: ErrDogrulama bekleniyordu, gelen %v", ad, err)
		}
	}
}

func TestYayinIsteginiDuzenle_SinirDegerler(t *testing.T) {
	ist := gecerliIstek()
	ist.Baslik, ist.Aciklama, ist.Bpm = strings.Repeat("ş", 80), strings.Repeat("ğ", 300), 300
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("sinir degerler kabul edilmeli: %v", err)
	}
	ist = gecerliIstek()
	ist.Bpm = 20
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("bpm 20 kabul edilmeli: %v", err)
	}
}

func TestYayinIsteginiDuzenle_CoverAltyapiKapanirStemlerAtilir(t *testing.T) {
	ist := gecerliIstek()
	kaynak := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	ist.KaynakYayinID = &kaynak
	ist.Stemler[0].ID = "gecersiz.id" // yok sayilir, dogrulanmaz
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("cover: %v", err)
	}
	if ist.AltyapiAcik || ist.Stemler != nil {
		t.Fatalf("cover altyapisi kapali ve stemsiz olmali: %+v", ist)
	}
}

func TestYayinIsteginiDuzenle_BosKaynakOrijinaldir(t *testing.T) {
	ist := gecerliIstek()
	bos := ""
	ist.KaynakYayinID = &bos
	if err := yayinIsteginiDuzenle(&ist); err != nil || ist.KaynakYayinID != nil || !ist.AltyapiAcik {
		t.Fatalf("bos kaynak orijinal sayilmali: %v %+v", err, ist)
	}
}

func TestYayinIsteginiDuzenle_KapaliAltyapiStemleriYokSayar(t *testing.T) {
	ist := gecerliIstek()
	ist.AltyapiAcik = false
	ist.Stemler = []StemGirdisi{{ID: "x.y"}}
	if err := yayinIsteginiDuzenle(&ist); err != nil || ist.Stemler != nil {
		t.Fatalf("kapali altyapi: %v %+v", err, ist.Stemler)
	}
}

func TestYamayiDuzenle(t *testing.T) {
	a, g := "  merhaba ", "link"
	y := YayinYamasi{Aciklama: &a, Gorunurluk: &g}
	if err := yamayiDuzenle(&y); err != nil || *y.Aciklama != "merhaba" {
		t.Fatalf("yama = %v %v", err, *y.Aciklama)
	}
	kotu := "gizli"
	if err := yamayiDuzenle(&YayinYamasi{Gorunurluk: &kotu}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("gecersiz gorunurluk: %v", err)
	}
	uzun := strings.Repeat("a", 301)
	if err := yamayiDuzenle(&YayinYamasi{Aciklama: &uzun}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("uzun aciklama: %v", err)
	}
}

func TestYayinIsteginiDuzenle_IcIceAlanSinirlari(t *testing.T) {
	bolum := func(b SozBolumu) func(*YayinIstegi) {
		return func(i *YayinIstegi) { i.Sozler.Bolumler = []SozBolumu{b} }
	}
	durumlar := map[string]func(*YayinIstegi){
		"uzun satir":     bolum(SozBolumu{BolumID: "b", Tur: "verse", Satirlar: []string{strings.Repeat("ş", 201)}}),
		"uzun bolum id":  bolum(SozBolumu{BolumID: strings.Repeat("b", 65), Tur: "verse"}),
		"uzun bolum tur": bolum(SozBolumu{BolumID: "b", Tur: strings.Repeat("v", 65)}),
		"uzun yapi id":   func(i *YayinIstegi) { i.Yapi = []YapiOgesi{{ID: strings.Repeat("s", 65), Tur: "verse"}} },
		"uzun yapi tur":  func(i *YayinIstegi) { i.Yapi = []YapiOgesi{{ID: "s", Tur: strings.Repeat("v", 65)}} },
		"toplam satir": func(i *YayinIstegi) {
			i.Sozler.Bolumler = make([]SozBolumu, 6)
			for k := range i.Sozler.Bolumler {
				i.Sozler.Bolumler[k] = SozBolumu{BolumID: "b", Satirlar: make([]string, 167)} // 1002 satir
			}
		},
	}
	for ad, boz := range durumlar {
		ist := gecerliIstek()
		boz(&ist)
		if err := yayinIsteginiDuzenle(&ist); !errors.Is(err, ErrDogrulama) {
			t.Errorf("%s: ErrDogrulama bekleniyordu, gelen %v", ad, err)
		}
	}

	ist := gecerliIstek()
	ist.Yapi = []YapiOgesi{{ID: strings.Repeat("ş", 64), Tur: strings.Repeat("ğ", 64)}}
	ist.Sozler.Bolumler = make([]SozBolumu, 5)
	for k := range ist.Sozler.Bolumler {
		ist.Sozler.Bolumler[k] = SozBolumu{BolumID: strings.Repeat("ş", 64), Tur: strings.Repeat("ğ", 64), Satirlar: make([]string, 200)}
	}
	ist.Sozler.Bolumler[0].Satirlar[0] = strings.Repeat("ş", 200)
	if err := yayinIsteginiDuzenle(&ist); err != nil {
		t.Fatalf("sinir degerler (1000 satir, 200/64 karakter) kabul edilmeli: %v", err)
	}
}

func TestYayinIsteginiDuzenle_NULKarakteriReddedilir(t *testing.T) {
	durumlar := map[string]func(*YayinIstegi){
		"baslik":         func(i *YayinIstegi) { i.Baslik = "Ge\x00ce" },
		"aciklama":       func(i *YayinIstegi) { i.Aciklama = "a\x00" },
		"tur":            func(i *YayinIstegi) { i.Tur = "p\x00p" },
		"tonik":          func(i *YayinIstegi) { i.Ton.Tonik = "A\x00" },
		"master url":     func(i *YayinIstegi) { i.MasterURL = "masters/u/p/m\x00.wav" },
		"stem url":       func(i *YayinIstegi) { i.Stemler[0].URL = "stems/u/p/d\x00.mp3" },
		"stem enstruman": func(i *YayinIstegi) { i.Stemler[0].Enstruman = "dr\x00ms" },
		"satir": func(i *YayinIstegi) {
			i.Sozler.Bolumler = []SozBolumu{{BolumID: "b", Tur: "verse", Satirlar: []string{"bir\x00"}}}
		},
		"bolum id":  func(i *YayinIstegi) { i.Sozler.Bolumler = []SozBolumu{{BolumID: "b\x00", Tur: "verse"}} },
		"bolum tur": func(i *YayinIstegi) { i.Sozler.Bolumler = []SozBolumu{{BolumID: "b", Tur: "v\x00"}} },
		"yapi id":   func(i *YayinIstegi) { i.Yapi = []YapiOgesi{{ID: "s\x00", Tur: "verse"}} },
		"yapi tur":  func(i *YayinIstegi) { i.Yapi = []YapiOgesi{{ID: "s", Tur: "v\x00"}} },
	}
	for ad, boz := range durumlar {
		ist := gecerliIstek()
		boz(&ist)
		if err := yayinIsteginiDuzenle(&ist); !errors.Is(err, ErrDogrulama) {
			t.Errorf("%s: ErrDogrulama bekleniyordu, gelen %v", ad, err)
		}
	}
}

func TestYamayiDuzenle_NULKarakteriReddedilir(t *testing.T) {
	a := "mer\x00haba"
	if err := yamayiDuzenle(&YayinYamasi{Aciklama: &a}); !errors.Is(err, ErrDogrulama) {
		t.Fatalf("NUL iceren aciklama: %v", err)
	}
}
