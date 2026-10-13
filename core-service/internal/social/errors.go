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
	ErrNotFound         = errors.New("not found")
	ErrNoProfile        = errors.New("profile not created yet")
	ErrInvalidUsername  = errors.New("invalid username")
	ErrUsernameTaken    = errors.New("username already taken")
	ErrValidation       = errors.New("validation error")
	ErrOwnership        = errors.New("file does not belong to this user")
	ErrSourceNotFound   = errors.New("source publication not found")
	ErrBackingDisabled  = errors.New("source publication backing track is disabled")
	ErrNoStems          = errors.New("publication has no stem copy")
	ErrInvalidStatus    = errors.New("publication is not in a valid state for this operation")
	ErrInvalidIdentity  = errors.New("invalid identity")
	ErrSelfFollow       = errors.New("you cannot follow yourself")
	ErrCommentsDisabled = errors.New("comments are disabled for this publication")
	ErrForbidden        = errors.New("you are not allowed to do this")
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
