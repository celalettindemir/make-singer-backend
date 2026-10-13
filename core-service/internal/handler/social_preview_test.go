package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// newPublicApp mimics the order in main: the unauthenticated endpoint first,
// then an /api group that cuts every request with 401 (in place of auth).
func newPublicApp(s *fakeSocialService, ipLimiter fiber.Handler) *fiber.App {
	app := fiber.New()
	h := NewSocialHandler(s)
	h.PublicRoutes(app.Group("/api/public"), ipLimiter)
	api := app.Group("/api", func(c *fiber.Ctx) error { return response.Unauthorized(c, i18n.KeyUnauthenticated) })
	h.Routes(api.Group("/social"), SocialLimits{Publication: passThrough, Comment: passThrough, Cover: passThrough})
	return app
}

func TestSocialHandler_PreviewUnauthenticatedContract(t *testing.T) {
	s := &fakeSocialService{preview: &social.Preview{
		Title: "Sarki", Owner: social.ProfileSummary{Username: "ali", DisplayName: "Ali", AvatarColor: 5},
		DurationSec: 180, Waveform: []float64{0.5, 1}, Genre: "pop",
	}}
	app := newPublicApp(s, passThrough)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/public/s/Ab3dEf9h", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	want := `{"baslik":"Sarki","sahip":{"kullaniciAdi":"ali","gorunenAd":"Ali","avatarRengi":5},"sureSn":180,"dalgaFormu":[0.5,1],"onizlemeUrl":null,"tur":"pop"}`
	if resp.StatusCode != http.StatusOK || string(raw) != want {
		t.Fatalf("durum=%d govde=%s", resp.StatusCode, raw)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || s.lastID != "Ab3dEf9h" {
		t.Fatalf("Cache-Control=%q slug=%q", resp.Header.Get("Cache-Control"), s.lastID)
	}
	// Endpoints that require authentication in the same app still return 401.
	if status, _ := doSocialRequest(t, app, http.MethodGet, "/api/social/s/Ab3dEf9h", ""); status != http.StatusUnauthorized {
		t.Fatalf("auth grubu atlanmamali: %d", status)
	}
}

func TestSocialHandler_Preview404JSON(t *testing.T) {
	status, body := doSocialRequest(t, newPublicApp(&fakeSocialService{err: social.ErrNotFound}, passThrough),
		http.MethodGet, "/api/public/s/Yok12345", "")
	if status != http.StatusNotFound || errorCode(body) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}

func TestSocialHandler_PreviewIPLimitApplied(t *testing.T) {
	limiter := func(c *fiber.Ctx) error { return response.RateLimited(c) }
	status, body := doSocialRequest(t, newPublicApp(&fakeSocialService{preview: &social.Preview{}}, limiter),
		http.MethodGet, "/api/public/s/Ab3dEf9h", "")
	if status != http.StatusTooManyRequests || errorCode(body) != "RATE_LIMITED" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}

// Cache-Control: no-store applies not only to 200; the 404 and the limiter's
// 429 must not be cached by a proxy/CDN either.
func TestSocialHandler_PreviewNoStoreOnEveryResponse(t *testing.T) {
	limiter := func(c *fiber.Ctx) error { return response.RateLimited(c) }
	cases := []struct {
		name string
		app  *fiber.App
		want int
	}{
		{"404", newPublicApp(&fakeSocialService{err: social.ErrNotFound}, passThrough), http.StatusNotFound},
		{"429", newPublicApp(&fakeSocialService{preview: &social.Preview{}}, limiter), http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tc.app.Test(httptest.NewRequest(http.MethodGet, "/api/public/s/Ab3dEf9h", nil))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want || resp.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("durum=%d Cache-Control=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
			}
		})
	}
}

// expectPanic verifies that f panics.
func expectPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("eksik limiter ile kayit panik etmeliydi")
		}
	}()
	f()
}

// P1: a forgotten limiter must blow up at registration, not on the first request.
func TestSocialHandler_MissingLimiterPanicsAtRegistration(t *testing.T) {
	h := NewSocialHandler(&fakeSocialService{})
	cases := map[string]SocialLimits{
		"hepsi-bos": {},
		"yayin-bos": {Comment: passThrough, Cover: passThrough},
		"yorum-bos": {Publication: passThrough, Cover: passThrough},
		"cover-bos": {Publication: passThrough, Comment: passThrough},
	}
	for name, l := range cases {
		t.Run(name, func(t *testing.T) {
			expectPanic(t, func() { h.Routes(fiber.New().Group("/api/social"), l) })
		})
	}
	t.Run("ip-limiti-bos", func(t *testing.T) {
		expectPanic(t, func() { h.PublicRoutes(fiber.New().Group("/api/public"), nil) })
	})
}
