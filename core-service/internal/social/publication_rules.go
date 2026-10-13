package social

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// stemIDPattern: a stem id becomes a key segment (stems/{stemId}.{ext}); it
// is the segment pattern without the extension.
var stemIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const (
	maxTitleLen       = 80
	maxDescriptionLen = 300
	maxGenreLen       = 40
	maxTonicLen       = 16
	maxInstrumentLen  = 40
	minBPM, maxBPM    = 20, 300
	maxStems          = 32
	maxStructureItems = 200
	maxLyricsSections = 200
	maxSectionLines   = 200
	maxTotalLines     = 1000 // total of the lines in all sections
	maxLineLen        = 200  // a single lyrics line (runes)
	maxItemFieldLen   = 64   // sectionId, section type, structure id and type (runes)
)

// ValidVisibility reports whether the value is one of the three visibilities.
func ValidVisibility(visibility string) bool {
	return visibility == VisibilityEveryone || visibility == VisibilityFollowers || visibility == VisibilityLink
}

func runeCountInRange(s string, minCount, maxCount int) bool {
	n := utf8.RuneCountInString(s)
	return n >= minCount && n <= maxCount
}

// containsNUL reports whether any of the values contains a NUL character.
// Postgres rejects NUL in text and jsonb fields; the client must get a 422
// instead of a 500.
func containsNUL(values ...string) bool {
	for _, d := range values {
		if strings.IndexByte(d, 0) >= 0 {
			return true
		}
	}
	return false
}

// normalizePublicationRequest trims the request, fills empty arrays and
// applies the contract rules. The source (cover) and URL ownership are
// checked in the service; only the format is checked here.
func normalizePublicationRequest(req *PublicationRequest) error {
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	switch {
	case containsNUL(req.Title, req.Description, req.Genre, req.MusicalKey.Tonic, req.MusicalKey.Mode, req.MasterURL):
		return validationError("metin alanlari NUL karakteri iceremez")
	case !runeCountInRange(req.Title, 1, maxTitleLen):
		return validationError("baslik 1-80 karakter olmali")
	case !runeCountInRange(req.Description, 0, maxDescriptionLen):
		return validationError("aciklama en cok 300 karakter olabilir")
	case !ValidVisibility(req.Visibility):
		return validationError("gorunurluk herkes, takipciler ya da link olmali")
	case req.MusicalKey.Mode != "major" && req.MusicalKey.Mode != "minor":
		return validationError("ton.mod major ya da minor olmali")
	case !runeCountInRange(req.MusicalKey.Tonic, 1, maxTonicLen):
		return validationError("ton.tonik bos olamaz")
	case !runeCountInRange(req.Genre, 1, maxGenreLen):
		return validationError("tur 1-40 karakter olmali")
	case req.DurationSec <= 0:
		return validationError("sureSn sifirdan buyuk olmali")
	case req.BPM < minBPM || req.BPM > maxBPM:
		return validationError("bpm 20-300 araliginda olmali")
	case req.MasterURL == "":
		return validationError("masterUrl zorunlu")
	case len(req.Structure) > maxStructureItems:
		return validationError("yapi cok uzun")
	case len(req.Lyrics.Sections) > maxLyricsSections:
		return validationError("sozler cok uzun")
	}
	if req.Structure == nil {
		req.Structure = []StructureItem{}
	}
	for _, item := range req.Structure {
		if item.Bars < 0 || containsNUL(item.ID, item.Type) ||
			!runeCountInRange(item.ID, 1, maxItemFieldLen) || !runeCountInRange(item.Type, 0, maxItemFieldLen) {
			return validationError("yapi ogesi gecersiz")
		}
	}
	if req.Lyrics.Sections == nil {
		req.Lyrics.Sections = []LyricsSection{}
	}
	totalLines := 0
	for i := range req.Lyrics.Sections {
		section := &req.Lyrics.Sections[i]
		totalLines += len(section.Lines)
		if len(section.Lines) > maxSectionLines || totalLines > maxTotalLines {
			return validationError("sozler cok uzun")
		}
		if containsNUL(section.SectionID, section.Type) || !runeCountInRange(section.SectionID, 0, maxItemFieldLen) ||
			!runeCountInRange(section.Type, 0, maxItemFieldLen) {
			return validationError("soz bolumu gecersiz")
		}
		for _, line := range section.Lines {
			if containsNUL(line) || !runeCountInRange(line, 0, maxLineLen) {
				return validationError("soz satiri gecersiz")
			}
		}
		if section.Lines == nil {
			section.Lines = []string{}
		}
	}

	if req.SourcePublicationID != nil && *req.SourcePublicationID == "" {
		req.SourcePublicationID = nil
	}
	if req.SourcePublicationID != nil {
		// A cover's backing is always disabled; the stem list is ignored.
		req.BackingEnabled = false
		req.Stems = nil
		return nil
	}
	if !req.BackingEnabled {
		// If backing is disabled, no stems are copied; the list is ignored.
		req.Stems = nil
		return nil
	}
	if len(req.Stems) == 0 {
		return validationError("altyapi acikken en az bir stem gerekli")
	}
	if len(req.Stems) > maxStems {
		return validationError("cok fazla stem")
	}
	seen := make(map[string]bool, len(req.Stems))
	for _, stem := range req.Stems {
		if !stemIDPattern.MatchString(stem.ID) {
			return validationError("stem id gecersiz")
		}
		if seen[stem.ID] {
			return validationError("stem id tekrar ediyor")
		}
		seen[stem.ID] = true
		if stem.URL == "" || stem.Duration <= 0 || !runeCountInRange(stem.Instrument, 1, maxInstrumentLen) ||
			containsNUL(stem.URL, stem.Instrument) {
			return validationError("stem alanlari gecersiz")
		}
	}
	return nil
}

// normalizePatch validates the PATCH body.
func normalizePatch(patch *PublicationPatch) error {
	if patch.Description != nil {
		text := strings.TrimSpace(*patch.Description)
		if !runeCountInRange(text, 0, maxDescriptionLen) {
			return validationError("aciklama en cok 300 karakter olabilir")
		}
		if containsNUL(text) {
			return validationError("aciklama NUL karakteri iceremez")
		}
		patch.Description = &text
	}
	if patch.Visibility != nil && !ValidVisibility(*patch.Visibility) {
		return validationError("gorunurluk herkes, takipciler ya da link olmali")
	}
	return nil
}
