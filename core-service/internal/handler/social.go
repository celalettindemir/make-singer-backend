package handler

import (
	"context"
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/i18n"
	"github.com/makeasinger/api/internal/social"
	"github.com/makeasinger/api/pkg/response"
)

// SocialService is the service surface used by the Discover endpoints
// (implemented by *social.Service). The interface lets handler tests swap the
// service for a hand-written fake.
type SocialService interface {
	MyProfile(ctx context.Context, userID string) (*social.Profile, error)
	SaveProfile(ctx context.Context, userID string, input social.ProfileInput) (*social.Profile, error)
	CheckUsername(ctx context.Context, userID, username string) (*social.UsernameAvailability, error)
	Profile(ctx context.Context, viewerID, username string) (*social.Profile, error)

	CreatePublication(ctx context.Context, userID string, req social.PublicationRequest) (*social.PublicationResponse, error)
	Publication(ctx context.Context, userID, publicationID string) (*social.PublicationDetail, error)
	PublicationBySlug(ctx context.Context, userID, slug string) (*social.PublicationDetail, error)
	UpdatePublication(ctx context.Context, userID, publicationID string, patch social.PublicationPatch) (*social.PublicationDetail, error)
	DeletePublication(ctx context.Context, userID, publicationID string) error
	Retry(ctx context.Context, userID, publicationID string) (*social.PublicationResponse, error)
	Files(ctx context.Context, userID, publicationID string) (*social.PublicationFiles, error)

	Feed(ctx context.Context, userID, tab, cursor string, limit int) (*social.Page[social.PublicationSummary], error)
	ProfilePublications(ctx context.Context, viewerID, username, kind, cursor string, limit int) (*social.Page[social.PublicationSummary], error)
	SetLike(ctx context.Context, userID, publicationID string, like bool) (*social.LikeStatus, error)

	SetFollow(ctx context.Context, userID, username string, follow bool) (*social.FollowStatus, error)
	Suggestions(ctx context.Context, userID string) ([]social.ProfileSummary, error)

	Comments(ctx context.Context, userID, publicationID, cursor string, limit int) (*social.Page[social.Comment], error)
	PostComment(ctx context.Context, userID, publicationID string, input social.CommentInput) (*social.Comment, error)
	DeleteComment(ctx context.Context, userID, commentID string) error

	Report(ctx context.Context, userID string, input social.ReportInput) error

	Covers(ctx context.Context, userID, publicationID, cursor string, limit int) (*social.Page[social.PublicationSummary], error)
	StartCover(ctx context.Context, userID, publicationID string) (*social.CoverStart, error)

	Preview(ctx context.Context, slug string) (*social.Preview, error)
}

var _ SocialService = (*social.Service)(nil)

// SocialHandler serves the endpoints under /api/social.
type SocialHandler struct {
	service SocialService
}

func NewSocialHandler(service SocialService) *SocialHandler {
	return &SocialHandler{service: service}
}

// SocialLimits holds the per-user limits applied to individual endpoints.
// All of them must be set.
type SocialLimits struct {
	Publication fiber.Handler // POST /yayinlar (hourly)
	Comment     fiber.Handler // POST /yayinlar/:id/yorumlar (per minute, 429 yavasla)
	Cover       fiber.Handler // POST /yayinlar/:id/cover-baslat (hourly)
}

// Routes binds the endpoints to the given group; each limit applies only to
// its own endpoint. A missing limiter panics at registration: Fiber does not
// check for a nil handler, so it would otherwise be a 500 on the first request.
func (h *SocialHandler) Routes(r fiber.Router, l SocialLimits) {
	if l.Publication == nil || l.Comment == nil || l.Cover == nil {
		panic("handler: SocialLimits is incomplete; the Publication, Comment and Cover limiters must all be set")
	}
	r.Get("/profil/ben", h.MyProfile)
	r.Put("/profil/ben", h.SaveProfile)
	r.Get("/profil/kullanici-adi/uygun", h.CheckUsername)
	r.Get("/profiller/:ad", h.Profile)
	r.Get("/profiller/:ad/yayinlar", h.ProfilePublications)
	r.Get("/akis", h.Feed)

	r.Post("/yayinlar", l.Publication, h.CreatePublication)
	r.Get("/yayinlar/:id", h.Publication)
	r.Patch("/yayinlar/:id", h.UpdatePublication)
	r.Delete("/yayinlar/:id", h.DeletePublication)
	r.Post("/yayinlar/:id/tekrar-dene", h.Retry)
	r.Post("/yayinlar/:id/dosyalar", h.Files)
	r.Get("/s/:slug", h.PublicationBySlug)
	r.Put("/yayinlar/:id/begeni", h.Like)
	r.Delete("/yayinlar/:id/begeni", h.Unlike)

	r.Put("/profiller/:ad/takip", h.Follow)
	r.Delete("/profiller/:ad/takip", h.Unfollow)
	r.Get("/oneriler", h.Suggestions)

	r.Get("/yayinlar/:id/yorumlar", h.Comments)
	r.Post("/yayinlar/:id/yorumlar", l.Comment, h.PostComment)
	r.Delete("/yorumlar/:id", h.DeleteComment)

	r.Post("/sikayetler", h.Report)

	r.Get("/yayinlar/:id/coverlar", h.Covers)
	r.Post("/yayinlar/:id/cover-baslat", l.Cover, h.StartCover)
}

// socialErrors maps service errors to the status and code of the contract.
// Order matters: the first match wins.
var socialErrors = []struct {
	err     error
	status  int
	code    string
	message i18n.Key
}{
	{social.ErrNotFound, fiber.StatusNotFound, "bulunamadi", i18n.KeySocialNotFound},
	{social.ErrNoProfile, fiber.StatusConflict, "profil_yok", i18n.KeySocialProfileMissing},
	{social.ErrInvalidUsername, fiber.StatusUnprocessableEntity, "gecersiz_kullanici_adi", i18n.KeySocialUsernameInvalid},
	{social.ErrUsernameTaken, fiber.StatusConflict, "kullanici_adi_alinmis", i18n.KeySocialUsernameTaken},
	{social.ErrValidation, fiber.StatusUnprocessableEntity, "dogrulama", i18n.KeySocialValidation},
	{social.ErrOwnership, fiber.StatusForbidden, "sahiplik", i18n.KeySocialOwnership},
	{social.ErrSourceNotFound, fiber.StatusNotFound, "kaynak_bulunamadi", i18n.KeySocialSourceNotFound},
	{social.ErrBackingDisabled, fiber.StatusForbidden, "altyapi_kapali", i18n.KeySocialBackingClosed},
	{social.ErrNoStems, fiber.StatusUnprocessableEntity, "stem_yok", i18n.KeySocialNoStems},
	{social.ErrInvalidStatus, fiber.StatusConflict, "durum", i18n.KeySocialBadStatus},
	{social.ErrSelfFollow, fiber.StatusUnprocessableEntity, "kendini_takip", i18n.KeySocialSelfFollow},
	{social.ErrCommentsDisabled, fiber.StatusForbidden, "yorum_kapali", i18n.KeySocialCommentsDisabled},
	{social.ErrForbidden, fiber.StatusForbidden, "yetki", i18n.KeySocialForbidden},
}

// socialError turns a service error into the contract body:
// {"error":{"code","message"}}. A field-level validation error carries its
// own message; the others take the message from the table. An
// unrecognized error becomes 500 sunucu_hatasi and its detail is only
// written to the log.
func socialError(c *fiber.Ctx, err error) error {
	if errors.Is(err, social.ErrInvalidIdentity) {
		return response.Unauthorized(c, i18n.KeyUnauthenticated)
	}
	var v *social.ValidationError
	if errors.As(err, &v) {
		return response.Error(c, fiber.StatusUnprocessableEntity, "dogrulama", v.Key, nil, v.Args...)
	}
	for _, e := range socialErrors {
		if errors.Is(err, e.err) {
			return response.Error(c, e.status, e.code, e.message, nil)
		}
	}
	log.Printf("social: %s %s: %v", c.Method(), c.Route().Path, err)
	return response.Error(c, fiber.StatusInternalServerError, "sunucu_hatasi", i18n.KeyInternal, nil)
}

// badBodyError answers an unparseable JSON body: on the social endpoints a
// validation error is 422 dogrulama, not 400.
func badBodyError(c *fiber.Ctx) error {
	return response.Error(c, fiber.StatusUnprocessableEntity, "dogrulama", i18n.KeyInvalidBody, nil)
}
