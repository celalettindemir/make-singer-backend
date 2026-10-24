package model

import "time"

// Device is one push-capable install of a user (uretim.cihazlar).
type Device struct {
	Token      string
	Platform   string // ios | android
	Language   string // tr | en | fr: language of the push copy
	AppVersion string
	UpdatedAt  time.Time
}
