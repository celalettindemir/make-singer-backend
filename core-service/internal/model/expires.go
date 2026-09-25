package model

import "time"

// ExpiresPtr, sifir zamani nil'e cevirir. Kalici (public) linklerde
// expiresAt alani JSON'da HIC gorunmesin diye: `0001-01-01T00:00:00Z`
// gondermek kalici bir linki suresi dolmus gibi gosterirdi.
func ExpiresPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
