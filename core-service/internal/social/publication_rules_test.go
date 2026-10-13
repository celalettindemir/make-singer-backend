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
		t.Fatalf("gecerli istek reddedildi: %v", err)
	}
	if req.Title != "Gece" || req.Structure == nil || req.Lyrics.Sections == nil {
		t.Fatalf("duzenleme = %+v", req)
	}
}

func TestNormalizePublicationRequest_Errors(t *testing.T) {
	cases := map[string]func(*PublicationRequest){
		"bos baslik":       func(i *PublicationRequest) { i.Title = "  " },
		"uzun baslik":      func(i *PublicationRequest) { i.Title = strings.Repeat("ş", 81) },
		"uzun aciklama":    func(i *PublicationRequest) { i.Description = strings.Repeat("a", 301) },
		"gorunurluk":       func(i *PublicationRequest) { i.Visibility = "gizli" },
		"ton mod":          func(i *PublicationRequest) { i.MusicalKey.Mode = "dorian" },
		"ton tonik":        func(i *PublicationRequest) { i.MusicalKey.Tonic = "" },
		"tur":              func(i *PublicationRequest) { i.Genre = "" },
		"sure":             func(i *PublicationRequest) { i.DurationSec = 0 },
		"bpm dusuk":        func(i *PublicationRequest) { i.BPM = 19 },
		"bpm yuksek":       func(i *PublicationRequest) { i.BPM = 301 },
		"master yok":       func(i *PublicationRequest) { i.MasterURL = "" },
		"stem yok":         func(i *PublicationRequest) { i.Stems = nil },
		"stem id noktali":  func(i *PublicationRequest) { i.Stems[0].ID = "a.b" },
		"stem id tirmanma": func(i *PublicationRequest) { i.Stems[0].ID = "../x" },
		"stem sure":        func(i *PublicationRequest) { i.Stems[0].Duration = 0 },
		"stem url":         func(i *PublicationRequest) { i.Stems[0].URL = "" },
		"stem enstruman":   func(i *PublicationRequest) { i.Stems[0].Instrument = "" },
		"stem tekrar":      func(i *PublicationRequest) { i.Stems = append(i.Stems, i.Stems[0]) },
		"yapi ogesi":       func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "", Bars: 4}} },
		"cok stem":         func(i *PublicationRequest) { i.Stems = make([]StemInput, 33) },
		"cok satir": func(i *PublicationRequest) {
			i.Lyrics.Sections = []LyricsSection{{SectionID: "b", Lines: make([]string, 201)}}
		},
	}
	for name, mutate := range cases {
		req := validRequest()
		mutate(&req)
		if err := normalizePublicationRequest(&req); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: ErrDogrulama bekleniyordu, gelen %v", name, err)
		}
	}
}

func TestNormalizePublicationRequest_BoundaryValues(t *testing.T) {
	req := validRequest()
	req.Title, req.Description, req.BPM = strings.Repeat("ş", 80), strings.Repeat("ğ", 300), 300
	if err := normalizePublicationRequest(&req); err != nil {
		t.Fatalf("sinir degerler kabul edilmeli: %v", err)
	}
	req = validRequest()
	req.BPM = 20
	if err := normalizePublicationRequest(&req); err != nil {
		t.Fatalf("bpm 20 kabul edilmeli: %v", err)
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
		t.Fatalf("cover altyapisi kapali ve stemsiz olmali: %+v", req)
	}
}

func TestNormalizePublicationRequest_EmptySourceIsOriginal(t *testing.T) {
	req := validRequest()
	empty := ""
	req.SourcePublicationID = &empty
	if err := normalizePublicationRequest(&req); err != nil || req.SourcePublicationID != nil || !req.BackingEnabled {
		t.Fatalf("bos kaynak orijinal sayilmali: %v %+v", err, req)
	}
}

func TestNormalizePublicationRequest_DisabledBackingIgnoresStems(t *testing.T) {
	req := validRequest()
	req.BackingEnabled = false
	req.Stems = []StemInput{{ID: "x.y"}}
	if err := normalizePublicationRequest(&req); err != nil || req.Stems != nil {
		t.Fatalf("kapali altyapi: %v %+v", err, req.Stems)
	}
}

func TestNormalizePatch(t *testing.T) {
	description, vis := "  merhaba ", "link"
	patch := PublicationPatch{Description: &description, Visibility: &vis}
	if err := normalizePatch(&patch); err != nil || *patch.Description != "merhaba" {
		t.Fatalf("yama = %v %v", err, *patch.Description)
	}
	bad := "gizli"
	if err := normalizePatch(&PublicationPatch{Visibility: &bad}); !errors.Is(err, ErrValidation) {
		t.Fatalf("gecersiz gorunurluk: %v", err)
	}
	long := strings.Repeat("a", 301)
	if err := normalizePatch(&PublicationPatch{Description: &long}); !errors.Is(err, ErrValidation) {
		t.Fatalf("uzun aciklama: %v", err)
	}
}

func TestNormalizePublicationRequest_NestedFieldLimits(t *testing.T) {
	section := func(b LyricsSection) func(*PublicationRequest) {
		return func(i *PublicationRequest) { i.Lyrics.Sections = []LyricsSection{b} }
	}
	cases := map[string]func(*PublicationRequest){
		"uzun satir":     section(LyricsSection{SectionID: "b", Type: "verse", Lines: []string{strings.Repeat("ş", 201)}}),
		"uzun bolum id":  section(LyricsSection{SectionID: strings.Repeat("b", 65), Type: "verse"}),
		"uzun bolum tur": section(LyricsSection{SectionID: "b", Type: strings.Repeat("v", 65)}),
		"uzun yapi id": func(i *PublicationRequest) {
			i.Structure = []StructureItem{{ID: strings.Repeat("s", 65), Type: "verse"}}
		},
		"uzun yapi tur": func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "s", Type: strings.Repeat("v", 65)}} },
		"toplam satir": func(i *PublicationRequest) {
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
			t.Errorf("%s: ErrDogrulama bekleniyordu, gelen %v", name, err)
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
		t.Fatalf("sinir degerler (1000 satir, 200/64 karakter) kabul edilmeli: %v", err)
	}
}

func TestNormalizePublicationRequest_RejectsNUL(t *testing.T) {
	cases := map[string]func(*PublicationRequest){
		"baslik":         func(i *PublicationRequest) { i.Title = "Ge\x00ce" },
		"aciklama":       func(i *PublicationRequest) { i.Description = "a\x00" },
		"tur":            func(i *PublicationRequest) { i.Genre = "p\x00p" },
		"tonik":          func(i *PublicationRequest) { i.MusicalKey.Tonic = "A\x00" },
		"master url":     func(i *PublicationRequest) { i.MasterURL = "masters/u/p/m\x00.wav" },
		"stem url":       func(i *PublicationRequest) { i.Stems[0].URL = "stems/u/p/d\x00.mp3" },
		"stem enstruman": func(i *PublicationRequest) { i.Stems[0].Instrument = "dr\x00ms" },
		"satir": func(i *PublicationRequest) {
			i.Lyrics.Sections = []LyricsSection{{SectionID: "b", Type: "verse", Lines: []string{"bir\x00"}}}
		},
		"bolum id":  func(i *PublicationRequest) { i.Lyrics.Sections = []LyricsSection{{SectionID: "b\x00", Type: "verse"}} },
		"bolum tur": func(i *PublicationRequest) { i.Lyrics.Sections = []LyricsSection{{SectionID: "b", Type: "v\x00"}} },
		"yapi id":   func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "s\x00", Type: "verse"}} },
		"yapi tur":  func(i *PublicationRequest) { i.Structure = []StructureItem{{ID: "s", Type: "v\x00"}} },
	}
	for name, mutate := range cases {
		req := validRequest()
		mutate(&req)
		if err := normalizePublicationRequest(&req); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: ErrDogrulama bekleniyordu, gelen %v", name, err)
		}
	}
}

func TestNormalizePatch_RejectsNUL(t *testing.T) {
	a := "mer\x00haba"
	if err := normalizePatch(&PublicationPatch{Description: &a}); !errors.Is(err, ErrValidation) {
		t.Fatalf("NUL iceren aciklama: %v", err)
	}
}
