package kimlik

import (
	"context"
	"errors"
	"testing"
	"time"
)

func yeniRefresh(userID string) *RefreshKayit {
	return &RefreshKayit{
		UserID:    userID,
		ClientID:  "makesinger-mobil",
		Scopes:    []string{"openid", "offline_access"},
		Audience:  []string{"makesinger-mobil"},
		AMR:       []string{"pwd"},
		AuthTime:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
}

func TestRefreshOlusturVeOku(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	jeton, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if jeton == "" {
		t.Fatal("bos jeton dondu")
	}
	kayit, err := depo.RefreshOku(ctx, jeton)
	if err != nil {
		t.Fatalf("RefreshOku: %v", err)
	}
	if kayit.UserID != "k1" {
		t.Errorf("UserID = %q", kayit.UserID)
	}
	if kayit.FamilyID == "" {
		t.Error("FamilyID bos")
	}
}

// Rotasyon: her kullanim yeni jeton uretir ve eski jeton olur.
func TestRefreshDonerVeEskisiOlur(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	eski, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	yeni, err := depo.RefreshDondur(ctx, eski, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	if yeni == eski {
		t.Fatal("jeton donmemis, ayni deger geldi")
	}
	if _, err := depo.RefreshOku(ctx, yeni); err != nil {
		t.Errorf("yeni jeton okunamadi: %v", err)
	}
}

// Calinti jeton tespiti: kullanilmis bir jeton tekrar sunulursa ailenin
// TAMAMI iptal olur. Yalnizca sunulan jetonu reddetmek yetmez; saldirgan
// zaten yeni jetonu almis olabilir.
func TestKullanilmisJetonAileyiIptalEder(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	birinci, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	ikinci, err := depo.RefreshDondur(ctx, birinci, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	// Saldirgan eski jetonu tekrar sunuyor.
	if _, err := depo.RefreshDondur(ctx, birinci, yeniRefresh("k1")); !errors.Is(err, ErrJetonTekrar) {
		t.Fatalf("hata = %v, beklenen ErrJetonTekrar", err)
	}
	// Gercek kullanicinin elindeki gecerli jeton da artik olu olmali.
	if _, err := depo.RefreshOku(ctx, ikinci); !errors.Is(err, ErrJetonYok) {
		t.Errorf("aile iptal edilmemis, ikinci jeton hala gecerli (err=%v)", err)
	}
}

func TestSuresiGecmisRefreshReddedilir(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	k := yeniRefresh("k1")
	k.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	jeton, err := depo.RefreshOlustur(ctx, k)
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := depo.RefreshOku(ctx, jeton); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}

func TestOlmayanRefresh(t *testing.T) {
	depo := NewSahteTokenStore()
	if _, err := depo.RefreshOku(context.Background(), "uydurma"); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}

// Cikis: kullanicinin butun refresh jetonlari olur.
func TestKullaniciIptalHepsiniOldurur(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	a, _ := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	b, _ := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	c, _ := depo.RefreshOlustur(ctx, yeniRefresh("k2"))
	if err := depo.KullaniciIptal(ctx, "k1", "makesinger-mobil"); err != nil {
		t.Fatalf("KullaniciIptal: %v", err)
	}
	for ad, jeton := range map[string]string{"a": a, "b": b} {
		if _, err := depo.RefreshOku(ctx, jeton); !errors.Is(err, ErrJetonYok) {
			t.Errorf("%s hala gecerli", ad)
		}
	}
	if _, err := depo.RefreshOku(ctx, c); err != nil {
		t.Errorf("baska kullanicinin jetonu iptal edildi: %v", err)
	}
}

// Jetonun kendisi degil ozeti saklanmali.
func TestJetonDuzMetinSaklanmaz(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	jeton, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	for _, kayit := range depo.TumKayitlar() {
		if string(kayit.TokenHash) == jeton {
			t.Fatal("jeton duz metin saklanmis")
		}
	}
}

func TestAccessKaydetVeOku(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	son := time.Now().UTC().Add(15 * time.Minute)
	if err := depo.AccessKaydet(ctx, "at1", "k1", "makesinger-mobil", []string{"openid"}, son); err != nil {
		t.Fatalf("AccessKaydet: %v", err)
	}
	kayit, err := depo.AccessOku(ctx, "at1")
	if err != nil {
		t.Fatalf("AccessOku: %v", err)
	}
	if kayit.UserID != "k1" {
		t.Errorf("UserID = %q", kayit.UserID)
	}
	if _, err := depo.AccessOku(ctx, "yok"); !errors.Is(err, ErrJetonYok) {
		t.Errorf("hata = %v, beklenen ErrJetonYok", err)
	}
}
