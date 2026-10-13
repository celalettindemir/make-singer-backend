package handler

import (
	"net/http"
	"testing"

	"github.com/makeasinger/api/internal/social"
)

func TestSocialHandler_FeedPageEnvelope(t *testing.T) {
	cursor := "sonraki"
	s := &fakeSocialService{page: &social.Page[social.PublicationSummary]{Items: []social.PublicationSummary{}, NextCursor: &cursor}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/akis?sekme=yeni&imlec=abc", "")
	if status != http.StatusOK || body["sonrakiImlec"] != "sonraki" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if _, isArray := body["ogeler"].([]any); !isArray {
		t.Fatalf("ogeler must be an array: %v", body)
	}
	if s.lastTab != "yeni" || s.lastCursor != "abc" || s.lastLimit != 20 {
		t.Fatalf("forwarded = tab %q cursor %q limit %d", s.lastTab, s.lastCursor, s.lastLimit)
	}
}

func TestSocialHandler_FeedLastPageCursorNull(t *testing.T) {
	s := &fakeSocialService{page: &social.Page[social.PublicationSummary]{Items: []social.PublicationSummary{}}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/akis?sekme=senin-icin&limit=999", "")
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if v, present := body["sonrakiImlec"]; !present || v != nil {
		t.Fatalf("sonrakiImlec must be present as null: %v", body)
	}
	if s.lastLimit != 50 {
		t.Fatalf("limit must be capped at 50: %d", s.lastLimit)
	}
}

func TestSocialHandler_FeedInvalidTab422(t *testing.T) {
	status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{err: social.ErrValidation}, socialTestUser), http.MethodGet, "/api/social/akis?sekme=x", "")
	if status != http.StatusUnprocessableEntity || errorCode(body) != "dogrulama" {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestSocialHandler_ProfilePublications(t *testing.T) {
	s := &fakeSocialService{page: &social.Page[social.PublicationSummary]{Items: []social.PublicationSummary{}}}
	status, _ := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/profiller/veli/yayinlar?tur=coverlar&limit=5", "")
	if status != http.StatusOK || s.lastUsername != "veli" || s.lastKind != "coverlar" || s.lastLimit != 5 {
		t.Fatalf("status=%d name=%q kind=%q limit=%d", status, s.lastUsername, s.lastKind, s.lastLimit)
	}
}

func TestSocialHandler_LikePutAndDelete(t *testing.T) {
	s := &fakeSocialService{like: &social.LikeStatus{LikedByMe: true, LikeCount: 3}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPut, "/api/social/yayinlar/y1/begeni", "")
	if status != http.StatusOK || body["begendimMi"] != true || body["begeniSayisi"] != float64(3) || !s.lastLike || s.lastID != "y1" {
		t.Fatalf("PUT status=%d body=%v", status, body)
	}
	s.like = &social.LikeStatus{LikeCount: 2}
	status, body = doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodDelete, "/api/social/yayinlar/y1/begeni", "")
	if status != http.StatusOK || body["begendimMi"] != false || s.lastLike {
		t.Fatalf("DELETE status=%d body=%v", status, body)
	}
	status, _ = doSocialRequest(t, newSocialApp(&fakeSocialService{err: social.ErrNotFound}, socialTestUser), http.MethodPut, "/api/social/yayinlar/y1/begeni", "")
	if status != http.StatusNotFound {
		t.Fatalf("an invisible publication must be 404: %d", status)
	}
}
