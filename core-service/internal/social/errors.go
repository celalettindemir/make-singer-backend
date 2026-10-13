package social

import (
	"errors"
	"fmt"
)

// Service errors. The HTTP layer maps them to the lowercase codes of the
// contract (handler/social.go): ErrNotFound -> 404 bulunamadi,
// ErrNoProfile -> 404 or 409 profil_yok (depending on the endpoint), and so
// on. The messages may reach the user; they carry no values (URLs, keys).
var (
	ErrNotFound         = errors.New("bulunamadi")
	ErrNoProfile        = errors.New("profil henuz olusturulmamis")
	ErrInvalidUsername  = errors.New("gecersiz kullanici adi")
	ErrUsernameTaken    = errors.New("kullanici adi alinmis")
	ErrValidation       = errors.New("dogrulama hatasi")
	ErrOwnership        = errors.New("dosya bu kullaniciya ait degil")
	ErrSourceNotFound   = errors.New("kaynak yayin bulunamadi")
	ErrBackingDisabled  = errors.New("kaynak yayinin altyapisi kapali")
	ErrNoStems          = errors.New("yayinin stem kopyasi yok")
	ErrInvalidStatus    = errors.New("yayin bu islem icin uygun durumda degil")
	ErrInvalidIdentity  = errors.New("kimlik gecersiz")
	ErrSelfFollow       = errors.New("kendini takip edemezsin")
	ErrCommentsDisabled = errors.New("yayinin yorumlari kapali")
	ErrForbidden        = errors.New("bu islem icin yetkin yok")
)

// validationError wraps ErrValidation with a field-specific description.
func validationError(message string) error {
	return fmt.Errorf("%w: %s", ErrValidation, message)
}
