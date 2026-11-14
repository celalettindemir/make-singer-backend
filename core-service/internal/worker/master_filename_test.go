package worker

import (
	"strings"
	"testing"
)

func TestMasterFileName_ImportPrefix(t *testing.T) {
	if got := masterFileName([]string{"stems/u/p/a.mp3", "imports/u/j/drums.mp3"}); !strings.HasPrefix(got, "imp-") || !strings.HasSuffix(got, ".wav") {
		t.Errorf("import stems: %q, want imp-<uuid>.wav", got)
	}
	if got := masterFileName([]string{"stems/u/p/a.mp3"}); strings.HasPrefix(got, "imp-") || !strings.HasSuffix(got, ".wav") {
		t.Errorf("normal stems: %q, want <uuid>.wav", got)
	}
	if got := masterFileName(nil); strings.HasPrefix(got, "imp-") {
		t.Errorf("no stems: %q", got)
	}
}

func TestMasterFileName_ImportMasterAsStem(t *testing.T) {
	if got := masterFileName([]string{"masters/u/p/imp-3f2b.wav"}); !strings.HasPrefix(got, "imp-") {
		t.Errorf("import master as stem: %q, want imp-<uuid>.wav", got)
	}
}
