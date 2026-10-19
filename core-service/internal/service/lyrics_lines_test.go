package service

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func collect(lines *[]string) func(string) error {
	return func(l string) error {
		*lines = append(*lines, l)
		return nil
	}
}

func TestLineSplitter_JoinsChunks(t *testing.T) {
	var got []string
	s := newLineSplitter(collect(&got))
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
	s := newLineSplitter(collect(&got))
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
	s := newLineSplitter(collect(&got))
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
	s := newLineSplitter(func(string) error { return io.ErrClosedPipe })
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
	}
	for _, c := range cases {
		got, ok := cleanLine(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("cleanLine(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
