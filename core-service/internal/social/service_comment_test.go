package social

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

const (
	kullaniciC = "8f1c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yorum1     = "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	yorumC     = "c21c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
)

// yorumDeposu: kullaniciA'nin profili var; yayinX kullaniciB'nin herkes
// yayinidir (180 sn, yorumlar acik).
func yorumDeposu() *sahteDepo {
	d := profilliDepo()
	k := sahteYayin(yayinX, kullaniciB, VisibilityEveryone, StatusPublished)
	k.CommentsEnabled, k.DurationSec = true, 180
	d.yayinlar[yayinX] = k
	return d
}

func intp(n int) *int { return &n }

func TestServis_YorumYaz(t *testing.T) {
	d := yorumDeposu()
	s, _ := testServisi(d)
	y, err := s.PostComment(context.Background(), kullaniciA, yayinX, CommentInput{Text: "  harika  ", TimeMs: intp(180000)})
	if err != nil {
		t.Fatalf("YorumYaz: %v", err)
	}
	if !y.CanDelete || y.Text != "harika" || y.Author.Username != "ali" || *y.TimeMs != 180000 {
		t.Fatalf("yorum = %+v", y)
	}
	if len(d.eklenenYorumlar) != 1 || d.eklenenYorumlar[0].PublicationID != yayinX || d.eklenenYorumlar[0].AuthorID != kullaniciA {
		t.Fatalf("eklenen = %+v", d.eklenenYorumlar)
	}
	if _, err := s.PostComment(context.Background(), kullaniciA, yayinX, CommentInput{Text: "damgasiz"}); err != nil {
		t.Fatalf("damgasiz yorum: %v", err)
	}
	if d.eklenenYorumlar[1].TimeMs != nil {
		t.Fatal("damgasiz yorumun zamani nil kalmali")
	}
}

func TestServis_YorumYazHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad        string
		hazirla   func(d *sahteDepo)
		kullanici string
		yayin     string
		girdi     CommentInput
		beklenen  error
	}{
		{"bos metin", nil, kullaniciA, yayinX, CommentInput{Text: "   "}, ErrValidation},
		{"uzun metin", nil, kullaniciA, yayinX, CommentInput{Text: strings.Repeat("ş", 501)}, ErrValidation},
		{"NUL iceren metin", nil, kullaniciA, yayinX, CommentInput{Text: "a\x00b"}, ErrValidation},
		{"negatif zaman", nil, kullaniciA, yayinX, CommentInput{Text: "a", TimeMs: intp(-1)}, ErrValidation},
		{"sureyi asan zaman", nil, kullaniciA, yayinX, CommentInput{Text: "a", TimeMs: intp(180001)}, ErrValidation},
		{"int32'yi asan zaman", func(d *sahteDepo) { d.yayinlar[yayinX].DurationSec = 3_000_000 }, kullaniciA, yayinX, CommentInput{Text: "a", TimeMs: intp(math.MaxInt32 + 1)}, ErrValidation},
		{"gorunmeyen yayin", func(d *sahteDepo) { d.yayinlar[yayinX].Visibility = VisibilityFollowers }, kullaniciA, yayinX, CommentInput{Text: "a"}, ErrNotFound},
		{"olmayan yayin", nil, kullaniciA, yayinY, CommentInput{Text: "a"}, ErrNotFound},
		{"bozuk yayin id", nil, kullaniciA, "x", CommentInput{Text: "a"}, ErrNotFound},
		{"yorum kapali", func(d *sahteDepo) { d.yayinlar[yayinX].CommentsEnabled = false }, kullaniciA, yayinX, CommentInput{Text: "a"}, ErrCommentsDisabled},
		{"yorum kapali, sahibi de yazamaz", func(d *sahteDepo) {
			d.profiller[kullaniciB] = &ProfileRecord{UserID: kullaniciB, Username: "sahip"}
			d.yayinlar[yayinX].CommentsEnabled = false
		}, kullaniciB, yayinX, CommentInput{Text: "a"}, ErrCommentsDisabled},
		{"profil yok", nil, kullaniciC, yayinX, CommentInput{Text: "a"}, ErrNoProfile},
	}
	for _, c := range durumlar {
		d := yorumDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		if _, err := s.PostComment(ctx, c.kullanici, c.yayin, c.girdi); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(d.eklenenYorumlar) != 0 {
			t.Errorf("%s: yorum yazilmamali", c.ad)
		}
	}
}

// Yorum kapaliyken sahibi de 403 alir; gorunurluk yorum_kapali'dan once gelir.
func TestServis_YorumlarKapaliVeGorunurluk(t *testing.T) {
	d := yorumDeposu()
	d.yayinlar[yayinX].CommentsEnabled = false
	s, _ := testServisi(d)
	ctx := context.Background()
	for _, k := range []string{kullaniciA, kullaniciB} {
		if _, err := s.Comments(ctx, k, yayinX, "", 20); !errors.Is(err, ErrCommentsDisabled) {
			t.Errorf("%s: %v, beklenen ErrYorumKapali", k, err)
		}
	}
	d.yayinlar[yayinX].Visibility = VisibilityFollowers
	if _, err := s.Comments(ctx, kullaniciA, yayinX, "", 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gorunmeyen yayin: %v", err)
	}
}

func TestServis_YorumlarSayfaVeImlec(t *testing.T) {
	d := yorumDeposu()
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	d.yorumOgeleri = []Comment{
		{ID: yorum1, TimeMs: intp(500), CreatedAt: t0},
		{ID: yorumC, CreatedAt: t0.Add(time.Minute)},
		{ID: "c31c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a", CreatedAt: t0},
	}
	s, _ := testServisi(d)
	ctx := context.Background()

	sayfa, err := s.Comments(ctx, kullaniciA, yayinX, "", 1)
	if err != nil || len(sayfa.Items) != 1 || sayfa.NextCursor == nil {
		t.Fatalf("ilk sayfa = %+v, %v", sayfa, err)
	}
	if _, err := s.Comments(ctx, kullaniciA, yayinX, *sayfa.NextCursor, 1); err != nil {
		t.Fatal(err)
	}
	im := d.sonYorumImleci
	if im == nil || im.Group != 0 || im.TimeMs == nil || *im.TimeMs != 500 || im.ID != yorum1 || !im.T.Equal(t0) {
		t.Fatalf("zamanli imlec = %+v", im)
	}

	sayfa, err = s.Comments(ctx, kullaniciA, yayinX, "", 2)
	if err != nil || sayfa.NextCursor == nil {
		t.Fatal(err)
	}
	if _, err := s.Comments(ctx, kullaniciA, yayinX, *sayfa.NextCursor, 2); err != nil {
		t.Fatal(err)
	}
	im = d.sonYorumImleci
	if im == nil || im.Group != 1 || im.TimeMs != nil || im.ID != yorumC {
		t.Fatalf("damgasiz imlec = %+v", im)
	}

	d.yorumOgeleri = nil
	bos, err := s.Comments(ctx, kullaniciA, yayinX, "", 20)
	if err != nil || bos.Items == nil || bos.NextCursor != nil {
		t.Fatalf("bos sayfa = %+v, %v", bos, err)
	}
}

func TestServis_YorumImleciDogrulama(t *testing.T) {
	gecerliID := "c11c2a9e-1b2c-4d3e-8f9a-0b1c2d3e4f5a"
	for _, ham := range []string{
		"bozuk!",
		*encodeCursor(CommentCursor{Group: 0, T: time.Now(), ID: gecerliID}),                  // G=0 ama Z yok
		*encodeCursor(CommentCursor{Group: 1, TimeMs: intp(3), T: time.Now(), ID: gecerliID}), // G=1 ama Z var
		*encodeCursor(CommentCursor{Group: 2, T: time.Now(), ID: gecerliID}),
		*encodeCursor(CommentCursor{Group: 0, TimeMs: intp(-1), T: time.Now(), ID: gecerliID}),
		*encodeCursor(CommentCursor{Group: 1, T: time.Now(), ID: "uuid-degil"}),
		*encodeCursor(CommentCursor{Group: 1, ID: gecerliID}),
	} {
		if _, err := decodeCommentCursor(ham); !errors.Is(err, ErrValidation) {
			t.Errorf("%q: %v, beklenen ErrDogrulama", ham, err)
		}
	}
}

func TestServis_YorumSil(t *testing.T) {
	ctx := context.Background()
	kayit := func() *CommentRecord {
		return &CommentRecord{ID: yorum1, PublicationID: yayinX, AuthorID: kullaniciA, PublicationOwnerID: kullaniciB}
	}
	// Yazar ve yayin sahibi silebilir.
	for _, k := range []string{kullaniciA, kullaniciB} {
		d := yorumDeposu()
		d.yorumKayitlari[yorum1] = kayit()
		s, _ := testServisi(d)
		if err := s.DeleteComment(ctx, k, yorum1); err != nil {
			t.Fatalf("%s silemedi: %v", k, err)
		}
		if err := s.DeleteComment(ctx, k, yorum1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("tekrar silme 404 olmali: %v", err)
		}
	}
	// Ucuncu kisi: yayini goruyorsa 403, gormuyorsa 404.
	d := yorumDeposu()
	d.yorumKayitlari[yorum1] = kayit()
	s, _ := testServisi(d)
	if err := s.DeleteComment(ctx, kullaniciC, yorum1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("baskasi: %v, beklenen ErrYetki", err)
	}
	d.yayinlar[yayinX].Visibility = VisibilityFollowers
	if err := s.DeleteComment(ctx, kullaniciC, yorum1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gorunmeyen yayin: %v, beklenen ErrBulunamadi", err)
	}
	if len(d.silinenYorumlar) != 0 {
		t.Fatal("yetkisiz silme yazilmamali")
	}
	for _, id := range []string{"x", yorumC} {
		if err := s.DeleteComment(ctx, kullaniciA, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, beklenen ErrBulunamadi", id, err)
		}
	}
}
