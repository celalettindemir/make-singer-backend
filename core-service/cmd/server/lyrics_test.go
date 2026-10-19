package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/lyricsprovider"
)

type nilStore struct{}

func (nilStore) Get(context.Context) (lyricsprovider.Name, bool, error) { return "", false, nil }
func (nilStore) Set(context.Context, lyricsprovider.Name, string) error { return nil }

func TestNewLyricsSelector_UnknownDefaultFails(t *testing.T) {
	cfg := &config.Config{Lyrics: config.LyricsConfig{DefaultProvider: "gemini"}}
	if _, err := newLyricsSelector(cfg, map[lyricsprovider.Name]client.ChatClient{}, nilStore{}); err == nil {
		t.Fatal("an unknown LYRICS_DEFAULT_PROVIDER must stop startup")
	}
}

func TestNewLyricsSelector_UsesConfiguredClientsAndDefault(t *testing.T) {
	cfg := &config.Config{Lyrics: config.LyricsConfig{DefaultProvider: "openai"}}
	clients := map[lyricsprovider.Name]client.ChatClient{
		lyricsprovider.Groq:      client.NewGroqClient(&config.GroqConfig{APIKey: "g"}),
		lyricsprovider.OpenAI:    client.NewOpenAIClient(&config.OpenAIConfig{APIKey: "o"}),
		lyricsprovider.Anthropic: client.NewAnthropicClient(&config.AnthropicConfig{}),
	}
	sel, err := newLyricsSelector(cfg, clients, nilStore{})
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.Available(); !reflect.DeepEqual(got, []lyricsprovider.Name{lyricsprovider.Groq, lyricsprovider.OpenAI}) {
		t.Errorf("Available = %v", got)
	}
	if name, _ := sel.Active(context.Background()); name != lyricsprovider.OpenAI {
		t.Errorf("Active = %v, want the default openai", name)
	}
}
