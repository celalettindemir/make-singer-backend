package social

import "time"

// ProfilOzeti, sozlesme §4.1. avatarRengi saklanmaz, kullanici
// kimliginden turetilir (AvatarRengi).
type ProfilOzeti struct {
	KullaniciAdi string `json:"kullaniciAdi"`
	GorunenAd    string `json:"gorunenAd"`
	AvatarRengi  int    `json:"avatarRengi"`
}

// Istatistik, profildeki sayaclar. sarki/cover yalnizca izleyicinin
// gorebildigi, yayinda olan ve silinmemis yayinlari sayar.
type Istatistik struct {
	Sarki   int `json:"sarki"`
	Takipci int `json:"takipci"`
	Takip   int `json:"takip"`
	Cover   int `json:"cover"`
}

// Profil, sozlesme §4.1.
type Profil struct {
	ProfilOzeti
	Bio              string     `json:"bio"`
	Istatistik       Istatistik `json:"istatistik"`
	TakipEdiyorMuyum bool       `json:"takipEdiyorMuyum"`
	BenMiyim         bool       `json:"benMiyim"`
}

// ProfilGirdisi, PUT /profil/ben govdesi.
type ProfilGirdisi struct {
	KullaniciAdi string `json:"kullaniciAdi"`
	GorunenAd    string `json:"gorunenAd"`
	Bio          string `json:"bio"`
}

// AdUygunlugu, GET /profil/kullanici-adi/uygun yaniti. Oneri, ad
// alinmissa ve uygun bir sayi eki bulunduysa doludur; aksi halde null.
type AdUygunlugu struct {
	Uygun bool    `json:"uygun"`
	Oneri *string `json:"oneri"`
}

// ProfilKaydi, sosyal.profil satiri.
type ProfilKaydi struct {
	KullaniciID  string
	KullaniciAdi string
	GorunenAd    string
	Bio          string
	Olusturma    time.Time
}

// ProfilSayilari, bir profilin izleyiciye gore sayaclari.
type ProfilSayilari struct {
	Istatistik       Istatistik
	TakipEdiyorMuyum bool
}
