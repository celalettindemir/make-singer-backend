package model

import "time"

// VocalRangeRecord is a user's measured (or assumed) singing range, stored
// server-side so a render started without a range can fall back to it.
// Notes are MIDI numbers.
type VocalRangeRecord struct {
	Low           int       `json:"low" validate:"min=24,max=96"`
	High          int       `json:"high" validate:"min=24,max=96,gtfield=Low"`
	ComfortCenter int       `json:"comfortCenter" validate:"min=24,max=96"`
	MeasuredAt    time.Time `json:"measuredAt" validate:"required"`
	Source        string    `json:"source" validate:"required,oneof=measured assumed"`
}

// CenterErrors reports comfortCenter when it lies outside low..high.
func (r VocalRangeRecord) CenterErrors() map[string]string {
	if r.ComfortCenter < r.Low || r.ComfortCenter > r.High {
		return map[string]string{"comfortCenter": "range"}
	}
	return nil
}
