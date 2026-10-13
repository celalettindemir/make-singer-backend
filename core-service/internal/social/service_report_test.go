package social

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestServis_SikayetYayinVeYorum(t *testing.T) {
	d := yorumDeposu()
	d.yorumKayitlari[yorum1] = &CommentRecord{ID: yorum1, PublicationID: yayinX, AuthorID: kullaniciB, PublicationOwnerID: kullaniciB}
	s, _ := testServisi(d)
	ctx := context.Background()

	if err := s.Report(ctx, kullaniciC, ReportInput{TargetType: "yayin", TargetID: strings.ToUpper(yayinX), Reason: "spam"}); err != nil {
		t.Fatalf("yayin sikayeti: %v", err)
	}
	if err := s.Report(ctx, kullaniciC, ReportInput{TargetType: "yorum", TargetID: yorum1, Reason: "taciz", Description: "  kaba  "}); err != nil {
		t.Fatalf("yorum sikayeti: %v", err)
	}
	if len(d.sikayetler) != 2 {
		t.Fatalf("sikayetler = %+v", d.sikayetler)
	}
	y, c := d.sikayetler[0], d.sikayetler[1]
	if y.ReporterID != kullaniciC || y.TargetID != yayinX || y.PublicationID != yayinX || y.Reason != "spam" || y.ID == "" {
		t.Fatalf("yayin sikayeti = %+v", y)
	}
	if c.TargetType != "yorum" || c.TargetID != yorum1 || c.PublicationID != yayinX || c.Description != "kaba" {
		t.Fatalf("yorum sikayeti = %+v", c)
	}
}

func TestServis_SikayetHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad       string
		hazirla  func(d *sahteDepo)
		girdi    ReportInput
		beklenen error
	}{
		{"bilinmeyen hedef turu", nil, ReportInput{TargetType: "profil", TargetID: yayinX, Reason: "spam"}, ErrValidation},
		{"bilinmeyen neden", nil, ReportInput{TargetType: "yayin", TargetID: yayinX, Reason: "kotu"}, ErrValidation},
		{"uzun aciklama", nil, ReportInput{TargetType: "yayin", TargetID: yayinX, Reason: "diger", Description: strings.Repeat("a", 301)}, ErrValidation},
		{"NUL iceren aciklama", nil, ReportInput{TargetType: "yayin", TargetID: yayinX, Reason: "diger", Description: "kaba\x00"}, ErrValidation},
		{"bozuk kimlik", nil, ReportInput{TargetType: "yayin", TargetID: "x", Reason: "spam"}, ErrNotFound},
		{"olmayan yayin", nil, ReportInput{TargetType: "yayin", TargetID: yayinY, Reason: "spam"}, ErrNotFound},
		{"gorunmeyen yayin", func(d *sahteDepo) { d.yayinlar[yayinX].Visibility = VisibilityFollowers },
			ReportInput{TargetType: "yayin", TargetID: yayinX, Reason: "spam"}, ErrNotFound},
		{"olmayan yorum", nil, ReportInput{TargetType: "yorum", TargetID: yorumC, Reason: "spam"}, ErrNotFound},
		{"silinmis yorum", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &CommentRecord{ID: yorum1, PublicationID: yayinX, Deleted: true}
		}, ReportInput{TargetType: "yorum", TargetID: yorum1, Reason: "spam"}, ErrNotFound},
		{"gorunmeyen yayinin yorumu", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &CommentRecord{ID: yorum1, PublicationID: yayinX}
			d.yayinlar[yayinX].Deleted = true
		}, ReportInput{TargetType: "yorum", TargetID: yorum1, Reason: "spam"}, ErrNotFound},
		{"gizli (takipciler) yayinin yorumu", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &CommentRecord{ID: yorum1, PublicationID: yayinX, AuthorID: kullaniciB, PublicationOwnerID: kullaniciB}
			d.yayinlar[yayinX].Visibility = VisibilityFollowers
		}, ReportInput{TargetType: "yorum", TargetID: yorum1, Reason: "spam"}, ErrNotFound},
	}
	for _, c := range durumlar {
		d := yorumDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		if err := s.Report(ctx, kullaniciC, c.girdi); !errors.Is(err, c.beklenen) {
			t.Errorf("%s: %v, beklenen %v", c.ad, err, c.beklenen)
		}
		if len(d.sikayetler) != 0 {
			t.Errorf("%s: sikayet yazilmamali", c.ad)
		}
	}
}

// Bozuk hedefId depoya hic gitmeden 404'tur: depo her cagride baska bir
// hata doner, yani bir cagri sonucu ErrBulunamadi'dan ayirirdi.
func TestServis_SikayetBozukKimlikDepoyaGitmez(t *testing.T) {
	d := yorumDeposu()
	d.hata = errors.New("depo cagrilmamali")
	s, _ := testServisi(d)
	for _, tur := range []string{"yayin", "yorum"} {
		err := s.Report(context.Background(), kullaniciC, ReportInput{TargetType: tur, TargetID: "uuid-degil", Reason: "spam"})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, beklenen ErrBulunamadi (depo cagrilmadan)", tur, err)
		}
	}
	if len(d.sikayetler) != 0 {
		t.Fatalf("sikayet yazilmamali: %+v", d.sikayetler)
	}
}
