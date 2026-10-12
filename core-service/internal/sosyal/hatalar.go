package sosyal

import (
	"errors"
	"fmt"
)

// Servis hatalari. HTTP katmani bunlari sozlesmedeki kucuk harfli
// kodlara cevirir (handler/sosyal.go): ErrBulunamadi -> 404 bulunamadi,
// ErrProfilYok -> 404 ya da 409 profil_yok (uca gore), vb. Mesajlar
// kullaniciya gidebilir; deger (URL, anahtar) tasimazlar.
var (
	ErrBulunamadi           = errors.New("bulunamadi")
	ErrProfilYok            = errors.New("profil henuz olusturulmamis")
	ErrGecersizKullaniciAdi = errors.New("gecersiz kullanici adi")
	ErrKullaniciAdiAlinmis  = errors.New("kullanici adi alinmis")
	ErrDogrulama            = errors.New("dogrulama hatasi")
	ErrSahiplik             = errors.New("dosya bu kullaniciya ait degil")
	ErrKaynakBulunamadi     = errors.New("kaynak yayin bulunamadi")
	ErrAltyapiKapali        = errors.New("kaynak yayinin altyapisi kapali")
	ErrStemYok              = errors.New("yayinin stem kopyasi yok")
	ErrDurum                = errors.New("yayin bu islem icin uygun durumda degil")
	ErrKimlik               = errors.New("kimlik gecersiz")
	ErrKendiniTakip         = errors.New("kendini takip edemezsin")
	ErrYorumKapali          = errors.New("yayinin yorumlari kapali")
	ErrYetki                = errors.New("bu islem icin yetkin yok")
)

// dogrulamaHatasi, ErrDogrulama'yi alan bazli bir aciklamayla sarar.
func dogrulamaHatasi(mesaj string) error {
	return fmt.Errorf("%w: %s", ErrDogrulama, mesaj)
}
