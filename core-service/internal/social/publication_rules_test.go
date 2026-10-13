package social

import (
	"errors"
	"strings"
	"testing"
)

func validRequest() PublicationRequest {
	return PublicationRequest{
		Title: "Gece", Description: "", Visibility: VisibilityEveryone, BackingEnabled: true, CommentsEnabled: true,
		MasterURL: "masters/u/p/m.wav",
		Stems:     []StemInput{{ID: "drums", Instrument: "drums", URL: "stems/u/p/drums.mp3", Duration: 184}},
		Genre:     "pop", BPM: 120, MusicalKey: MusicalKey{Tonic: "A", Mode: "minor"}, DurationSec: 184,
	}
}

func TestNormalizePublicationRequest_Valid(t *testing.T) {
	req := validRequest()
	req.Title = "  Gece  "
	if err := normalizePublicationRequest(&req); err != nil {
		t.Fatalf("a valid request was rejected: %v", err)
	}
	if req.Title != "Gece" || req.Structure == nil || req.Lyrics.Sections == nil {
		t.Fatalf("edit = %+v", req)
	}
}

func TestNormalizePublicationRequest_Errors(t *testing.T) {
	cases := map[string]func(*PublicationRequest){
		"empty title":       func(i *PublicationRequest) { i.Title = "  " },
		"long title":        func(i *PublicationRequest) { i.Title = strings.Repeat("ş", 81) },
		"long description":  func(i *PublicationRequest) { i.Description = strings.Repeat("a", 301) },
		"visibility":        func(i *PublicationRequest) { i.Visibility = "gizli" },
		"key mode":          func(i *PublicationRequest) { i.MusicalKey.Mode = "dorian" },
		"key tonic":         func(i *PublicationRequest) { i.MusicalKey.Tonic = "" },
		"genre":             func(i *PublicationRequest) { i.Genre = "" },
		"duration":          func(i *PublicationRequest) { i.DurationSec = 0 },
		"low bpm":           func(i *PublicationRequest) { i.BPM = 19 },
		"high bpm":          func(i *PublicationRequest) { i.BPM = 301 },
		"no master":         func(i *PublicationRequest) { i.MasterURL = "" },
		"no stem":           func(i *PublicationRequest) { i.Stems = nil },
		"stem id with dots": func(i *PublicationRequest) { i.Stems[0].ID = "a.b" },
		"stem id traversal": func(i *PublicationRequest) { i.Stems[0].ID = "../x" },
		"stem duration":     func(i *PublicationRequest) { i.Stems[0].Duration = 0 },
		"stem url":          func(i *PublicationRequest) { i.Stems[0].URL = "" },
		"stem instrument":   func(i *PublicationRequest) { i.Stems[0].Instrument = "" },
		"duplicate stem":    func(i *PublicationRequest) { i.Stems = append(i.Stems, i.Stems[0]) },
		"structure item":    func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "", Bars: 4}} },
		"too many stems":    func(i *PublicationRequest) { i.Stems = make([]StemInput, 33) },
		"too many lines": func(i *PublicationRequest) {
			i.Lyrics.Sections = []LyricsSection{{SectionID: "b", Lines: make([]string, 201)}}
		},
	}
	for name, mutate := range cases {
		req := validRequest()
		mutate(&req)
		if err := normalizePublicationRequest(&req); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
}

func TestNormalizePublicationRequest_BoundaryValues(t *testing.T) {
	req := validRequest()
	req.Title, req.Description, req.BPM = strings.Repeat("ş", 80), strings.Repeat("ğ", 300), 300
	if err := normalizePublicationRequest(&req); err != nil {
		t.Fatalf("boundary values must be accepted: %v", err)
	}
	req = validRequest()
	req.BPM = 20
	if err := normalizePublicationRequest(&req); err != nil {
		t.Fatalf("bpm 20 must be accepted: %v", err)
	}
}

func TestNormalizePublicationRequest_CoverDisablesBackingDropsStems(t *testing.T) {
	req := validRequest()
	sourceID := "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	req.SourcePublicationID = &sourceID
	req.Stems[0].ID = "gecersiz.id" // ignored, not validated
	if err := normalizePublicationRequest(&req); err != nil {
		t.Fatalf("cover: %v", err)
	}
	if req.BackingEnabled || req.Stems != nil {
		t.Fatalf("a cover must have the backing track disabled and no stems: %+v", req)
	}
}

func TestNormalizePublicationRequest_EmptySourceIsOriginal(t *testing.T) {
	req := validRequest()
	empty := ""
	req.SourcePublicationID = &empty
	if err := normalizePublicationRequest(&req); err != nil || req.SourcePublicationID != nil || !req.BackingEnabled {
		t.Fatalf("an empty source must count as an original: %v %+v", err, req)
	}
}

func TestNormalizePublicationRequest_DisabledBackingIgnoresStems(t *testing.T) {
	req := validRequest()
	req.BackingEnabled = false
	req.Stems = []StemInput{{ID: "x.y"}}
	if err := normalizePublicationRequest(&req); err != nil || req.Stems != nil {
		t.Fatalf("disabled backing track: %v %+v", err, req.Stems)
	}
}

func TestNormalizePatch(t *testing.T) {
	description, vis := "  merhaba ", "link"
	patch := PublicationPatch{Description: &description, Visibility: &vis}
	if err := normalizePatch(&patch); err != nil || *patch.Description != "merhaba" {
		t.Fatalf("patch = %v %v", err, *patch.Description)
	}
	bad := "gizli"
	if err := normalizePatch(&PublicationPatch{Visibility: &bad}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid visibility: %v", err)
	}
	long := strings.Repeat("a", 301)
	if err := normalizePatch(&PublicationPatch{Description: &long}); !errors.Is(err, ErrValidation) {
		t.Fatalf("long description: %v", err)
	}
}

func TestNormalizePublicationRequest_NestedFieldLimits(t *testing.T) {
	section := func(b LyricsSection) func(*PublicationRequest) {
		return func(i *PublicationRequest) { i.Lyrics.Sections = []LyricsSection{b} }
	}
	cases := map[string]func(*PublicationRequest){
		"long line":         section(LyricsSection{SectionID: "b", Type: "verse", Lines: []string{strings.Repeat("ş", 201)}}),
		"long section id":   section(LyricsSection{SectionID: strings.Repeat("b", 65), Type: "verse"}),
		"long section type": section(LyricsSection{SectionID: "b", Type: strings.Repeat("v", 65)}),
		"long structure id": func(i *PublicationRequest) {
			i.Structure = []StructureItem{{ID: strings.Repeat("s", 65), Type: "verse"}}
		},
		"long structure type": func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "s", Type: strings.Repeat("v", 65)}} },
		"total lines": func(i *PublicationRequest) {
			i.Lyrics.Sections = make([]LyricsSection, 6)
			for idx := range i.Lyrics.Sections {
				i.Lyrics.Sections[idx] = LyricsSection{SectionID: "b", Lines: make([]string, 167)} // 1002 lines
			}
		},
	}
	for name, mutate := range cases {
		req := validRequest()
		mutate(&req)
		if err := normalizePublicationRequest(&req); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}

	req := validRequest()
	req.Structure = []StructureItem{{ID: strings.Repeat("ş", 64), Type: strings.Repeat("ğ", 64)}}
	req.Lyrics.Sections = make([]LyricsSection, 5)
	for idx := range req.Lyrics.Sections {
		req.Lyrics.Sections[idx] = LyricsSection{SectionID: strings.Repeat("ş", 64), Type: strings.Repeat("ğ", 64), Lines: make([]string, 200)}
	}
	req.Lyrics.Sections[0].Lines[0] = strings.Repeat("ş", 200)
	if err := normalizePublicationRequest(&req); err != nil {
		t.Fatalf("boundary values (1000 lines, 200/64 characters) must be accepted: %v", err)
	}
}

func TestNormalizePublicationRequest_RejectsNUL(t *testing.T) {
	cases := map[string]func(*PublicationRequest){
		"title":           func(i *PublicationRequest) { i.Title = "Ge\x00ce" },
		"description":     func(i *PublicationRequest) { i.Description = "a\x00" },
		"genre":           func(i *PublicationRequest) { i.Genre = "p\x00p" },
		"tonic":           func(i *PublicationRequest) { i.MusicalKey.Tonic = "A\x00" },
		"master url":      func(i *PublicationRequest) { i.MasterURL = "masters/u/p/m\x00.wav" },
		"stem url":        func(i *PublicationRequest) { i.Stems[0].URL = "stems/u/p/d\x00.mp3" },
		"stem instrument": func(i *PublicationRequest) { i.Stems[0].Instrument = "dr\x00ms" },
		"line": func(i *PublicationRequest) {
			i.Lyrics.Sections = []LyricsSection{{SectionID: "b", Type: "verse", Lines: []string{"bir\x00"}}}
		},
		"section id":     func(i *PublicationRequest) { i.Lyrics.Sections = []LyricsSection{{SectionID: "b\x00", Type: "verse"}} },
		"section type":   func(i *PublicationRequest) { i.Lyrics.Sections = []LyricsSection{{SectionID: "b", Type: "v\x00"}} },
		"structure id":   func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "s\x00", Type: "verse"}} },
		"structure type": func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "s", Type: "v\x00"}} },
	}
	for name, mutate := range cases {
		req := validRequest()
		mutate(&req)
		if err := normalizePublicationRequest(&req); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
}

func TestNormalizePatch_RejectsNUL(t *testing.T) {
	a := "mer\x00haba"
	if err := normalizePatch(&PublicationPatch{Description: &a}); !errors.Is(err, ErrValidation) {
		t.Fatalf("description containing NUL: %v", err)
	}
}
