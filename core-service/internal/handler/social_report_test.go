package handler

import (
	"net/http"
	"testing"

	"github.com/makeasinger/api/internal/social"
)

func TestSocialHandler_Report201NoBody(t *testing.T) {
	s := &fakeSocialService{}
	status, raw := doSocialRawRequest(t, newSocialApp(s, socialTestUser), http.MethodPost, "/api/social/sikayetler",
		`{"hedefTuru":"yorum","hedefId":"c1","neden":"spam","aciklama":"x"}`)
	if status != http.StatusCreated || raw != "" {
		t.Fatalf("status=%d body=%q", status, raw)
	}
	if s.lastReport != (social.ReportInput{TargetType: "yorum", TargetID: "c1", Reason: "spam", Description: "x"}) {
		t.Fatalf("forwarded = %+v", s.lastReport)
	}
	// aciklama is optional.
	if status, _ := doSocialRawRequest(t, newSocialApp(s, socialTestUser), http.MethodPost, "/api/social/sikayetler",
		`{"hedefTuru":"yayin","hedefId":"y1","neden":"diger"}`); status != http.StatusCreated || s.lastReport.Description != "" {
		t.Fatalf("without a description: status=%d %+v", status, s.lastReport)
	}
}

func TestSocialHandler_ReportErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{social.ErrNotFound, http.StatusNotFound, "bulunamadi"},
		{wrapValidation("neden"), http.StatusUnprocessableEntity, "dogrulama"},
	} {
		status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{err: tc.err}, socialTestUser), http.MethodPost,
			"/api/social/sikayetler", `{"hedefTuru":"yayin","hedefId":"y1","neden":"spam"}`)
		if status != tc.status || errorCode(body) != tc.code {
			t.Errorf("%v: status=%d body=%v", tc.err, status, body)
		}
	}
}
