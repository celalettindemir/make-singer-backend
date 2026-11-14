package client

import (
	"errors"
	"testing"
)

func TestImportKeys_Classify(t *testing.T) {
	tests := []struct {
		key                         string
		isImport, isVocal, isMaster bool
	}{
		{"imports/u/j/vocal.mp3", true, true, false},
		{"imports/u/j/drums.mp3", true, false, false},
		{"imports/u/j/source.mp3", true, false, false},
		{"masters/u/p/imp-x.wav", false, false, true},
		{"masters/u/p/x.wav", false, false, false},
		{"stems/u/p/vocal.mp3", false, false, false},
		{"vocals/u/imports/vocal.mp3", false, false, false},
	}
	for _, tc := range tests {
		if got := IsImportKey(tc.key); got != tc.isImport {
			t.Errorf("IsImportKey(%q) = %v", tc.key, got)
		}
		if got := IsImportVocalKey(tc.key); got != tc.isVocal {
			t.Errorf("IsImportVocalKey(%q) = %v", tc.key, got)
		}
		if got := IsImportMasterKey(tc.key); got != tc.isMaster {
			t.Errorf("IsImportMasterKey(%q) = %v", tc.key, got)
		}
	}
}

func TestUserKey_ImportsOwned(t *testing.T) {
	got, err := UserKey("imports", "u1", "j1", "source.mp3")
	if err != nil || got != "imports/u1/j1/source.mp3" {
		t.Fatalf("UserKey = %q, %v", got, err)
	}
	if err := VerifyKeyOwner(got, "u1"); err != nil {
		t.Errorf("owner: %v", err)
	}
	if err := VerifyKeyOwner(got, "u2"); !errors.Is(err, ErrKeyNotOwned) {
		t.Errorf("other user: %v, want ErrKeyNotOwned", err)
	}
}
