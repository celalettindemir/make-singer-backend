package kimlik

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	// Ayni kullanici, BASKA istemci: cikis yalnizca o istemcinin
	// jetonlarini oldurmeli.
	baskaIstemci := yeniRefresh("k1")
	baskaIstemci.ClientID = "makesinger-web"
	d, _ := depo.RefreshOlustur(ctx, baskaIstemci)
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
	if _, err := depo.RefreshOku(ctx, d); err != nil {
		t.Errorf("baska istemcinin jetonu iptal edildi: %v", err)
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
	// Alan alan bakmak yetmez (sha256 ham baytlari ile base64 dizgesi
	// hicbir kosulda esit olmaz, o karsilastirma bedavaya geciyor).
	// Kaydin TAMAMINI serileyip jetonun hicbir alanda gecmedigini
	// dogruluyoruz.
	veri, err := json.Marshal(depo.TumKayitlar())
	if err != nil {
		t.Fatalf("kayitlar serilenemedi: %v", err)
	}
	if strings.Contains(string(veri), jeton) {
		t.Fatalf("jeton kayit icinde bir alanda duz metin gecmis: %s", veri)
	}
	// Map anahtarlari da ozet olmali: sahte depo jetonu anahtar olarak
	// tutarsa duz metin yine bellekte durur.
	for anahtar := range depo.refresh {
		if strings.Contains(anahtar, jeton) {
			t.Fatal("jeton map anahtarinda duz metin saklanmis")
		}
	}
}

// Rotasyon baska bir kullanicinin jetonunu devralmamali: cagiran katmanda
// bir hata olursa ayni aileye yabanci kayit girmesin.
func TestRotasyonSahiplikDogrular(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	jeton, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := depo.RefreshDondur(ctx, jeton, yeniRefresh("k2")); !errors.Is(err, ErrJetonYok) {
		t.Errorf("baska kullanici icin hata = %v, beklenen ErrJetonYok", err)
	}
	baskaIstemci := yeniRefresh("k1")
	baskaIstemci.ClientID = "makesinger-web"
	if _, err := depo.RefreshDondur(ctx, jeton, baskaIstemci); !errors.Is(err, ErrJetonYok) {
		t.Errorf("baska istemci icin hata = %v, beklenen ErrJetonYok", err)
	}
	// Reddedilen denemeler jetonu tuketmemis olmali.
	if _, err := depo.RefreshOku(ctx, jeton); err != nil {
		t.Errorf("gecerli jeton reddedilen denemeler sonrasi olmus: %v", err)
	}
}

// Sahte depo cagirana DAHILI isaretci vermemeli: Postgres her okumada
// taze satir dondurdugu icin cagiranin mutasyonu veritabanini bozmaz,
// sahte depo da ayni yalitimi vermeli.
func TestSahteDepoKopyaDondurur(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	girdi := yeniRefresh("k1")
	jeton, err := depo.RefreshOlustur(ctx, girdi)
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	// Cagiranin elindeki dilimi degistirmek depoyu etkilememeli.
	girdi.Scopes[0] = "BOZULDU"

	kayit, err := depo.RefreshOku(ctx, jeton)
	if err != nil {
		t.Fatalf("RefreshOku: %v", err)
	}
	if kayit.Scopes[0] != "openid" {
		t.Errorf("Scopes cagiranla paylasilmis: %v", kayit.Scopes)
	}
	// Donen kaydi bozmak da depoyu etkilememeli.
	kayit.Scopes[0] = "BOZULDU"
	kayit.TokenHash[0] ^= 0xff
	kayit.UserID = "saldirgan"
	iptal := time.Now().UTC()
	kayit.RevokedAt = &iptal

	tekrar, err := depo.RefreshOku(ctx, jeton)
	if err != nil {
		t.Fatalf("donen kayit mutasyonu depoyu bozdu: %v", err)
	}
	if tekrar.Scopes[0] != "openid" || tekrar.UserID != "k1" || tekrar.RevokedAt != nil {
		t.Errorf("dahili kayit mutasyona ugradi: %+v", tekrar)
	}

	// Access kaydi icin de ayni yalitim.
	kapsam := []string{"openid"}
	if err := depo.AccessKaydet(ctx, "at1", "k1", "makesinger-mobil", kapsam, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("AccessKaydet: %v", err)
	}
	kapsam[0] = "BOZULDU"
	ak, err := depo.AccessOku(ctx, "at1")
	if err != nil {
		t.Fatalf("AccessOku: %v", err)
	}
	if ak.Scopes[0] != "openid" {
		t.Errorf("access Scopes cagiranla paylasilmis: %v", ak.Scopes)
	}
	ak.UserID = "saldirgan"
	if ak2, err := depo.AccessOku(ctx, "at1"); err != nil || ak2.UserID != "k1" {
		t.Errorf("dahili access kaydi mutasyona ugradi: %+v (err=%v)", ak2, err)
	}
}

// TumKayitlar da kopya dondurmeli.
func TestTumKayitlarKopyaDondurur(t *testing.T) {
	depo := NewSahteTokenStore()
	ctx := context.Background()
	jeton, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	for _, kayit := range depo.TumKayitlar() {
		kayit.UserID = "saldirgan"
		kayit.Scopes[0] = "BOZULDU"
	}
	kayit, err := depo.RefreshOku(ctx, jeton)
	if err != nil {
		t.Fatalf("RefreshOku: %v", err)
	}
	if kayit.UserID != "k1" || kayit.Scopes[0] != "openid" {
		t.Errorf("TumKayitlar dahili isaretci dondurmus: %+v", kayit)
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
