package service

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func collect(lines *[]string) func(model.LyricLine) error {
	return func(l model.LyricLine) error {
		*lines = append(*lines, l.Text)
		return nil
	}
}

func TestLineSplitter_JoinsChunks(t *testing.T) {
	var got []string
	s := newLineSplitter(model.LanguageTR, collect(&got))
	for _, c := range []string{"Gece boy", "unca\nŞehir ışık", "ları\n"} {
		if err := s.Write(c); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"Gece boyunca", "Şehir ışıkları"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLineSplitter_CloseEmitsTail(t *testing.T) {
	var got []string
	s := newLineSplitter(model.LanguageTR, collect(&got))
	if err := s.Write("a\nb"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %q", got)
	}
}

func TestLineSplitter_StopsAt16(t *testing.T) {
	var got []string
	s := newLineSplitter(model.LanguageTR, collect(&got))
	err := s.Write(strings.Repeat("x\n", 20))
	if !errors.Is(err, errLineLimit) {
		t.Fatalf("err = %v, want errLineLimit", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 16 || s.Count() != 16 {
		t.Fatalf("emitted %d, count %d", len(got), s.Count())
	}
}

func TestLineSplitter_PropagatesEmitError(t *testing.T) {
	s := newLineSplitter(model.LanguageTR, func(model.LyricLine) error { return io.ErrClosedPipe })
	if err := s.Write("a\n"); err != io.ErrClosedPipe {
		t.Fatalf("err = %v", err)
	}
}

func TestCleanLine(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"  [Chorus]", "", false},
		{"Nakarat:", "", false},
		{"KÖPRÜ", "", false},
		{"Kıta:", "", false},
		{"GİRİŞ", "", false},
		{"ÇIKIŞ", "", false},
		{"(x2)", "", false},
		{"", "", false},
		{"1. Gece", "Gece", true},
		{"2) Gece", "Gece", true},
		{"- Gece", "Gece", true},
		{"• Gece", "Gece", true},
		{"\"Gece\"", "Gece", true},
		{"“Gece”", "Gece", true},
		{"«Gece»", "Gece", true},
		{"Gece\r", "Gece", true},
		{"Chorus of birds sings", "Chorus of birds sings", true},
		{"Verse 1:", "", false},
		{"Nakarat 2", "", false},
		{"Chorus (x2)", "", false},
		{"Chorus 2 (x3):", "", false},
		{"1. [Chorus]", "", false},
		{"- Nakarat", "", false},
	}
	for _, c := range cases {
		got, ok := cleanLine(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("cleanLine(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestLineSplitter_MarkOnlyLineNotCounted(t *testing.T) {
	var got []model.LyricLine
	s := newLineSplitter(model.LanguageTR, func(l model.LyricLine) error { got = append(got, l); return nil })
	if err := s.Write("Gece boyunca şehir ışıkları~\n|\nSen yanımdayken\n"); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 || len(got) != 2 || got[0].Text != "Gece boyunca şehir ışıkları" || len(got[0].Cues) != 1 {
		t.Fatal(got)
	}
}
