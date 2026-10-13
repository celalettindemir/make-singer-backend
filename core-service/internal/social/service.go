package social

import (
	"context"
	"errors"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/client"
	"github.com/makeasinger/api/internal/config"
)

// Enqueuer is the part of *asynq.Client that is used here.
type Enqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// Service applies the business rules of the Discover endpoints. Visibility
// is applied here for details (CanSeeDetail) and in the store for lists
// (visibilityCondition).
type Service struct {
	store   Store
	queue   Enqueuer
	storage client.StorageClient // nil: no R2, unsigned addresses are produced
	r2      *config.R2Config
	now     func() time.Time

	// copier is the stem copier of cover-baslat (WithCopier);
	// nil: no R2, the copy is skipped.
	copier client.Copier
}

// NewService builds the service. storage may be nil (R2 not configured);
// main passes only the real client to avoid the typed-nil trap.
func NewService(store Store, queue Enqueuer, storage client.StorageClient, r2 *config.R2Config) *Service {
	return &Service{store: store, queue: queue, storage: storage, r2: r2, now: time.Now}
}

// parseUserID validates the caller's identity. The auth middleware always
// writes a uuid; anything else is a configuration error and returns 401.
func parseUserID(raw string) (string, error) {
	id, ok := parseID(raw)
	if !ok {
		return "", ErrInvalidIdentity
	}
	return id, nil
}

// summary turns the record into a ProfileSummary.
func (r *ProfileRecord) summary() ProfileSummary {
	return ProfileSummary{
		Username:    r.Username,
		DisplayName: r.DisplayName,
		AvatarColor: AvatarColor(r.UserID),
	}
}

// buildProfile turns the record into a full Profile for the viewer.
func (s *Service) buildProfile(ctx context.Context, rec *ProfileRecord, viewerID string) (*Profile, error) {
	counts, err := s.store.ProfileCounts(ctx, rec.UserID, viewerID)
	if err != nil {
		return nil, err
	}
	return &Profile{
		ProfileSummary: rec.summary(),
		Bio:            rec.Bio,
		Stats:          counts.Stats,
		FollowedByMe:   counts.FollowedByMe,
		IsMe:           rec.UserID == viewerID,
	}, nil
}

// MyProfile returns the caller's profile, or ErrNoProfile if there is none.
func (s *Service) MyProfile(ctx context.Context, userID string) (*Profile, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	rec, err := s.store.GetProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.buildProfile(ctx, rec, id)
}

// SaveProfile creates or updates the profile (PUT /profil/ben).
func (s *Service) SaveProfile(ctx context.Context, userID string, input ProfileInput) (*Profile, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	input, err = normalizeProfileInput(input)
	if err != nil {
		return nil, err
	}
	rec, err := s.store.UpsertProfile(ctx, ProfileRecord{
		UserID: id, Username: input.Username, DisplayName: input.DisplayName, Bio: input.Bio,
	})
	if err != nil {
		return nil, err
	}
	return s.buildProfile(ctx, rec, id)
}

// CheckUsername reports whether the name is available to the caller. Their
// own name is available. If it is taken, it suggests the first free one among
// name2..name99.
func (s *Service) CheckUsername(ctx context.Context, userID, username string) (*UsernameAvailability, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	if !ValidUsername(username) {
		return nil, ErrInvalidUsername
	}
	owners, err := s.store.UsernameOwners(ctx, []string{username})
	if err != nil {
		return nil, err
	}
	if owner, taken := owners[username]; !taken || owner == id {
		return &UsernameAvailability{Available: true}, nil
	}
	candidates := usernameCandidates(username)
	owners, err = s.store.UsernameOwners(ctx, candidates)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if owner, taken := owners[candidate]; !taken || owner == id {
			return &UsernameAvailability{Available: false, Suggestion: &candidate}, nil
		}
	}
	return &UsernameAvailability{Available: false}, nil
}

// Profile returns a profile by username as the viewer sees it. A malformed
// name is ErrNotFound: no such profile can exist.
func (s *Service) Profile(ctx context.Context, viewerID, username string) (*Profile, error) {
	id, err := parseUserID(viewerID)
	if err != nil {
		return nil, err
	}
	if !ValidUsername(username) {
		return nil, ErrNotFound
	}
	rec, err := s.store.GetProfileByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNoProfile) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s.buildProfile(ctx, rec, id)
}
