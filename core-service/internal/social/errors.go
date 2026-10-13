package social

import (
	"errors"

	"github.com/makeasinger/api/internal/i18n"
)

// Service errors. The HTTP layer maps them to the lowercase codes of the
// contract (handler/social.go): ErrNotFound -> 404 bulunamadi,
// ErrNoProfile -> 404 or 409 profil_yok (depending on the endpoint), and so
// on. The client-facing message comes from the localized i18n key in that
// mapping; these texts only reach logs. They carry no values (URLs, keys).
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

// ValidationError is a field-level validation failure. errors.Is matches
// it against ErrValidation (422 dogrulama); the HTTP layer renders Key
// with Args in the request's language.
type ValidationError struct {
	Key  i18n.Key
	Args []any
}

func (e *ValidationError) Error() string {
	return ErrValidation.Error() + ": " + i18n.Message(i18n.TR, e.Key, e.Args...)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

// invalid returns a *ValidationError for key.
func invalid(key i18n.Key, args ...any) error {
	return &ValidationError{Key: key, Args: args}
}
