package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

func sampleDetail() *social.PublicationDetail {
	detail := &social.PublicationDetail{Status: social.StatusPublished, Visibility: social.VisibilityEveryone}
	detail.ID, detail.Slug, detail.Title = "y1", "Ab3dEf9h", "Gece"
	detail.Waveform = []float64{}
	detail.Structure = []social.StructureItem{}
	detail.Lyrics = social.Lyrics{Sections: []social.LyricsSection{}}
	detail.CreatedAt = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	return detail
}

func TestSocialHandler_CreatePublication202(t *testing.T) {
	s := &fakeSocialService{pubResponse: &social.PublicationResponse{ID: "y1", Slug: "Ab3dEf9h", Status: social.StatusPreparing}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPost, "/api/social/yayinlar",
		`{"baslik":"Gece","gorunurluk":"herkes","altyapiAcik":true,"masterUrl":"masters/u/p/m.wav","stemler":[{"id":"d","enstruman":"drums","url":"stems/u/p/d.mp3","sure":12.5}],"bpm":120,"sureSn":184,"ton":{"tonik":"A","mod":"minor"},"kaynakYayinId":null}`)
	if status != http.StatusAccepted || body["durum"] != "hazirlaniyor" || body["id"] != "y1" || body["slug"] != "Ab3dEf9h" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
	if s.lastRequest.Title != "Gece" || len(s.lastRequest.Stems) != 1 || s.lastRequest.Stems[0].Duration != 12.5 || s.lastRequest.SourcePublicationID != nil {
		t.Fatalf("istek = %+v", s.lastRequest)
	}
}

func TestSocialHandler_CreatePublicationErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{social.ErrNoProfile, http.StatusConflict, "profil_yok"},
		{social.ErrOwnership, http.StatusForbidden, "sahiplik"},
		{social.ErrSourceNotFound, http.StatusNotFound, "kaynak_bulunamadi"},
		{social.ErrBackingDisabled, http.StatusForbidden, "altyapi_kapali"},
		{social.ErrValidation, http.StatusUnprocessableEntity, "dogrulama"},
	}
	for _, tc := range cases {
		status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{err: tc.err}, socialTestUser), http.MethodPost, "/api/social/yayinlar", `{}`)
		if status != tc.status || errorCode(body) != tc.code {
			t.Errorf("%v: durum=%d govde=%v", tc.err, status, body)
		}
	}
}

func TestSocialHandler_CreatePublicationRateLimit(t *testing.T) {
	app := fiber.New()
	group := app.Group("/api/social", asUser(socialTestUser))
	NewSocialHandler(&fakeSocialService{}).Routes(group, SocialLimits{Publication: func(c *fiber.Ctx) error {
		return c.Status(http.StatusTooManyRequests).JSON(fiber.Map{"error": fiber.Map{"code": "RATE_LIMITED", "message": "x"}})
	}, Comment: passThrough, Cover: passThrough})
	if status, _ := doSocialRequest(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); status != http.StatusTooManyRequests {
		t.Fatalf("yayin limiti POST /yayinlar'a uygulanmali: %d", status)
	}
	if status, _ := doSocialRequest(t, app, http.MethodGet, "/api/social/yayinlar/y1", ""); status == http.StatusTooManyRequests {
		t.Fatal("yayin limiti okuma uclarina uygulanmamali")
	}
}

// PublicationDetail contract: gorunurluk is always present, kaynak as null,
// and dalgaFormu as an array.
func TestSocialHandler_PublicationDetailContractFields(t *testing.T) {
	s := &fakeSocialService{detail: sampleDetail()}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/yayinlar/y1", "")
	if status != http.StatusOK || s.lastID != "y1" {
		t.Fatalf("durum=%d", status)
	}
	for _, field := range []string{"id", "slug", "baslik", "sahip", "tur", "bpm", "ton", "sureSn", "dalgaFormu",
		"begeniSayisi", "yorumSayisi", "coverSayisi", "begendimMi", "altyapiAcik", "kaynak", "olusturma",
		"aciklama", "yorumAcik", "sozler", "yapi", "durum", "benimMi", "gorunurluk"} {
		if _, ok := body[field]; !ok {
			t.Errorf("%s alani yok", field)
		}
	}
	if body["kaynak"] != nil {
		t.Errorf("kaynak null olmali: %v", body["kaynak"])
	}
	if _, isArray := body["dalgaFormu"].([]any); !isArray {
		t.Errorf("dalgaFormu dizi olmali: %v", body["dalgaFormu"])
	}
	if body["olusturma"] != "2026-10-12T10:00:00Z" {
		t.Errorf("olusturma RFC3339 olmali: %v", body["olusturma"])
	}
	tone := body["ton"].(map[string]any)
	if _, ok := tone["tonik"]; !ok {
		t.Errorf("ton.tonik yok: %v", tone)
	}
}

func TestSocialHandler_SlugAndMissing404(t *testing.T) {
	s := &fakeSocialService{detail: sampleDetail()}
	if status, _ := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/s/Ab3dEf9h", ""); status != http.StatusOK || s.lastID != "Ab3dEf9h" {
		t.Fatalf("slug durum=%d son=%q", status, s.lastID)
	}
	status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{err: social.ErrNotFound}, socialTestUser), http.MethodGet, "/api/social/s/Ab3dEf9h", "")
	if status != http.StatusNotFound || errorCode(body) != "bulunamadi" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}

func TestSocialHandler_UpdatePublicationOnlySentFields(t *testing.T) {
	s := &fakeSocialService{detail: sampleDetail()}
	status, _ := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPatch, "/api/social/yayinlar/y1", `{"gorunurluk":"link"}`)
	if status != http.StatusOK {
		t.Fatalf("durum=%d", status)
	}
	if s.lastPatch.Visibility == nil || *s.lastPatch.Visibility != "link" || s.lastPatch.Description != nil || s.lastPatch.BackingEnabled != nil || s.lastPatch.CommentsEnabled != nil {
		t.Fatalf("yama = %+v", s.lastPatch)
	}
	status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{err: social.ErrNoStems}, socialTestUser), http.MethodPatch, "/api/social/yayinlar/y1", `{"altyapiAcik":true}`)
	if status != http.StatusUnprocessableEntity || errorCode(body) != "stem_yok" {
		t.Fatalf("stem_yok: durum=%d govde=%v", status, body)
	}
}

func TestSocialHandler_DeletePublication204And404(t *testing.T) {
	s := &fakeSocialService{}
	if status, _ := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodDelete, "/api/social/yayinlar/y1", ""); status != http.StatusNoContent || s.lastID != "y1" {
		t.Fatalf("durum=%d", status)
	}
	if status, _ := doSocialRequest(t, newSocialApp(&fakeSocialService{err: social.ErrNotFound}, socialTestUser), http.MethodDelete, "/api/social/yayinlar/y1", ""); status != http.StatusNotFound {
		t.Fatalf("durum=%d", status)
	}
}

func TestSocialHandler_Retry202And409(t *testing.T) {
	s := &fakeSocialService{pubResponse: &social.PublicationResponse{ID: "y1", Slug: "Ab3dEf9h", Status: social.StatusPreparing}}
	if status, _ := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPost, "/api/social/yayinlar/y1/tekrar-dene", ""); status != http.StatusAccepted {
		t.Fatalf("durum=%d", status)
	}
	status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{err: social.ErrInvalidStatus}, socialTestUser), http.MethodPost, "/api/social/yayinlar/y1/tekrar-dene", "")
	if status != http.StatusConflict || errorCode(body) != "durum" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}

func TestSocialHandler_FilesMasterURL(t *testing.T) {
	expiresAt := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	s := &fakeSocialService{files: &social.PublicationFiles{Master: social.FileLink{URL: "https://imzali/x", ExpiresAt: &expiresAt}}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPost, "/api/social/yayinlar/y1/dosyalar", "")
	master, _ := body["master"].(map[string]any)
	if status != http.StatusOK || master["url"] != "https://imzali/x" || master["expiresAt"] != "2026-10-12T11:00:00Z" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}
