package sosyal

import "time"

// Ton, sozlesme §4.1: { tonik, mod }.
type Ton struct {
	Tonik string `json:"tonik"`
	Mod   string `json:"mod"`
}

// YapiOgesi, sarki yapisinin bir bolumu.
type YapiOgesi struct {
	ID   string  `json:"id"`
	Tur  string  `json:"tur"`
	Olcu float64 `json:"olcu"`
}

// SozBolumu ve Sozler, sozlesme §4.1 Sozler.
type SozBolumu struct {
	BolumID  string   `json:"bolumId"`
	Tur      string   `json:"tur"`
	Satirlar []string `json:"satirlar"`
}

type Sozler struct {
	Bolumler []SozBolumu `json:"bolumler"`
}

// KaynakOzeti, cover'in orijinali. Orijinal silinmisse Silindi true ve
// diger alanlar son bilinen degerlerdir.
type KaynakOzeti struct {
	YayinID           string `json:"yayinId"`
	Baslik            string `json:"baslik"`
	SahipKullaniciAdi string `json:"sahipKullaniciAdi"`
	Silindi           bool   `json:"silindi"`
}

// YayinOzeti, sozlesme §4.1. Kaynak cover olmayan yayinda null olarak
// GORUNUR (omitempty yok); DalgaFormu hicbir zaman null degildir.
type YayinOzeti struct {
	ID           string       `json:"id"`
	Slug         string       `json:"slug"`
	Baslik       string       `json:"baslik"`
	Sahip        ProfilOzeti  `json:"sahip"`
	Tur          string       `json:"tur"`
	Bpm          int          `json:"bpm"`
	Ton          Ton          `json:"ton"`
	SureSn       int          `json:"sureSn"`
	DalgaFormu   []float64    `json:"dalgaFormu"`
	BegeniSayisi int          `json:"begeniSayisi"`
	YorumSayisi  int          `json:"yorumSayisi"`
	CoverSayisi  int          `json:"coverSayisi"`
	BegendimMi   bool         `json:"begendimMi"`
	AltyapiAcik  bool         `json:"altyapiAcik"`
	Kaynak       *KaynakOzeti `json:"kaynak"`
	Olusturma    time.Time    `json:"olusturma"`
}

// YayinDetayi, sozlesme §4.1. Gorunurluk her zaman gonderilir.
type YayinDetayi struct {
	YayinOzeti
	Aciklama   string      `json:"aciklama"`
	YorumAcik  bool        `json:"yorumAcik"`
	Sozler     Sozler      `json:"sozler"`
	Yapi       []YapiOgesi `json:"yapi"`
	Durum      string      `json:"durum"`
	BenimMi    bool        `json:"benimMi"`
	Gorunurluk string      `json:"gorunurluk"`
}

// StemGirdisi, yayin isteginde bir stem. URL render sonucunun fileUrl'i
// (tam adres ya da ciplak anahtar).
type StemGirdisi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	URL       string  `json:"url"`
	Sure      float64 `json:"sure"`
}

// YayinIstegi, POST /yayinlar govdesi (mobil PublishInput).
type YayinIstegi struct {
	Baslik        string        `json:"baslik"`
	Aciklama      string        `json:"aciklama"`
	Gorunurluk    string        `json:"gorunurluk"`
	AltyapiAcik   bool          `json:"altyapiAcik"`
	YorumAcik     bool          `json:"yorumAcik"`
	MasterURL     string        `json:"masterUrl"`
	Stemler       []StemGirdisi `json:"stemler"`
	Sozler        Sozler        `json:"sozler"`
	Yapi          []YapiOgesi   `json:"yapi"`
	Tur           string        `json:"tur"`
	Bpm           int           `json:"bpm"`
	Ton           Ton           `json:"ton"`
	SureSn        int           `json:"sureSn"`
	KaynakYayinID *string       `json:"kaynakYayinId"`
}

// YayinYaniti, POST /yayinlar 202 govdesi.
type YayinYaniti struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Durum string `json:"durum"`
}

// YayinYamasi, PATCH /yayinlar/{id}; yalnizca gonderilen alanlar degisir.
type YayinYamasi struct {
	Aciklama    *string `json:"aciklama"`
	AltyapiAcik *bool   `json:"altyapiAcik"`
	Gorunurluk  *string `json:"gorunurluk"`
	YorumAcik   *bool   `json:"yorumAcik"`
}

// DosyaAdresi ve YayinDosyalari, POST /yayinlar/{id}/dosyalar yaniti.
type DosyaAdresi struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type YayinDosyalari struct {
	Master DosyaAdresi `json:"master"`
}

// HazirlikStemi ve Hazirlik, yayin.hazirlik kolonu: kopyalanacak kaynak
// anahtarlar. tekrar-dene ayni girdiyle yeniden kosabilsin diye satirda
// durur; disari verilmez.
type HazirlikStemi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	Sure      float64 `json:"sure"`
	Key       string  `json:"key"`
}

type Hazirlik struct {
	MasterKey string          `json:"masterKey"`
	Stems     []HazirlikStemi `json:"stems"`
}

// StemKaydi, yayin.stem_listesi ogesi: kopyalanmis stem. Anahtar,
// sosyal/{yayinId}/stems/{stemId}.{ext} kopyasidir.
type StemKaydi struct {
	ID        string  `json:"id"`
	Enstruman string  `json:"enstruman"`
	Sure      float64 `json:"sure"`
	Anahtar   string  `json:"anahtar"`
}

// YeniYayin, depoya yazilacak dogrulanmis yayin.
type YeniYayin struct {
	ID             string
	Slug           string
	SahipID        string
	Baslik         string
	Aciklama       string
	Tur            string
	Bpm            int
	Ton            Ton
	SureSn         int
	Gorunurluk     string
	AltyapiAcik    bool
	YorumAcik      bool
	Sozler         Sozler
	Yapi           []YapiOgesi
	Hazirlik       Hazirlik
	KaynakYayinID  string // "" orijinal
	KaynakBaslik   string
	KaynakSahipAdi string
}

// YayinKaydi, depodan okunan detay ve servisin karar icin ihtiyac
// duydugu ic alanlar. Silinmis yayinlar da doner (Silindi true);
// gorunurlugu servis DetayGorebilir ile uygular.
type YayinKaydi struct {
	YayinDetayi
	SahipID        string
	KaynakYayinID  string
	StemVar        bool // hazirlikta stem var: altyapi sonradan acilabilir
	MasterAnahtari string
	Silindi        bool
	TakipEdiyor    bool // izleyici sahibi takip ediyor
}

// gorunurlukGirdisi, kaydi izleyiciye gore gorunurluk girdisine cevirir.
func (k *YayinKaydi) gorunurlukGirdisi(izleyiciID string) GorunurlukGirdisi {
	return GorunurlukGirdisi{
		SahipID: k.SahipID, IzleyiciID: izleyiciID, Gorunurluk: k.Gorunurluk,
		Durum: k.Durum, Silindi: k.Silindi, TakipEdiyor: k.TakipEdiyor,
	}
}

// HazirlikKaydi, sosyal:hazirla isinin okudugu satir.
type HazirlikKaydi struct {
	ID       string
	SahipID  string
	Durum    string
	Silindi  bool
	Hazirlik Hazirlik
}

// HazirlikSonucu, isin basariyla bitince satira yazdiklari.
type HazirlikSonucu struct {
	MasterAnahtari   string
	StemListesi      []StemKaydi
	DalgaFormu       []float64
	OnizlemeAnahtari string // "" ise onizleme yok
}
