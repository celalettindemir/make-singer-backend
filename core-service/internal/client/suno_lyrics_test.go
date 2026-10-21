package client

import (
	"testing"

	"github.com/makeasinger/api/internal/model"
)

func TestSunoLyrics_TagsAndSkipsEmpty(t *testing.T) {
	got := SunoLyrics([]model.GuideSection{
		{ID: "s1", Type: "verse", Lines: []string{" a ", "", "b"}},
		{ID: "s2", Type: "chorus", Lines: nil},
		{ID: "s3", Type: "prechorus", Lines: []string{"c"}},
	})
	if want := "[Verse]\na\nb\n\n[Pre-Chorus]\nc"; got != want {
		t.Fatalf("SunoLyrics = %q, want %q", got, want)
	}
}

func TestSunoLyrics_TagNames(t *testing.T) {
	cases := map[string]string{
		"intro": "[Intro]", "verse": "[Verse]", "prechorus": "[Pre-Chorus]", "chorus": "[Chorus]",
		"bridge": "[Bridge]", "outro": "[Outro]", "hook": "[Hook]",
	}
	for typ, tag := range cases {
		got := SunoLyrics([]model.GuideSection{{ID: "s", Type: typ, Lines: []string{"x"}}})
		if got != tag+"\nx" {
			t.Errorf("%s → %q, want %q", typ, got, tag+"\nx")
		}
	}
}
