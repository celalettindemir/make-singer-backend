package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

func TestSocialHandler_FollowResponse(t *testing.T) {
	for _, tc := range []struct {
		method string
		follow bool
	}{{http.MethodPut, true}, {http.MethodDelete, false}} {
		s := &fakeSocialService{follow: &social.FollowStatus{Followers: 4, FollowedByMe: tc.follow}}
		status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), tc.method, "/api/social/profiller/ayse/takip", "")
		if status != http.StatusOK || body["takipci"] != float64(4) || body["takipEdiyorMuyum"] != tc.follow {
			t.Fatalf("%s: durum=%d govde=%v", tc.method, status, body)
		}
		if s.lastUsername != "ayse" || s.lastFollow != tc.follow || s.lastUser != socialTestUser {
			t.Fatalf("%s: iletilen ad=%q takip=%v", tc.method, s.lastUsername, s.lastFollow)
		}
	}
}

func TestSocialHandler_SelfFollow422(t *testing.T) {
	s := &fakeSocialService{err: social.ErrSelfFollow}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPut, "/api/social/profiller/ben/takip", "")
	if status != http.StatusUnprocessableEntity || errorCode(body) != "kendini_takip" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}

func TestSocialHandler_FollowMissingProfile404(t *testing.T) {
	s := &fakeSocialService{err: social.ErrNotFound}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodDelete, "/api/social/profiller/yok/takip", "")
	if status != http.StatusNotFound || errorCode(body) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}

func TestSocialHandler_SuggestionsArray(t *testing.T) {
	s := &fakeSocialService{suggestions: []social.ProfileSummary{{Username: "ayse", DisplayName: "Ayse", AvatarColor: 2}}}
	app := newSocialApp(s, socialTestUser)
	status, raw := doSocialRawRequest(t, app, http.MethodGet, "/api/social/oneriler", "")
	if status != http.StatusOK || raw != `[{"kullaniciAdi":"ayse","gorunenAd":"Ayse","avatarRengi":2}]` {
		t.Fatalf("durum=%d govde=%s", status, raw)
	}
	s.suggestions = []social.ProfileSummary{}
	if _, raw := doSocialRawRequest(t, app, http.MethodGet, "/api/social/oneriler", ""); raw != `[]` {
		t.Fatalf("bos oneri [] olmali: %s", raw)
	}
}

// doSocialRawRequest sends the request and returns the status code and the
// raw body (for endpoints that return arrays).
func doSocialRawRequest(t *testing.T, app *fiber.App, method, path, body string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}
