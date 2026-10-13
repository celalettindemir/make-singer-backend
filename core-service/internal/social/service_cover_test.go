package social

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// sahteKopyalayici, kopyalari kaydeder; hata doluysa her kopyada onu doner.
type sahteKopyalayici struct {
	kopyalar [][2]string
	hata     error
}

func (k *sahteKopyalayici) Copy(_ context.Context, kaynak, hedef string) error {
	if k.hata != nil {
		return k.hata
	}
	k.kopyalar = append(k.kopyalar, [2]string{kaynak, hedef})
	return nil
}

// coverDeposu: yayinX kullaniciB'nin altyapisi acik, iki stem kopyasi
// olan herkes yayinidir.
func coverDeposu() *sahteDepo {
	d := yeniSahteDepo()
	k := sahteYayin(yayinX, kullaniciB, VisibilityEveryone, StatusPublished)
	k.Genre, k.BPM, k.MusicalKey, k.DurationSec = "pop", 120, MusicalKey{Tonic: "A", Mode: "minor"}, 180
	k.Structure = []StructureItem{{ID: "v1", Type: "verse", Bars: 8}}
	k.Lyrics = Lyrics{Sections: []LyricsSection{{SectionID: "v1", Type: "verse", Lines: []string{"la"}}}}
	d.yayinlar[yayinX] = k
	d.stemListeleri = map[string][]StemRecord{yayinX: {
		{ID: "drums", Instrument: "Davul", Duration: 180.5, Key: "sosyal/" + yayinX + "/stems/drums.mp3"},
		{ID: "bass", Instrument: "Bas", Duration: 179, Key: "sosyal/" + yayinX + "/stems/bass"},
	}}
	return d
}

func TestServis_CoverBaslat(t *testing.T) {
	d := coverDeposu()
	s, _ := testServisi(d)
	kop := &sahteKopyalayici{}
	s.WithCopier(kop)

	b, err := s.StartCover(context.Background(), kullaniciA, yayinX)
	if err != nil {
		t.Fatalf("CoverBaslat: %v", err)
	}
	if b.Source != (SourceSummary{PublicationID: yayinX, Title: "Orijinal", OwnerUsername: "sahip"}) {
		t.Fatalf("kaynak = %+v", b.Source)
	}
	if b.Genre != "pop" || b.BPM != 120 || b.MusicalKey.Mode != "minor" || b.DurationSec != 180 || len(b.Structure) != 1 || len(b.Lyrics.Sections) != 1 {
		t.Fatalf("baslangic = %+v", b)
	}
	if len(kop.kopyalar) != 2 || len(b.Stems) != 2 {
		t.Fatalf("kopyalar = %v, stemler = %+v", kop.kopyalar, b.Stems)
	}
	desen := regexp.MustCompile(`^stems/` + kullaniciA + `/([0-9a-f-]{36})/drums\.mp3$`)
	m := desen.FindStringSubmatch(kop.kopyalar[0][1])
	if kop.kopyalar[0][0] != "sosyal/"+yayinX+"/stems/drums.mp3" || m == nil {
		t.Fatalf("ilk kopya = %v", kop.kopyalar[0])
	}
	if kop.kopyalar[1][1] != "stems/"+kullaniciA+"/"+m[1]+"/bass" {
		t.Fatalf("ikinci kopya ayni klasore, uzantisiz = %v", kop.kopyalar[1])
	}
	st := b.Stems[0]
	if st.ID != "drums" || st.Instrument != "Davul" || st.Duration != 180.5 ||
		!strings.HasSuffix(st.FileURL, "/"+kop.kopyalar[0][1]) || !strings.Contains(st.FileURL, "makeasinger-private") {
		t.Fatalf("stem = %+v", st)
	}

	// Ikinci baslatma yeni bir klasore kopyalar.
	if _, err := s.StartCover(context.Background(), kullaniciA, yayinX); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kop.kopyalar[2][1], m[1]) {
		t.Fatalf("her baslatma yeni klasor almali: %v", kop.kopyalar[2])
	}
}

func TestServis_CoverBaslatImzaliAdres(t *testing.T) {
	d := coverDeposu()
	s := NewService(d, &sahteKuyruk{}, imzaliDepolama{}, testR2()).WithCopier(&sahteKopyalayici{})
	b, err := s.StartCover(context.Background(), kullaniciA, yayinX)
	if err != nil || !strings.HasPrefix(b.Stems[0].FileURL, "https://imzali.example/stems/"+kullaniciA+"/") {
		t.Fatalf("imzali adres = %+v, %v", b, err)
	}
}

func TestServis_CoverBaslatKopyalayicisiz(t *testing.T) {
	s, _ := testServisi(coverDeposu())
	b, err := s.StartCover(context.Background(), kullaniciA, yayinX)
	if err != nil || len(b.Stems) != 2 {
		t.Fatalf("R2'siz (dev) baslangic = %+v, %v", b, err)
	}
}

func TestServis_CoverBaslatHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad        string
		hazirla   func(d *sahteDepo)
		kullanici string
		yayin     string
		beklenen  error
	}{
		{"altyapi kapali", func(d *sahteDepo) { d.yayinlar[yayinX].BackingEnabled = false }, kullaniciA, yayinX, ErrBackingDisabled},
		{"cover yayini", func(d *sahteDepo) { d.yayinlar[yayinX].SourcePublicationID = yayinY }, kullaniciA, yayinX, ErrBackingDisabled},
		{"stem kopyasi yok", func(d *sahteDepo) { d.stemListeleri[yayinX] = []StemRecord{} }, kullaniciA, yayinX, ErrBackingDisabled},
		{"gorunmeyen", func(d *sahteDepo) { d.yayinlar[yayinX].Visibility = VisibilityFollowers }, kullaniciA, yayinX, ErrNotFound},
		{"olmayan", nil, kullaniciA, yayinY, ErrNotFound},
		{"bozuk id", nil, kullaniciA, "x", ErrNotFound},
		{"sahibinin hazirlanan yayini", func(d *sahteDepo) { d.yayinlar[yayinX].Status = StatusPreparing }, kullaniciB, yayinX, ErrInvalidStatus},
		{"baskasinin hazirlanan yayini", func(d *sahteDepo) { d.yayinlar[yayinX].Status = StatusPreparing }, kullaniciA, yayinX, ErrNotFound},
	}
	for _, c := range durumlar {
		d := coverDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		kop := &sahteKopyalayici{}
		s.WithCopier(kop)
		if _, err := s.StartCover(ctx, c.kullanici, c.yayin); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(kop.kopyalar) != 0 {
			t.Errorf("%s: kopya yapilmamali", c.ad)
		}
	}

	s, _ := testServisi(coverDeposu())
	s.WithCopier(&sahteKopyalayici{hata: errors.New("r2 kapali")})
	if _, err := s.StartCover(ctx, kullaniciA, yayinX); err == nil || errors.Is(err, ErrBackingDisabled) {
		t.Fatalf("kopya hatasi sunucu hatasi olmali: %v", err)
	}
}

func TestServis_Coverlar(t *testing.T) {
	d := coverDeposu()
	d.akisOgeleri = ozetler(3)
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Covers(ctx, kullaniciA, strings.ToUpper(yayinX), "", 2)
	if err != nil || len(sayfa.Items) != 2 || sayfa.NextCursor == nil {
		t.Fatalf("sayfa = %+v, %v", sayfa, err)
	}
	if d.sonAkis.tur != "cover" || d.sonAkis.sahip != yayinX || d.sonAkis.izleyici != kullaniciA || d.sonAkis.limit != 2 {
		t.Fatalf("depo cagrisi = %+v", d.sonAkis)
	}

	d.sonAkis = akisCagrisi{}
	d.yayinlar[yayinX].BackingEnabled = false
	bos, err := s.Covers(ctx, kullaniciA, yayinX, "", 20)
	if err != nil || bos.Items == nil || len(bos.Items) != 0 || bos.NextCursor != nil {
		t.Fatalf("altyapi kapali bos sayfa = %+v, %v", bos, err)
	}
	if d.sonAkis.tur != "" {
		t.Fatal("altyapi kapaliyken depo sorgulanmamali")
	}

	d.yayinlar[yayinX].Visibility = VisibilityFollowers
	if _, err := s.Covers(ctx, kullaniciA, yayinX, "", 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gorunmeyen kaynak: %v", err)
	}
	if _, err := s.Covers(ctx, kullaniciA, yayinX, "bozuk!", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("bozuk imlec: %v", err)
	}
}
