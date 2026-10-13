package social

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"strconv"
	"time"
)

const (
	defaultPageSize = 20
	maxPageSize     = 50
)

// Page is the contract's list envelope. NextCursor is VISIBLE as null on the
// last page; Items is never null.
type Page[T any] struct {
	Items      []T     `json:"ogeler"`
	NextCursor *string `json:"sonrakiImlec"`
}

// PageSize interprets ?limit=: 20 when empty or invalid, at most 50. Mobile
// sends no limit; web may.
func PageSize(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return defaultPageSize
	}
	if n > maxPageSize {
		return maxPageSize
	}
	return n
}

// TimeCursor is the position in lists ordered by time, descending.
type TimeCursor struct {
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

// ScoreCursor is the position in the "for you" feed. RefMs (json "n") is the
// first page's reference time (Unix ms); later pages are computed with the
// same value so scores do not drift between pages.
type ScoreCursor struct {
	Score float64 `json:"p"`
	ID    string  `json:"id"`
	RefMs int64   `json:"n"`
}

func encodeCursor(v any) *string {
	b, _ := json.Marshal(v)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeCursor(raw string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, v) != nil {
		return validationError("imlec gecersiz")
	}
	return nil
}

// decodeTimeCursor returns nil for an empty string.
func decodeTimeCursor(raw string) (*TimeCursor, error) {
	if raw == "" {
		return nil, nil
	}
	var cur TimeCursor
	if err := decodeCursor(raw, &cur); err != nil {
		return nil, err
	}
	id, ok := parseID(cur.ID)
	if !ok || cur.T.IsZero() {
		return nil, validationError("imlec gecersiz")
	}
	cur.ID = id
	return &cur, nil
}

// decodeScoreCursor returns nil for an empty string.
func decodeScoreCursor(raw string) (*ScoreCursor, error) {
	if raw == "" {
		return nil, nil
	}
	var cur ScoreCursor
	if err := decodeCursor(raw, &cur); err != nil {
		return nil, err
	}
	id, ok := parseID(cur.ID)
	if !ok || cur.RefMs <= 0 {
		return nil, validationError("imlec gecersiz")
	}
	cur.ID = id
	return &cur, nil
}

// CommentCursor is the position in the comment list. The list has two
// groups: timed comments first (Group=0; time_ms, created, id ascending),
// then untimed ones (Group=1; created, id descending). TimeMs is set only
// when Group=0.
type CommentCursor struct {
	Group  int       `json:"g"`
	TimeMs *int      `json:"z,omitempty"`
	T      time.Time `json:"t"`
	ID     string    `json:"id"`
}

// decodeCommentCursor returns nil for an empty string. z must fit the
// time_ms (integer) column; a value above MaxInt32 is a 422 here instead of
// a 500 in the query.
func decodeCommentCursor(raw string) (*CommentCursor, error) {
	if raw == "" {
		return nil, nil
	}
	var cur CommentCursor
	if err := decodeCursor(raw, &cur); err != nil {
		return nil, err
	}
	id, ok := parseID(cur.ID)
	valid := ok && !cur.T.IsZero() &&
		((cur.Group == 0 && cur.TimeMs != nil && *cur.TimeMs >= 0 && *cur.TimeMs <= math.MaxInt32) || (cur.Group == 1 && cur.TimeMs == nil))
	if !valid {
		return nil, validationError("imlec gecersiz")
	}
	cur.ID = id
	return &cur, nil
}

// commentCursorAfter builds the cursor of the page after the page's last comment.
func commentCursorAfter(last Comment) *string {
	cur := CommentCursor{Group: 1, T: last.CreatedAt, ID: last.ID}
	if last.TimeMs != nil {
		cur.Group, cur.TimeMs = 0, last.TimeMs
	}
	return encodeCursor(cur)
}
