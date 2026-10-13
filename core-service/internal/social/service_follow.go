package social

import "context"

// suggestionLimit is the maximum number of profiles GET /oneriler returns (spec §4.4).
const suggestionLimit = 10

// FollowStatus is the PUT/DELETE /profiller/{ad}/takip response.
type FollowStatus struct {
	Followers    int  `json:"takipci"`
	FollowedByMe bool `json:"takipEdiyorMuyum"`
}

// SetFollow is PUT (follow=true) and DELETE (false) /profiller/{ad}/takip.
// Repeating is safe. Following (or unfollowing) oneself is ErrSelfFollow; a
// missing or malformed name is ErrNotFound. The follower needs no profile
// (like liking).
func (s *Service) SetFollow(ctx context.Context, userID, username string, follow bool) (*FollowStatus, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	if !ValidUsername(username) {
		return nil, ErrNotFound
	}
	p, err := s.store.GetProfileByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if p.UserID == id {
		return nil, ErrSelfFollow
	}
	if follow {
		err = s.store.Follow(ctx, id, p.UserID)
	} else {
		err = s.store.Unfollow(ctx, id, p.UserID)
	}
	if err != nil {
		return nil, err
	}
	return s.store.FollowStatus(ctx, id, p.UserID)
}

// Suggestions is GET /oneriler: at most 10 ProfileSummary items, unpaged. An
// empty list is returned as [], not null.
func (s *Service) Suggestions(ctx context.Context, userID string) ([]ProfileSummary, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return nil, err
	}
	records, err := s.store.Suggestions(ctx, id, suggestionLimit)
	if err != nil {
		return nil, err
	}
	summaries := make([]ProfileSummary, 0, len(records))
	for i := range records {
		summaries = append(summaries, records[i].summary())
	}
	return summaries, nil
}
