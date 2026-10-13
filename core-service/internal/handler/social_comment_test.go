package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

func TestSocialHandler_PostComment201Contract(t *testing.T) {
	s := &fakeSocialService{comment: &social.Comment{
		ID: "y1", Author: social.ProfileSummary{Username: "ali", DisplayName: "Ali", AvatarColor: 1},
		Text: "guzel", CreatedAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), CanDelete: true,
	}}
	status, raw := doSocialRawRequest(t, newSocialApp(s, socialTestUser), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"guzel","zamanMs":null}`)
	want := `{"id":"y1","yazar":{"kullaniciAdi":"ali","gorunenAd":"Ali","avatarRengi":1},"metin":"guzel","zamanMs":null,"olusturma":"2026-10-12T09:00:00Z","silebilirMiyim":true}`
	if status != http.StatusCreated || raw != want {
		t.Fatalf("durum=%d govde=%s", status, raw)
	}
	if s.lastID != "abc" || s.lastComment.Text != "guzel" || s.lastComment.TimeMs != nil {
		t.Fatalf("iletilen = %q %+v", s.lastID, s.lastComment)
	}

	doSocialRawRequest(t, newSocialApp(s, socialTestUser), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"an","zamanMs":1500}`)
	if s.lastComment.TimeMs == nil || *s.lastComment.TimeMs != 1500 {
		t.Fatalf("zamanMs iletilmedi: %+v", s.lastComment)
	}
}

func TestSocialHandler_CommentErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{social.ErrCommentsDisabled, http.StatusForbidden, "yorum_kapali"},
		{social.ErrNoProfile, http.StatusConflict, "profil_yok"},
		{social.ErrNotFound, http.StatusNotFound, "bulunamadi"},
		{wrapValidation("metin"), http.StatusUnprocessableEntity, "dogrulama"},
	}
	for _, tc := range cases {
		s := &fakeSocialService{err: tc.err}
		status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPost,
			"/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":null}`)
		if status != tc.status || errorCode(body) != tc.code {
			t.Errorf("POST %v: durum=%d govde=%v", tc.err, status, body)
		}
	}
	s := &fakeSocialService{err: social.ErrCommentsDisabled}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/yayinlar/abc/yorumlar", "")
	if status != http.StatusForbidden || errorCode(body) != "yorum_kapali" {
		t.Fatalf("GET: durum=%d govde=%v", status, body)
	}
	status, body = doSocialRequest(t, newSocialApp(&fakeSocialService{}, socialTestUser), http.MethodPost,
		"/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":1.5}`)
	if status != http.StatusUnprocessableEntity || errorCode(body) != "dogrulama" {
		t.Fatalf("kesirli zamanMs: durum=%d govde=%v", status, body)
	}
}

func TestSocialHandler_CommentsPage(t *testing.T) {
	s := &fakeSocialService{commentPage: &social.Page[social.Comment]{Items: []social.Comment{}}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet,
		"/api/social/yayinlar/abc/yorumlar?imlec=xyz", "")
	if status != http.StatusOK || s.lastCursor != "xyz" || s.lastLimit != 20 || s.lastID != "abc" {
		t.Fatalf("durum=%d imlec=%q limit=%d", status, s.lastCursor, s.lastLimit)
	}
	if v, present := body["sonrakiImlec"]; !present || v != nil {
		t.Fatalf("sonrakiImlec null olmali: %v", body)
	}
}

func TestSocialHandler_DeleteComment(t *testing.T) {
	s := &fakeSocialService{}
	status, _ := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodDelete, "/api/social/yorumlar/y9", "")
	if status != http.StatusNoContent || s.lastID != "y9" {
		t.Fatalf("durum=%d id=%q", status, s.lastID)
	}
	s.err = social.ErrForbidden
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodDelete, "/api/social/yorumlar/y9", "")
	if status != http.StatusForbidden || errorCode(body) != "yetki" {
		t.Fatalf("durum=%d govde=%v", status, body)
	}
}

// The comment limit applies only to POST on the comments route.
func TestSocialHandler_CommentLimitOnlyOnPost(t *testing.T) {
	slowDown := func(c *fiber.Ctx) error {
		return response.Error(c, fiber.StatusTooManyRequests, "yavasla", "Biraz yavasla", nil)
	}
	s := &fakeSocialService{
		commentPage: &social.Page[social.Comment]{Items: []social.Comment{}},
		pubResponse: &social.PublicationResponse{},
	}
	app := newSocialAppWithLimits(s, socialTestUser, SocialLimits{Publication: passThrough, Comment: slowDown, Cover: passThrough})
	status, body := doSocialRequest(t, app, http.MethodPost, "/api/social/yayinlar/abc/yorumlar", `{"metin":"x","zamanMs":null}`)
	if status != http.StatusTooManyRequests || errorCode(body) != "yavasla" {
		t.Fatalf("POST yorum: durum=%d govde=%v", status, body)
	}
	if status, _ := doSocialRequest(t, app, http.MethodGet, "/api/social/yayinlar/abc/yorumlar", ""); status != http.StatusOK {
		t.Fatalf("GET yorumlar limitlenmemeli: %d", status)
	}
	if status, _ := doSocialRequest(t, app, http.MethodDelete, "/api/social/yorumlar/y1", ""); status != http.StatusNoContent {
		t.Fatalf("DELETE yorum limitlenmemeli: %d", status)
	}
	if status, _ := doSocialRequest(t, app, http.MethodPost, "/api/social/yayinlar", `{}`); status != http.StatusAccepted {
		t.Fatalf("yayin yorum limitine takilmamali: %d", status)
	}
}
