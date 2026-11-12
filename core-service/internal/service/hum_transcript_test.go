package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/model"
)

func TestHumLines(t *testing.T) {
	long := strings.Repeat("ağacım ", 36) // 252 runes, the cut falls inside a word
	var sentences []string
	for i := 0; i < 10; i++ {
		sentences = append(sentences, "gece yolunda yürüdüm")
	}
	sentences[1] = long + "bitti"
	text := strings.Join(sentences, ". ")

	tests := []struct {
		name     string
		text     string
		segments []string
		lang     model.Language
		want     []string
	}{
		{"spec 7 kayıt 1", "Oy şeftaşım, şeftaşım. Sarı sarı gözlerine kurban olmuşum. Malım çiçeğim", nil, "tr",
			[]string{"Oy şeftaşım, şeftaşım", "Sarı sarı gözlerine kurban olmuşum", "Malım çiçeğim"}},
		{"spec 7 kayıt 2: sadece melodi", "ımm nanana eee yam ımm nanana…", nil, "tr", nil},
		{"tek anlamlı kelime", "nanana sevdim lalala", nil, "tr", nil},
		{"baş ve sondaki dolgu atılır", "na na seni sevdim la la", nil, "tr", []string{"seni sevdim"}},
		{"ortadaki da kalır", "sen de gel o da gelsin", nil, "tr", []string{"sen de gel o da gelsin"}},
		{"segmentler metne üstün", "yok sayılır", []string{" Oy şeftaşım ", "Sarı sarı"}, "tr", []string{"Oy şeftaşım", "Sarı sarı"}},
		{"boş", "", nil, "tr", nil},
		{"baba kalır", "baba seni sevdim", nil, "tr", []string{"baba seni sevdim"}},
		{"papa kalır", "papa seni sevdim", nil, "tr", []string{"papa seni sevdim"}},
		{"dede kalır", "dede seni sevdim", nil, "tr", []string{"dede seni sevdim"}},
		{"im kelimesi kalır", "im seni sevdim", nil, "tr", []string{"im seni sevdim"}},
		{"ımm atılır", "ımm seni sevdim", nil, "tr", []string{"seni sevdim"}},
		{"nanana atılır", "nanana seni sevdim", nil, "tr", []string{"seni sevdim"}},
		{"lalala atılır", "lalala seni sevdim", nil, "tr", []string{"seni sevdim"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HumLines(&client.Transcription{Text: tt.text, Segments: tt.segments}, tt.lang)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("HumLines = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("en çok 8 satır, 200 karakter", func(t *testing.T) {
		got := HumLines(&client.Transcription{Text: text}, "tr")
		if len(got) != 8 {
			t.Fatalf("len = %d, want 8", len(got))
		}
		if n := len([]rune(got[1])); n != 200 {
			t.Errorf("long line = %d runes, want 200", n)
		}
		if got[0] != "gece yolunda yürüdüm" {
			t.Errorf("got[0] = %q", got[0])
		}
	})

	t.Run("nil transcription", func(t *testing.T) {
		if got := HumLines(nil, "tr"); got != nil {
			t.Errorf("got %q, want nil", got)
		}
	})
}

func TestHumLines_UnknownLanguageUsesAllFillers(t *testing.T) {
	got := HumLines(&client.Transcription{Text: "euh la la love you hmm"}, "")
	if want := []string{"love you"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
