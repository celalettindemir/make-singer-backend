package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/social"
)

const socialTestUser = "6f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"

// fakeSocialService is a hand-written fake of SocialService. Each field sets
// what the matching method returns; the inputs of the last call are recorded.
type fakeSocialService struct {
	err error

	profile      *social.Profile
	availability *social.UsernameAvailability
	pubResponse  *social.PublicationResponse
	detail       *social.PublicationDetail
	files        *social.PublicationFiles
	page         *social.Page[social.PublicationSummary]
	like         *social.LikeStatus
	follow       *social.FollowStatus
	suggestions  []social.ProfileSummary
	comment      *social.Comment
	commentPage  *social.Page[social.Comment]
	cover        *social.CoverStart
	preview      *social.Preview

	lastUser     string
	lastUsername string
	lastProfile  social.ProfileInput
	lastID       string
	lastRequest  social.PublicationRequest
	lastPatch    social.PublicationPatch
	lastTab      string
	lastKind     string
	lastCursor   string
	lastLimit    int
	lastLike     bool
	lastFollow   bool
	lastComment  social.CommentInput
	lastReport   social.ReportInput
}

func (s *fakeSocialService) MyProfile(_ context.Context, userID string) (*social.Profile, error) {
	s.lastUser = userID
	return s.profile, s.err
}

func (s *fakeSocialService) SaveProfile(_ context.Context, userID string, input social.ProfileInput) (*social.Profile, error) {
	s.lastUser, s.lastProfile = userID, input
	return s.profile, s.err
}

func (s *fakeSocialService) CheckUsername(_ context.Context, userID, username string) (*social.UsernameAvailability, error) {
	s.lastUser, s.lastUsername = userID, username
	return s.availability, s.err
}

func (s *fakeSocialService) Profile(_ context.Context, userID, username string) (*social.Profile, error) {
	s.lastUser, s.lastUsername = userID, username
	return s.profile, s.err
}

func (s *fakeSocialService) CreatePublication(_ context.Context, userID string, req social.PublicationRequest) (*social.PublicationResponse, error) {
	s.lastUser, s.lastRequest = userID, req
	return s.pubResponse, s.err
}

func (s *fakeSocialService) Publication(_ context.Context, userID, id string) (*social.PublicationDetail, error) {
	s.lastUser, s.lastID = userID, id
	return s.detail, s.err
}

func (s *fakeSocialService) PublicationBySlug(_ context.Context, userID, slug string) (*social.PublicationDetail, error) {
	s.lastUser, s.lastID = userID, slug
	return s.detail, s.err
}

func (s *fakeSocialService) UpdatePublication(_ context.Context, userID, id string, patch social.PublicationPatch) (*social.PublicationDetail, error) {
	s.lastUser, s.lastID, s.lastPatch = userID, id, patch
	return s.detail, s.err
}

func (s *fakeSocialService) DeletePublication(_ context.Context, userID, id string) error {
	s.lastUser, s.lastID = userID, id
	return s.err
}

func (s *fakeSocialService) Retry(_ context.Context, userID, id string) (*social.PublicationResponse, error) {
	s.lastUser, s.lastID = userID, id
	return s.pubResponse, s.err
}

func (s *fakeSocialService) Files(_ context.Context, userID, id string) (*social.PublicationFiles, error) {
	s.lastUser, s.lastID = userID, id
	return s.files, s.err
}

func (s *fakeSocialService) Feed(_ context.Context, userID, tab, cursor string, limit int) (*social.Page[social.PublicationSummary], error) {
	s.lastUser, s.lastTab, s.lastCursor, s.lastLimit = userID, tab, cursor, limit
	return s.page, s.err
}

func (s *fakeSocialService) ProfilePublications(_ context.Context, userID, username, kind, cursor string, limit int) (*social.Page[social.PublicationSummary], error) {
	s.lastUser, s.lastUsername, s.lastKind, s.lastCursor, s.lastLimit = userID, username, kind, cursor, limit
	return s.page, s.err
}

func (s *fakeSocialService) SetLike(_ context.Context, userID, id string, like bool) (*social.LikeStatus, error) {
	s.lastUser, s.lastID, s.lastLike = userID, id, like
	return s.like, s.err
}

func (s *fakeSocialService) SetFollow(_ context.Context, userID, username string, follow bool) (*social.FollowStatus, error) {
	s.lastUser, s.lastUsername, s.lastFollow = userID, username, follow
	return s.follow, s.err
}

func (s *fakeSocialService) Suggestions(_ context.Context, userID string) ([]social.ProfileSummary, error) {
	s.lastUser = userID
	return s.suggestions, s.err
}

func (s *fakeSocialService) Comments(_ context.Context, userID, id, cursor string, limit int) (*social.Page[social.Comment], error) {
	s.lastUser, s.lastID, s.lastCursor, s.lastLimit = userID, id, cursor, limit
	return s.commentPage, s.err
}

func (s *fakeSocialService) PostComment(_ context.Context, userID, id string, input social.CommentInput) (*social.Comment, error) {
	s.lastUser, s.lastID, s.lastComment = userID, id, input
	return s.comment, s.err
}

func (s *fakeSocialService) DeleteComment(_ context.Context, userID, id string) error {
	s.lastUser, s.lastID = userID, id
	return s.err
}

func (s *fakeSocialService) Report(_ context.Context, userID string, input social.ReportInput) error {
	s.lastUser, s.lastReport = userID, input
	return s.err
}

func (s *fakeSocialService) Covers(_ context.Context, userID, id, cursor string, limit int) (*social.Page[social.PublicationSummary], error) {
	s.lastUser, s.lastID, s.lastCursor, s.lastLimit = userID, id, cursor, limit
	return s.page, s.err
}

func (s *fakeSocialService) StartCover(_ context.Context, userID, id string) (*social.CoverStart, error) {
	s.lastUser, s.lastID = userID, id
	return s.cover, s.err
}

func (s *fakeSocialService) Preview(_ context.Context, slug string) (*social.Preview, error) {
	s.lastID = slug
	return s.preview, s.err
}

func passThrough(c *fiber.Ctx) error { return c.Next() }

// newSocialApp builds the /api/social group with the fake service and
// pass-through limits.
func newSocialApp(s *fakeSocialService, userID string) *fiber.App {
	return newSocialAppWithLimits(s, userID, SocialLimits{Publication: passThrough, Comment: passThrough, Cover: passThrough})
}

// newSocialAppWithLimits builds it with the given limits.
func newSocialAppWithLimits(s *fakeSocialService, userID string, l SocialLimits) *fiber.App {
	app := fiber.New()
	group := app.Group("/api/social", asUser(userID))
	NewSocialHandler(s).Routes(group, l)
	return app
}

// doSocialRequest sends the request and returns the status code and the decoded JSON body.
func doSocialRequest(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any) {
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
		t.Fatalf("request: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("body is not JSON (%d): %s", resp.StatusCode, raw)
		}
	}
	return resp.StatusCode, out
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestSocialHandler_MyProfileMissing404NoProfile(t *testing.T) {
	s := &fakeSocialService{err: social.ErrNoProfile}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/profil/ben", "")
	if status != http.StatusNotFound || errorCode(body) != "profil_yok" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if s.lastUser != socialTestUser {
		t.Fatalf("user was not forwarded: %q", s.lastUser)
	}
}

func TestSocialHandler_MyProfileContractFields(t *testing.T) {
	s := &fakeSocialService{profile: &social.Profile{
		ProfileSummary: social.ProfileSummary{Username: "ali", DisplayName: "Ali", AvatarColor: 3},
		Bio:            "b", IsMe: true,
	}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/profil/ben", "")
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	for _, field := range []string{"kullaniciAdi", "gorunenAd", "avatarRengi", "bio", "istatistik", "takipEdiyorMuyum", "benMiyim"} {
		if _, ok := body[field]; !ok {
			t.Errorf("field %s is missing: %v", field, body)
		}
	}
	stats := body["istatistik"].(map[string]any)
	for _, field := range []string{"sarki", "takipci", "takip", "cover"} {
		if _, ok := stats[field]; !ok {
			t.Errorf("istatistik.%s is missing", field)
		}
	}
}

func TestSocialHandler_SaveProfileErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{social.ErrUsernameTaken, http.StatusConflict, "kullanici_adi_alinmis"},
		{social.ErrInvalidUsername, http.StatusUnprocessableEntity, "gecersiz_kullanici_adi"},
		{wrapValidation("gorunenAd"), http.StatusUnprocessableEntity, "dogrulama"},
		{errors.New("pg is down"), http.StatusInternalServerError, "sunucu_hatasi"},
	}
	for _, tc := range cases {
		s := &fakeSocialService{err: tc.err}
		status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPut, "/api/social/profil/ben",
			`{"kullaniciAdi":"ali","gorunenAd":"Ali","bio":""}`)
		if status != tc.status || errorCode(body) != tc.code {
			t.Errorf("%v: status=%d body=%v", tc.err, status, body)
		}
	}
}

func wrapValidation(field string) error { return errors.Join(social.ErrValidation, errors.New(field)) }

func TestSocialHandler_SaveProfileForwardsBody(t *testing.T) {
	s := &fakeSocialService{profile: &social.Profile{}}
	status, _ := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodPut, "/api/social/profil/ben",
		`{"kullaniciAdi":"ali","gorunenAd":"Ali","bio":"selam"}`)
	if status != http.StatusOK || s.lastProfile != (social.ProfileInput{Username: "ali", DisplayName: "Ali", Bio: "selam"}) {
		t.Fatalf("status=%d input=%+v", status, s.lastProfile)
	}
}

func TestSocialHandler_MalformedBody422(t *testing.T) {
	status, body := doSocialRequest(t, newSocialApp(&fakeSocialService{}, socialTestUser), http.MethodPut, "/api/social/profil/ben", `{"kullaniciAdi":`)
	if status != http.StatusUnprocessableEntity || errorCode(body) != "dogrulama" {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestSocialHandler_CheckUsernameSuggestionNullOrSet(t *testing.T) {
	s := &fakeSocialService{availability: &social.UsernameAvailability{Available: true}}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/profil/kullanici-adi/uygun?ad=ali", "")
	if status != http.StatusOK || body["uygun"] != true || s.lastUsername != "ali" {
		t.Fatalf("status=%d body=%v name=%q", status, body, s.lastUsername)
	}
	if suggestion, present := body["oneri"]; !present || suggestion != nil {
		t.Fatalf("the oneri field must be present as null: %v", body)
	}
}

func TestSocialHandler_ProfileMissing404NotFound(t *testing.T) {
	s := &fakeSocialService{err: social.ErrNotFound}
	status, body := doSocialRequest(t, newSocialApp(s, socialTestUser), http.MethodGet, "/api/social/profiller/yok", "")
	if status != http.StatusNotFound || errorCode(body) != "bulunamadi" || s.lastUsername != "yok" {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

func TestSocialHandler_Unauthenticated401(t *testing.T) {
	status, _ := doSocialRequest(t, newSocialApp(&fakeSocialService{}, ""), http.MethodGet, "/api/social/profil/ben", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d", status)
	}
}
