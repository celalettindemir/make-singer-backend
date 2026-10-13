package social

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestServis_SikayetYayinVeYorum(t *testing.T) {
	d := yorumDeposu()
	d.yorumKayitlari[yorum1] = &YorumKaydi{ID: yorum1, YayinID: yayinX, YazarID: kullaniciB, YayinSahibiID: kullaniciB}
	s, _ := testServisi(d)
	ctx := context.Background()

	if err := s.Sikayet(ctx, kullaniciC, SikayetGirdisi{HedefTuru: "yayin", HedefID: strings.ToUpper(yayinX), Neden: "spam"}); err != nil {
		t.Fatalf("yayin sikayeti: %v", err)
	}
	if err := s.Sikayet(ctx, kullaniciC, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorum1, Neden: "taciz", Aciklama: "  kaba  "}); err != nil {
		t.Fatalf("yorum sikayeti: %v", err)
	}
	if len(d.sikayetler) != 2 {
		t.Fatalf("sikayetler = %+v", d.sikayetler)
	}
	y, c := d.sikayetler[0], d.sikayetler[1]
	if y.BildirenID != kullaniciC || y.HedefID != yayinX || y.YayinID != yayinX || y.Neden != "spam" || y.ID == "" {
		t.Fatalf("yayin sikayeti = %+v", y)
	}
	if c.HedefTuru != "yorum" || c.HedefID != yorum1 || c.YayinID != yayinX || c.Aciklama != "kaba" {
		t.Fatalf("yorum sikayeti = %+v", c)
	}
}

func TestServis_SikayetHatalari(t *testing.T) {
	ctx := context.Background()
	durumlar := []struct {
		ad       string
		hazirla  func(d *sahteDepo)
		girdi    SikayetGirdisi
		beklenen error
	}{
		{"bilinmeyen hedef turu", nil, SikayetGirdisi{HedefTuru: "profil", HedefID: yayinX, Neden: "spam"}, ErrValidation},
		{"bilinmeyen neden", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinX, Neden: "kotu"}, ErrValidation},
		{"uzun aciklama", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinX, Neden: "diger", Aciklama: strings.Repeat("a", 301)}, ErrValidation},
		{"NUL iceren aciklama", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinX, Neden: "diger", Aciklama: "kaba\x00"}, ErrValidation},
		{"bozuk kimlik", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: "x", Neden: "spam"}, ErrNotFound},
		{"olmayan yayin", nil, SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinY, Neden: "spam"}, ErrNotFound},
		{"gorunmeyen yayin", func(d *sahteDepo) { d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler },
			SikayetGirdisi{HedefTuru: "yayin", HedefID: yayinX, Neden: "spam"}, ErrNotFound},
		{"olmayan yorum", nil, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorumC, Neden: "spam"}, ErrNotFound},
		{"silinmis yorum", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &YorumKaydi{ID: yorum1, YayinID: yayinX, Silindi: true}
		}, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorum1, Neden: "spam"}, ErrNotFound},
		{"gorunmeyen yayinin yorumu", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &YorumKaydi{ID: yorum1, YayinID: yayinX}
			d.yayinlar[yayinX].Silindi = true
		}, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorum1, Neden: "spam"}, ErrNotFound},
		{"gizli (takipciler) yayinin yorumu", func(d *sahteDepo) {
			d.yorumKayitlari[yorum1] = &YorumKaydi{ID: yorum1, YayinID: yayinX, YazarID: kullaniciB, YayinSahibiID: kullaniciB}
			d.yayinlar[yayinX].Gorunurluk = GorunurlukTakipciler
		}, SikayetGirdisi{HedefTuru: "yorum", HedefID: yorum1, Neden: "spam"}, ErrNotFound},
	}
	for _, c := range durumlar {
		d := yorumDeposu()
		if c.hazirla != nil {
			c.hazirla(d)
		}
		s, _ := testServisi(d)
		if err := s.Sikayet(ctx, kullaniciC, c.girdi); !errors.Is(err, c.beklenen) {
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
		err := s.Sikayet(context.Background(), kullaniciC, SikayetGirdisi{HedefTuru: tur, HedefID: "uuid-degil", Neden: "spam"})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, beklenen ErrBulunamadi (depo cagrilmadan)", tur, err)
		}
	}
	if len(d.sikayetler) != 0 {
		t.Fatalf("sikayet yazilmamali: %+v", d.sikayetler)
	}
}
