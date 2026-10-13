package social

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestPageSize(t *testing.T) {
	cases := map[string]int{"": 20, "x": 20, "0": 20, "-3": 20, "1": 1, "35": 35, "50": 50, "51": 50, "1000": 50}
	for raw, want := range cases {
		if got := PageSize(raw); got != want {
			t.Errorf("SayfaBoyutu(%q) = %d, beklenen %d", raw, got, want)
		}
	}
}

func TestTimeCursor_RoundTrip(t *testing.T) {
	instant := time.Date(2026, 10, 12, 9, 30, 15, 123456000, time.UTC)
	raw := encodeCursor(TimeCursor{T: instant, ID: pubX})
	cur, err := decodeTimeCursor(*raw)
	if err != nil || !cur.T.Equal(instant) || cur.ID != pubX {
		t.Fatalf("imlec = %+v, %v", cur, err)
	}
	if cur, err := decodeTimeCursor(""); cur != nil || err != nil {
		t.Fatalf("bos imlec nil olmali: %+v %v", cur, err)
	}
}

func TestScoreCursor_RoundTrip(t *testing.T) {
	raw := encodeCursor(ScoreCursor{Score: 0.1234567890123, ID: pubX, RefMs: 1760000000000})
	cur, err := decodeScoreCursor(*raw)
	if err != nil || cur.Score != 0.1234567890123 || cur.RefMs != 1760000000000 {
		t.Fatalf("imlec = %+v, %v", cur, err)
	}
}

func TestCursor_MalformedRejected(t *testing.T) {
	malformed := []string{"!!!", "e30", *encodeCursor(map[string]string{"id": "x"}), *encodeCursor(TimeCursor{ID: pubX})}
	for _, raw := range malformed {
		if _, err := decodeTimeCursor(raw); !errors.Is(err, ErrValidation) {
			t.Errorf("zaman %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"!!!", *encodeCursor(ScoreCursor{ID: pubX})} {
		if _, err := decodeScoreCursor(raw); !errors.Is(err, ErrValidation) {
			t.Errorf("puan %q: %v", raw, err)
		}
	}
}

// The time_ms column is an integer ($4::int); a value above MaxInt32 would be a
// pgx 500, so it must be 422. The limit itself is valid.
func TestCommentCursor_ZInt32Limit(t *testing.T) {
	instant := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if _, err := decodeCommentCursor(*encodeCursor(CommentCursor{Group: 0, TimeMs: intp(math.MaxInt32), T: instant, ID: comment1})); err != nil {
		t.Fatalf("MaxInt32 gecerli olmali: %v", err)
	}
	if _, err := decodeCommentCursor(*encodeCursor(CommentCursor{Group: 0, TimeMs: intp(math.MaxInt32 + 1), T: instant, ID: comment1})); !errors.Is(err, ErrValidation) {
		t.Fatalf("MaxInt32+1: %v, beklenen ErrDogrulama", err)
	}
}
