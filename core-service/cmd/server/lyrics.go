package main

import (
	"fmt"
	"log"
	"time"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
	"github.com/makeasinger/api/internal/lyricsprovider"
)

// lyricsProviderTTL is how long a pod trusts its cached provider choice.
const lyricsProviderTTL = 30 * time.Second

// newLyricsSelector builds the lyrics provider selector (R-LYR-11). An
// unknown LYRICS_DEFAULT_PROVIDER stops startup.
func newLyricsSelector(cfg *config.Config, clients map[lyricsprovider.Name]client.ChatClient, store lyricsprovider.Store) (*lyricsprovider.Selector, error) {
	fallback, err := lyricsprovider.ParseName(cfg.Lyrics.DefaultProvider)
	if err != nil {
		return nil, fmt.Errorf("LYRICS_DEFAULT_PROVIDER: %w", err)
	}
	sel := lyricsprovider.NewSelector(clients, store, fallback, lyricsProviderTTL, time.Now)
	log.Printf("Lyrics providers: default %s, available %v", fallback, sel.Available())
	return sel, nil
}
