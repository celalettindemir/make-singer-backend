package social

import "time"

// ProfileSummary is contract §4.1. avatarRengi is not stored; it is derived
// from the user ID (AvatarColor).
type ProfileSummary struct {
	Username    string `json:"kullaniciAdi"`
	DisplayName string `json:"gorunenAd"`
	AvatarColor int    `json:"avatarRengi"`
}

// Stats are the counters on a profile. songs/covers count only publications
// that the viewer can see, that are published and not deleted.
type Stats struct {
	Songs     int `json:"sarki"`
	Followers int `json:"takipci"`
	Following int `json:"takip"`
	Covers    int `json:"cover"`
}

// Profile is contract §4.1.
type Profile struct {
	ProfileSummary
	Bio          string `json:"bio"`
	Stats        Stats  `json:"istatistik"`
	FollowedByMe bool   `json:"takipEdiyorMuyum"`
	IsMe         bool   `json:"benMiyim"`
}

// ProfileInput is the PUT /profil/ben body.
type ProfileInput struct {
	Username    string `json:"kullaniciAdi"`
	DisplayName string `json:"gorunenAd"`
	Bio         string `json:"bio"`
}

// UsernameAvailability is the GET /profil/kullanici-adi/uygun response.
// Suggestion is set when the name is taken and a free numeric suffix was
// found; otherwise it is null.
type UsernameAvailability struct {
	Available  bool    `json:"uygun"`
	Suggestion *string `json:"oneri"`
}

// ProfileRecord is a sosyal.profil row.
type ProfileRecord struct {
	UserID      string
	Username    string
	DisplayName string
	Bio         string
	CreatedAt   time.Time
}

// ProfileCounts are a profile's counters as seen by the viewer.
type ProfileCounts struct {
	Stats        Stats
	FollowedByMe bool
}
