package model

import "time"

// ExpiresPtr converts a zero time to nil. For permanent (public) links the
// expiresAt field must not appear in the JSON at all: sending
// `0001-01-01T00:00:00Z` would make a permanent link look expired.
func ExpiresPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
