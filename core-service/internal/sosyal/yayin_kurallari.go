package sosyal

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// stemIDDeseni: stem id bir anahtar segmenti olur (stems/{stemId}.{ext});
// segment deseninin uzantisiz halidir.
var stemIDDeseni = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const (
	baslikEnUzun      = 80
	aciklamaEnUzun    = 300
	turEnUzun         = 40
	tonikEnUzun       = 16
	enstrumanEnUzun   = 40
	bpmEnAz, bpmEnCok = 20, 300
	stemEnCok         = 32
	yapiEnCok         = 200
	bolumEnCok        = 200
	satirEnCok        = 200
)

// GorunurlukGecerli, degerin uc gorunurlukten biri oldugunu soyler.
func GorunurlukGecerli(g string) bool {
	return g == GorunurlukHerkes || g == GorunurlukTakipciler || g == GorunurlukLink
}

func runeAraliginda(s string, enAz, enCok int) bool {
	n := utf8.RuneCountInString(s)
	return n >= enAz && n <= enCok
}

// yayinIsteginiDuzenle, istegi kirpar, bos dizileri doldurur ve sozlesme
// kurallarini uygular. Kaynak (cover) ve URL sahipligi servis tarafinda
// denetlenir; burada yalnizca bicim vardir.
func yayinIsteginiDuzenle(ist *YayinIstegi) error {
	ist.Baslik = strings.TrimSpace(ist.Baslik)
	ist.Aciklama = strings.TrimSpace(ist.Aciklama)
	switch {
	case !runeAraliginda(ist.Baslik, 1, baslikEnUzun):
		return dogrulamaHatasi("baslik 1-80 karakter olmali")
	case !runeAraliginda(ist.Aciklama, 0, aciklamaEnUzun):
		return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
	case !GorunurlukGecerli(ist.Gorunurluk):
		return dogrulamaHatasi("gorunurluk herkes, takipciler ya da link olmali")
	case ist.Ton.Mod != "major" && ist.Ton.Mod != "minor":
		return dogrulamaHatasi("ton.mod major ya da minor olmali")
	case !runeAraliginda(ist.Ton.Tonik, 1, tonikEnUzun):
		return dogrulamaHatasi("ton.tonik bos olamaz")
	case !runeAraliginda(ist.Tur, 1, turEnUzun):
		return dogrulamaHatasi("tur 1-40 karakter olmali")
	case ist.SureSn <= 0:
		return dogrulamaHatasi("sureSn sifirdan buyuk olmali")
	case ist.Bpm < bpmEnAz || ist.Bpm > bpmEnCok:
		return dogrulamaHatasi("bpm 20-300 araliginda olmali")
	case ist.MasterURL == "":
		return dogrulamaHatasi("masterUrl zorunlu")
	case len(ist.Yapi) > yapiEnCok:
		return dogrulamaHatasi("yapi cok uzun")
	case len(ist.Sozler.Bolumler) > bolumEnCok:
		return dogrulamaHatasi("sozler cok uzun")
	}
	if ist.Yapi == nil {
		ist.Yapi = []YapiOgesi{}
	}
	for _, o := range ist.Yapi {
		if o.ID == "" || o.Olcu < 0 {
			return dogrulamaHatasi("yapi ogesi gecersiz")
		}
	}
	if ist.Sozler.Bolumler == nil {
		ist.Sozler.Bolumler = []SozBolumu{}
	}
	for i := range ist.Sozler.Bolumler {
		b := &ist.Sozler.Bolumler[i]
		if len(b.Satirlar) > satirEnCok {
			return dogrulamaHatasi("sozler cok uzun")
		}
		if b.Satirlar == nil {
			b.Satirlar = []string{}
		}
	}

	if ist.KaynakYayinID != nil && *ist.KaynakYayinID == "" {
		ist.KaynakYayinID = nil
	}
	if ist.KaynakYayinID != nil {
		// Cover'in altyapisi her zaman kapalidir; stem listesi yok sayilir.
		ist.AltyapiAcik = false
		ist.Stemler = nil
		return nil
	}
	if !ist.AltyapiAcik {
		// Altyapi kapaliysa stem kopyalanmaz; liste yok sayilir.
		ist.Stemler = nil
		return nil
	}
	if len(ist.Stemler) == 0 {
		return dogrulamaHatasi("altyapi acikken en az bir stem gerekli")
	}
	if len(ist.Stemler) > stemEnCok {
		return dogrulamaHatasi("cok fazla stem")
	}
	gorulen := make(map[string]bool, len(ist.Stemler))
	for _, st := range ist.Stemler {
		if !stemIDDeseni.MatchString(st.ID) {
			return dogrulamaHatasi("stem id gecersiz")
		}
		if gorulen[st.ID] {
			return dogrulamaHatasi("stem id tekrar ediyor")
		}
		gorulen[st.ID] = true
		if st.URL == "" || st.Sure <= 0 || !runeAraliginda(st.Enstruman, 1, enstrumanEnUzun) {
			return dogrulamaHatasi("stem alanlari gecersiz")
		}
	}
	return nil
}

// yamayiDuzenle, PATCH govdesini dogrular.
func yamayiDuzenle(y *YayinYamasi) error {
	if y.Aciklama != nil {
		a := strings.TrimSpace(*y.Aciklama)
		if !runeAraliginda(a, 0, aciklamaEnUzun) {
			return dogrulamaHatasi("aciklama en cok 300 karakter olabilir")
		}
		y.Aciklama = &a
	}
	if y.Gorunurluk != nil && !GorunurlukGecerli(*y.Gorunurluk) {
		return dogrulamaHatasi("gorunurluk herkes, takipciler ya da link olmali")
	}
	return nil
}
