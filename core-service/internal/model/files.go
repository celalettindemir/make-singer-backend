package model

import "time"

// FileSignRequest is sent by the client to refresh the file URLs it holds,
// which may have expired.
type FileSignRequest struct {
	URLs []string `json:"urls" validate:"required,min=1,max=50,dive,required"`
}

// SignedFile is the fresh form of a URL. Public (exports/) keys have no
// ExpiresAt.
type SignedFile struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// FileSignResponse carries the fresh URLs in the SAME order as the input.
type FileSignResponse struct {
	Files []SignedFile `json:"files"`
}
