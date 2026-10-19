package model

import "errors"

// LyricsSongSection is one section of the song sent as context (R-LYR-05).
type LyricsSongSection struct {
	Type   SectionType `json:"type" validate:"required,oneof=intro verse prechorus chorus bridge outro instrumental"`
	Bars   *int        `json:"bars,omitempty" validate:"omitempty,min=1,max=64"`
	Lyrics string      `json:"lyrics,omitempty" validate:"max=2000"`
}

// LyricsContext is the optional song context of both lyrics requests
// (R-LYR-05, R-LYR-08, R-LYR-10). A request without it is answered as before.
type LyricsContext struct {
	Topic        string              `json:"topic,omitempty" validate:"max=500"`
	BPM          *int                `json:"bpm,omitempty" validate:"omitempty,min=40,max=220"`
	Song         []LyricsSongSection `json:"song,omitempty" validate:"omitempty,max=32,dive"`
	SectionIndex *int                `json:"sectionIndex,omitempty"`
}

// ErrSectionIndex is returned by CheckIndex.
var ErrSectionIndex = errors.New("sectionIndex does not point at a song section of the requested type")

// CheckIndex validates the cross-field rule: with a song, sectionIndex is
// present, in range, and names a section of sectionType.
func (c LyricsContext) CheckIndex(sectionType SectionType) error {
	if len(c.Song) == 0 {
		return nil
	}
	if c.SectionIndex == nil || *c.SectionIndex < 0 || *c.SectionIndex >= len(c.Song) {
		return ErrSectionIndex
	}
	if c.Song[*c.SectionIndex].Type != sectionType {
		return ErrSectionIndex
	}
	return nil
}
