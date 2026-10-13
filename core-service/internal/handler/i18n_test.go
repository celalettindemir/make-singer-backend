package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
)

// TestErrorMessages_FollowAcceptLanguage: the message follows
// Accept-Language (Turkish without it) while status and code stay fixed.
func TestErrorMessages_FollowAcceptLanguage(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		user     string
		status   int
		code     string
		header   string
		wantLang string
		wantMsg  string
	}{
		{"sentinel en", social.ErrNoProfile, socialTestUser, http.StatusNotFound, "profil_yok", "en-US,en;q=0.9", "en", "The profile has not been created yet."},
		{"sentinel default tr", social.ErrNoProfile, socialTestUser, http.StatusNotFound, "profil_yok", "", "tr", "Profil henüz oluşturulmamış."},
		{"validation args en", &social.ValidationError{Key: i18n.KeyFieldFileURLInvalid, Args: []any{"masterUrl"}}, socialTestUser,
			http.StatusUnprocessableEntity, "dogrulama", "en", "en", "masterUrl: invalid file URL."},
		{"validation args tr", &social.ValidationError{Key: i18n.KeyFieldFileURLInvalid, Args: []any{"masterUrl"}}, socialTestUser,
			http.StatusUnprocessableEntity, "dogrulama", "tr-TR", "tr", "masterUrl: geçersiz dosya adresi."},
		{"unauthenticated en", nil, "", http.StatusUnauthorized, "UNAUTHORIZED", "de, en;q=0.5", "en", "Authentication failed."},
		{"unauthenticated tr", nil, "", http.StatusUnauthorized, "UNAUTHORIZED", "fr", "tr", "Kimlik doğrulanamadı."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newSocialApp(&fakeSocialService{err: tt.err}, tt.user)
			req := httptest.NewRequest(http.MethodGet, "/api/social/profil/ben", nil)
			if tt.header != "" {
				req.Header.Set("Accept-Language", tt.header)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("body %s: %v", raw, err)
			}
			if resp.StatusCode != tt.status || body.Error.Code != tt.code || body.Error.Message != tt.wantMsg {
				t.Fatalf("got %d %q %q, want %d %q %q", resp.StatusCode, body.Error.Code, body.Error.Message, tt.status, tt.code, tt.wantMsg)
			}
			if got := resp.Header.Get("Content-Language"); got != tt.wantLang {
				t.Errorf("Content-Language = %q, want %q", got, tt.wantLang)
			}
			if got := resp.Header.Get("Vary"); got != "Accept-Language" {
				t.Errorf("Vary = %q", got)
			}
		})
	}
}
