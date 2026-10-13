package social

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestSayfaBoyutu(t *testing.T) {
	durumlar := map[string]int{"": 20, "x": 20, "0": 20, "-3": 20, "1": 1, "35": 35, "50": 50, "51": 50, "1000": 50}
	for ham, beklenen := range durumlar {
		if got := PageSize(ham); got != beklenen {
			t.Errorf("SayfaBoyutu(%q) = %d, beklenen %d", ham, got, beklenen)
		}
	}
}

func TestZamanImleci_GidisDonus(t *testing.T) {
	an := time.Date(2026, 10, 12, 9, 30, 15, 123456000, time.UTC)
	ham := encodeCursor(TimeCursor{T: an, ID: yayinX})
	im, err := decodeTimeCursor(*ham)
	if err != nil || !im.T.Equal(an) || im.ID != yayinX {
		t.Fatalf("imlec = %+v, %v", im, err)
	}
	if im, err := decodeTimeCursor(""); im != nil || err != nil {
		t.Fatalf("bos imlec nil olmali: %+v %v", im, err)
	}
}

func TestPuanImleci_GidisDonus(t *testing.T) {
	ham := encodeCursor(ScoreCursor{Score: 0.1234567890123, ID: yayinX, RefMs: 1760000000000})
	im, err := decodeScoreCursor(*ham)
	if err != nil || im.Score != 0.1234567890123 || im.RefMs != 1760000000000 {
		t.Fatalf("imlec = %+v, %v", im, err)
	}
}

func TestImlec_BozukReddedilir(t *testing.T) {
	bozuklar := []string{"!!!", "e30", *encodeCursor(map[string]string{"id": "x"}), *encodeCursor(TimeCursor{ID: yayinX})}
	for _, ham := range bozuklar {
		if _, err := decodeTimeCursor(ham); !errors.Is(err, ErrValidation) {
			t.Errorf("zaman %q: %v", ham, err)
		}
	}
	for _, ham := range []string{"!!!", *encodeCursor(ScoreCursor{ID: yayinX})} {
		if _, err := decodeScoreCursor(ham); !errors.Is(err, ErrValidation) {
			t.Errorf("puan %q: %v", ham, err)
		}
	}
}

// zaman_ms sutunu integer'dir ($4::int); MaxInt32'yi asan z pgx'te 500
// yerine 422 olmali. Sinirin kendisi gecerlidir.
func TestYorumImleci_ZInt32Siniri(t *testing.T) {
	an := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if _, err := decodeCommentCursor(*encodeCursor(CommentCursor{Group: 0, TimeMs: intp(math.MaxInt32), T: an, ID: yorum1})); err != nil {
		t.Fatalf("MaxInt32 gecerli olmali: %v", err)
	}
	if _, err := decodeCommentCursor(*encodeCursor(CommentCursor{Group: 0, TimeMs: intp(math.MaxInt32 + 1), T: an, ID: yorum1})); !errors.Is(err, ErrValidation) {
		t.Fatalf("MaxInt32+1: %v, beklenen ErrDogrulama", err)
	}
}
