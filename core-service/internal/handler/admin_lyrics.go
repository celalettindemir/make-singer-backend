package handler

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/lyricsprovider"
	"github.com/makeasinger/api/internal/middleware"
	"github.com/makeasinger/api/pkg/response"
)

// LyricsProviderAdmin reads and sets the AI lyricist (R-LYR-11)
// (*lyricsprovider.Selector).
type LyricsProviderAdmin interface {
	Active(ctx context.Context) (lyricsprovider.Name, client.ChatClient)
	Available() []lyricsprovider.Name
	Set(ctx context.Context, name lyricsprovider.Name, by string) error
}

// WithLyricsProvider binds the lyrics-provider routes.
func (h *AdminHandler) WithLyricsProvider(p LyricsProviderAdmin) *AdminHandler {
	h.lyrics = p
	return h
}

type lyricsProviderState struct {
	Provider  lyricsprovider.Name   `json:"provider"`
	Available []lyricsprovider.Name `json:"available"`
}

func (h *AdminHandler) lyricsState(ctx context.Context) lyricsProviderState {
	name, _ := h.lyrics.Active(ctx)
	available := h.lyrics.Available()
	if available == nil {
		available = []lyricsprovider.Name{}
	}
	return lyricsProviderState{Provider: name, Available: available}
}

// GetLyricsProvider handles GET /api/admin/lyrics-provider.
func (h *AdminHandler) GetLyricsProvider(c *fiber.Ctx) error {
	return response.OK(c, h.lyricsState(c.UserContext()))
}

// SetLyricsProvider handles PUT /api/admin/lyrics-provider.
func (h *AdminHandler) SetLyricsProvider(c *fiber.Ctx) error {
	var req struct {
		Provider *string `json:"provider"`
	}
	if !decodeBody(c, &req) {
		return invalidField(c, "body")
	}
	if req.Provider == nil {
		return invalidField(c, "provider")
	}
	name, err := lyricsprovider.ParseName(*req.Provider)
	if err != nil {
		return invalidField(c, "provider")
	}
	if err := h.lyrics.Set(c.UserContext(), name, middleware.GetAdminEmail(c)); err != nil {
		if errors.Is(err, lyricsprovider.ErrUnavailable) {
			return response.ValidationError(c, i18n.KeyValidationFailed, map[string]string{"provider": "unavailable"})
		}
		return adminFailure(c, "set lyrics provider", err)
	}
	return response.OK(c, h.lyricsState(c.UserContext()))
}
