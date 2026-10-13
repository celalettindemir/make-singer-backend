package social

import "time"

// Comment is contract §4.1. TimeMs is VISIBLE as null on an untimed comment.
type Comment struct {
	ID        string         `json:"id"`
	Author    ProfileSummary `json:"yazar"`
	Text      string         `json:"metin"`
	TimeMs    *int           `json:"zamanMs"`
	CreatedAt time.Time      `json:"olusturma"`
	CanDelete bool           `json:"silebilirMiyim"`
}

// CommentInput is the POST /yayinlar/{id}/yorumlar body.
type CommentInput struct {
	Text   string `json:"metin"`
	TimeMs *int   `json:"zamanMs"`
}

// CommentInsert is a validated comment to be written to the store.
type CommentInsert struct {
	ID            string
	PublicationID string
	AuthorID      string
	Text          string
	TimeMs        *int
}

// CommentRecord is the row that the delete decision needs.
type CommentRecord struct {
	ID                 string
	PublicationID      string
	AuthorID           string
	PublicationOwnerID string
	Deleted            bool
}
