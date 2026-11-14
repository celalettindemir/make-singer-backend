package worker

import (
	"strings"
	"testing"
)

func TestMasterFileName_ImportPrefix(t *testing.T) {
	if got := masterFileName([]string{"stems/u/p/a.mp3", "imports/u/j/drums.mp3"}, nil); !strings.HasPrefix(got, "imp-") || !strings.HasSuffix(got, ".wav") {
		t.Errorf("import stems: %q, want imp-<uuid>.wav", got)
	}
	if got := masterFileName([]string{"stems/u/p/a.mp3"}, nil); strings.HasPrefix(got, "imp-") || !strings.HasSuffix(got, ".wav") {
		t.Errorf("normal stems: %q, want <uuid>.wav", got)
	}
	if got := masterFileName(nil, nil); strings.HasPrefix(got, "imp-") {
		t.Errorf("no stems: %q", got)
	}
}

func TestMasterFileName_ImportMasterAsStem(t *testing.T) {
	if got := masterFileName([]string{"masters/u/p/imp-3f2b.wav"}, nil); !strings.HasPrefix(got, "imp-") {
		t.Errorf("import master as stem: %q, want imp-<uuid>.wav", got)
	}
}

func TestMasterFileName_ImportKeyAsTake(t *testing.T) {
	for _, k := range []string{"imports/u/j/rest.mp3", "masters/u/p/imp-3f2b.wav", "exports/u/imp-3f2b.mp3"} {
		if got := masterFileName([]string{"stems/u/p/a.mp3"}, []string{k}); !strings.HasPrefix(got, "imp-") {
			t.Errorf("take %q: %q, want imp-<uuid>.wav", k, got)
		}
	}
	if got := masterFileName([]string{"stems/u/p/a.mp3"}, []string{"vocals/u/p/t.wav"}); strings.HasPrefix(got, "imp-") {
		t.Errorf("normal take: %q", got)
	}
}
