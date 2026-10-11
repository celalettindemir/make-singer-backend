package model

import "time"

// FileSignRequest, istemcinin elinde tuttugu (suresi gecmis olabilecek)
// dosya adreslerini tazelemek icin gonderdigi istektir.
type FileSignRequest struct {
	URLs []string `json:"urls" validate:"required,min=1,max=50,dive,required"`
}

// SignedFile, bir adresin taze hali. Public (exports/) anahtarlarda
// ExpiresAt yoktur.
type SignedFile struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// FileSignResponse, girdiyle AYNI sirada taze adresleri tasir.
type FileSignResponse struct {
	Files []SignedFile `json:"files"`
}
