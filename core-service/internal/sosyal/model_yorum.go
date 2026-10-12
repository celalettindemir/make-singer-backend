package sosyal

import "time"

// Yorum, sozlesme §4.1. ZamanMs damgasiz yorumda null olarak GORUNUR.
type Yorum struct {
	ID             string      `json:"id"`
	Yazar          ProfilOzeti `json:"yazar"`
	Metin          string      `json:"metin"`
	ZamanMs        *int        `json:"zamanMs"`
	Olusturma      time.Time   `json:"olusturma"`
	SilebilirMiyim bool        `json:"silebilirMiyim"`
}

// YorumGirdisi, POST /yayinlar/{id}/yorumlar govdesi.
type YorumGirdisi struct {
	Metin   string `json:"metin"`
	ZamanMs *int   `json:"zamanMs"`
}

// YeniYorum, depoya yazilacak dogrulanmis yorum.
type YeniYorum struct {
	ID      string
	YayinID string
	YazarID string
	Metin   string
	ZamanMs *int
}

// YorumKaydi, silme kararinin ihtiyac duydugu satir.
type YorumKaydi struct {
	ID            string
	YayinID       string
	YazarID       string
	YayinSahibiID string
	Silindi       bool
}
