package handler

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

func TestSocialHandler_StartCoverContract(t *testing.T) {
	s := &fakeSocialService{cover: &social.CoverStart{
		Source: social.SourceSummary{PublicationID: "y1", Title: "B", OwnerUsername: "ali"},
		Genre:  "pop", BPM: 120, MusicalKey: social.MusicalKey{Tonic: "A", Mode: "minor"}, DurationSec: 180,
		Structure: []social.StructureItem{}, Lyrics: social.Lyrics{Sections: []social.LyricsSection{}},
		Stems: []social.CoverStem{{ID: "drums", Instrument: "Davul", FileURL: "https://x/stems/u/k/drums.mp3", Duration: 3}},
	}}
	status, raw := doSocialRawRequest(t, newSocialApp(s, socialTestUser), http.MethodPost, "/api/social/yayinlar/y1/cover-baslat", "")
	want := `{"kaynak":{"yayinId":"y1","baslik":"B","sahipKullaniciAdi":"ali","silindi":false},"tur":"pop","bpm":120,` +
		`"ton":{"tonik":"A","mod":"minor"},"sureSn":180,"yapi":[],"sozler":{"bolumler":[]},` +
		`"stemler":[{"id":"drums","enstruman":"Davul","fileUrl":"https://x/stems/u/k/drums.mp3","sure":3}]}`
	if status != http.StatusOK || raw != want || s.lastID != "y1" {
		t.Fatalf("status=%d body=%s", status, raw)
	}
}

func TestSocialHandler_CoverErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{social.ErrBackingDisabled, http.StatusForbidden, "altyapi_kapali"},
		{social.ErrNotFound, http.StatusNotFound, "bulunamadi"},
		{social.ErrInvalidStatus, http.StatusConflict, "durum"},
	} {
		status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{err: tc.err}, socialTestUser), http.MethodPost,
			"/api/social/yayinlar/y1/cover-baslat", "")
		if status != tc.status || errorCode(body) != tc.code {
			t.Errorf("%v: status=%d body=%v", tc.err, status, body)
		}
	}
}

func TestSocialHandler_CoversPageAndLimit(t *testing.T) {
	s := &fakeSocialService{
		page:  &social.Page[social.PublicationSummary]{Items: []social.PublicationSummary{}},
		cover: &social.CoverStart{},
	}
	limiter := func(c *fiber.Ctx) error { return c.SendStatus(http.StatusTooManyRequests) }
	app := newSocialAppWithLimits(s, socialTestUser, SocialLimits{Publication: passThrough, Comment: passThrough, Cover: limiter})
	status, body := doSocialRequest(t, app, http.MethodGet, "/api/social/yayinlar/y1/coverlar?imlec=i", "")
	if status != http.StatusOK || s.lastCursor != "i" || s.lastID != "y1" {
		t.Fatalf("list: status=%d body=%v", status, body)
	}
	if v, present := body["sonrakiImlec"]; !present || v != nil {
		t.Fatalf("sonrakiImlec must be null: %v", body)
	}
	if status, _ := doSocialRawRequest(t, app, http.MethodPost, "/api/social/yayinlar/y1/cover-baslat", ""); status != http.StatusTooManyRequests {
		t.Fatalf("the cover limit must apply to cover-baslat: %d", status)
	}
}
