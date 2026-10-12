package sosyal

import "fmt"

// Gorunurluk ve durum degerleri (sozlesme §3).
const (
	GorunurlukHerkes     = "herkes"
	GorunurlukTakipciler = "takipciler"
	GorunurlukLink       = "link"

	DurumHazirlaniyor = "hazirlaniyor"
	DurumYayinda      = "yayinda"
	DurumHata         = "hata"
)

// GorunurlukGirdisi, bir yayinin bir izleyiciye gorunup gorunmedigine
// karar vermek icin gereken her sey.
type GorunurlukGirdisi struct {
	SahipID     string
	IzleyiciID  string
	Gorunurluk  string
	Durum       string
	Silindi     bool
	TakipEdiyor bool // izleyici sahibi takip ediyor mu
}

// gorebilir, spec §4.9 tablosunun tek dogruluk kaynagidir:
//   - silinmis yayini kimse gormez (sahibi dahil; DELETE tekrari 404).
//   - sahibi kendi yayinini her durumda gorur (hazirlaniyor, hata dahil).
//   - digerleri yalnizca "yayinda" olani gorur: herkes her zaman,
//     takipciler yalnizca takip edenler, link yalnizca detayda.
//
// gorunurlukKosulu ayni kurali SQL'de kurar; ikisinin esitligi canli
// Postgres testinde tum kombinasyonlarla dogrulanir.
func gorebilir(g GorunurlukGirdisi, detay bool) bool {
	if g.Silindi {
		return false
	}
	if g.IzleyiciID != "" && g.SahipID == g.IzleyiciID {
		return true
	}
	if g.Durum != DurumYayinda {
		return false
	}
	switch g.Gorunurluk {
	case GorunurlukHerkes:
		return true
	case GorunurlukTakipciler:
		return g.TakipEdiyor
	case GorunurlukLink:
		return detay
	}
	return false
}

// DetayGorebilir, id ya da slug ile acilan detay icin kuraldir.
func DetayGorebilir(g GorunurlukGirdisi) bool { return gorebilir(g, true) }

// ListedeGorebilir, profil listeleri ve sayaclar icin kuraldir; link
// yayinlari yalnizca sahibine listelenir.
func ListedeGorebilir(g GorunurlukGirdisi) bool { return gorebilir(g, false) }

// gorunurlukKosulu, gorebilir'in SQL karsiligidir. a yayin tablosunun
// takma adi, izleyici izleyici kimliginin parametresidir (orn. "$1").
// Ikisi de sabit dizgedir; kullanici girdisi buraya girmez.
func gorunurlukKosulu(a, izleyici string, detay bool) string {
	link := ""
	if detay {
		link = fmt.Sprintf(" OR %s.gorunurluk = 'link'", a)
	}
	return fmt.Sprintf(`(%[1]s.silinme IS NULL AND (%[1]s.sahip_id = %[2]s OR (%[1]s.durum = 'yayinda' AND (%[1]s.gorunurluk = 'herkes'%[3]s OR (%[1]s.gorunurluk = 'takipciler' AND EXISTS (SELECT 1 FROM sosyal.takip gt WHERE gt.takipci_id = %[2]s AND gt.takip_edilen_id = %[1]s.sahip_id))))))`,
		a, izleyici, link)
}
