package sosyal

import (
	"errors"
	"testing"
	"time"
)

func TestSayfaBoyutu(t *testing.T) {
	durumlar := map[string]int{"": 20, "x": 20, "0": 20, "-3": 20, "1": 1, "35": 35, "50": 50, "51": 50, "1000": 50}
	for ham, beklenen := range durumlar {
		if got := SayfaBoyutu(ham); got != beklenen {
			t.Errorf("SayfaBoyutu(%q) = %d, beklenen %d", ham, got, beklenen)
		}
	}
}

func TestZamanImleci_GidisDonus(t *testing.T) {
	an := time.Date(2026, 10, 12, 9, 30, 15, 123456000, time.UTC)
	ham := imlecKodla(ZamanImleci{T: an, ID: yayinX})
	im, err := zamanImleciCoz(*ham)
	if err != nil || !im.T.Equal(an) || im.ID != yayinX {
		t.Fatalf("imlec = %+v, %v", im, err)
	}
	if im, err := zamanImleciCoz(""); im != nil || err != nil {
		t.Fatalf("bos imlec nil olmali: %+v %v", im, err)
	}
}

func TestPuanImleci_GidisDonus(t *testing.T) {
	ham := imlecKodla(PuanImleci{P: 0.1234567890123, ID: yayinX, N: 1760000000000})
	im, err := puanImleciCoz(*ham)
	if err != nil || im.P != 0.1234567890123 || im.N != 1760000000000 {
		t.Fatalf("imlec = %+v, %v", im, err)
	}
}

func TestImlec_BozukReddedilir(t *testing.T) {
	bozuklar := []string{"!!!", "e30", *imlecKodla(map[string]string{"id": "x"}), *imlecKodla(ZamanImleci{ID: yayinX})}
	for _, ham := range bozuklar {
		if _, err := zamanImleciCoz(ham); !errors.Is(err, ErrDogrulama) {
			t.Errorf("zaman %q: %v", ham, err)
		}
	}
	for _, ham := range []string{"!!!", *imlecKodla(PuanImleci{ID: yayinX})} {
		if _, err := puanImleciCoz(ham); !errors.Is(err, ErrDogrulama) {
			t.Errorf("puan %q: %v", ham, err)
		}
	}
}
