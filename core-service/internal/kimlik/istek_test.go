package kimlik

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// testRedis, yerel Redis ister. Yoksa test atlanir: birim testler agsiz
// gecmek zorunda (bkz. Global Constraints).
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	ctx, iptal := context.WithTimeout(context.Background(), time.Second)
	defer iptal()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("yerel Redis yok, atlaniyor")
	}
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

func TestAuthIstekArayuzuKarsilar(t *testing.T) {
	var _ op.AuthRequest = &AuthIstek{}
}

func TestIstekOlusturVeOku(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{
		ClientID:            "makesinger-mobil",
		RedirectURI:         "com.makesinger.app:/oauth2redirect",
		Scopes:              oidc.SpaceDelimitedArray{oidc.ScopeOpenID},
		ResponseType:        oidc.ResponseTypeCode,
		CodeChallenge:       "abc",
		CodeChallengeMethod: oidc.CodeChallengeMethodS256,
		State:               "durum1",
		Nonce:               "n1",
	}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	okunan, err := depo.IDileOku(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if okunan.GetNonce() != "n1" {
		t.Errorf("nonce = %q, beklenen n1", okunan.GetNonce())
	}
	if okunan.GetCodeChallenge() == nil || okunan.GetCodeChallenge().Challenge != "abc" {
		t.Errorf("code challenge kaybolmus: %+v", okunan.GetCodeChallenge())
	}
}

// Kod TEK kullanimlik olmali. Ikinci kez kullanilabilirse, adres
// cubugundan veya loglardan kod kapan biri jeton alir.
func TestKodTekKullanimlik(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{
		ClientID:     "makesinger-mobil",
		ResponseType: oidc.ResponseTypeCode,
	}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.KodKaydet(ctx, istek.GetID(), "kod123"); err != nil {
		t.Fatalf("KodKaydet: %v", err)
	}
	if _, err := depo.KodlaOku(ctx, "kod123"); err != nil {
		t.Fatalf("ilk okuma basarisiz: %v", err)
	}
	if _, err := depo.KodlaOku(ctx, "kod123"); !errors.Is(err, ErrIstekYok) {
		t.Errorf("ikinci okuma hatasi = %v, beklenen ErrIstekYok", err)
	}
}

func TestOlmayanIstek(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	if _, err := depo.IDileOku(context.Background(), "yok"); !errors.Is(err, ErrIstekYok) {
		t.Errorf("hata = %v, beklenen ErrIstekYok", err)
	}
}

// Giris tamamlanmadan istek "done" olmamali: aksi halde kod, kullanici
// hic dogrulanmadan verilir.
func TestIstekBaslangictaTamamlanmamis(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if istek.Done() {
		t.Error("yeni istek tamamlanmis gorunuyor")
	}
	if istek.GetSubject() != "" {
		t.Errorf("subject = %q, bos olmali", istek.GetSubject())
	}
	if err := depo.TamamlandiIsaretle(ctx, istek.GetID(), "kullanici-1"); err != nil {
		t.Fatalf("TamamlandiIsaretle: %v", err)
	}
	okunan, err := depo.IDileOku(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if !okunan.Done() || okunan.GetSubject() != "kullanici-1" {
		t.Errorf("tamamlanmis istek yanlis: done=%v subject=%q", okunan.Done(), okunan.GetSubject())
	}
}

// C1 ek bulgu: zaten tamamlanmis bir istek BASKA bir kullaniciya
// yeniden baglanamaz; Subject EZILMEZ.
func TestTamamlandiIsaretleSubjectEzmez(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.TamamlandiIsaretle(ctx, istek.GetID(), "kullanici-1"); err != nil {
		t.Fatalf("ilk TamamlandiIsaretle: %v", err)
	}
	// 15 kez ust uste baska kimliklerle dene: hepsi reddedilmeli.
	for i := 0; i < 15; i++ {
		if err := depo.TamamlandiIsaretle(ctx, istek.GetID(), "saldirgan"); !errors.Is(err, ErrIstekZatenTamamlandi) {
			t.Fatalf("%d. deneme hatasi = %v, beklenen ErrIstekZatenTamamlandi", i+1, err)
		}
	}
	okunan, err := depo.IDileOku(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if okunan.GetSubject() != "kullanici-1" {
		t.Errorf("subject = %q, EZILMIS (beklenen kullanici-1)", okunan.GetSubject())
	}
}

// AYNI kullanici icin ikinci cagri NO-OP olmali: durust bir cift
// gonderim (formu iki kez yollamak, geri tusu) hata gostermemeli ve
// AuthTime gercek giris anini korumali.
func TestTamamlandiIsaretleAyniKullaniciNoOp(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Olustur(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.TamamlandiIsaretle(ctx, istek.GetID(), "kullanici-1"); err != nil {
		t.Fatalf("ilk TamamlandiIsaretle: %v", err)
	}
	ilk, err := depo.IDileOku(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if err := depo.TamamlandiIsaretle(ctx, istek.GetID(), "kullanici-1"); err != nil {
		t.Fatalf("ikinci TamamlandiIsaretle (ayni kullanici) hata dondu: %v", err)
	}
	ikinci, err := depo.IDileOku(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if !ikinci.GetAuthTime().Equal(ilk.GetAuthTime()) {
		t.Error("AuthTime yeniden yazildi; no-op olmasi gerekirdi")
	}
}

// M4: Olustur'a gelen ucuncu parametre id_token_hint'in "sub"udur, bir
// istek ID'si DEGILDIR. Istek ID'si HER ZAMAN yeni bir UUID olmali;
// aksi halde es zamanli iki akis ayni Redis anahtarini ezer.
func TestIstekIDIpucuSubjectOlmaz(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	const ipucu = "kullanici-42"
	birinci, err := depo.Olustur(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, ipucu)
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if birinci.GetID() == ipucu {
		t.Fatal("istek ID'si id_token_hint sub'una esitlendi")
	}
	if birinci.IpucuSubject != ipucu {
		t.Errorf("IpucuSubject = %q, beklenen %q", birinci.IpucuSubject, ipucu)
	}
	// Ipucu Done()'u etkilememeli: kullanici henuz giris yapmadi.
	if birinci.Done() || birinci.GetSubject() != "" {
		t.Error("ipucu ile istek tamamlanmis gorunuyor")
	}
	// Es zamanli ikinci akis ayni anahtari EZMEMELI.
	ikinci, err := depo.Olustur(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, ipucu)
	if err != nil {
		t.Fatalf("ikinci Olustur: %v", err)
	}
	if ikinci.GetID() == birinci.GetID() {
		t.Fatal("ayni ipucu ile iki istek ayni ID'yi aldi")
	}
}

// Cerez baglamasi, auth istegiyle ayni depoda tutulur ve okunabilir
// olmali; eslesmeyen bir cerez degeri ErrOturumYok dondurmeli.
func TestOturumBaglamaYazVeOku(t *testing.T) {
	depo := NewIstekDepo(testRedis(t))
	ctx := context.Background()
	baglama := OturumBaglama{AuthRequestID: "istek-1", CSRF: "csrf-1"}
	if err := depo.OturumYaz(ctx, "oturum-1", baglama, istekTTL); err != nil {
		t.Fatalf("OturumYaz: %v", err)
	}
	okunan, err := depo.OturumOku(ctx, "oturum-1")
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	if okunan != baglama {
		t.Errorf("baglama = %+v, beklenen %+v", okunan, baglama)
	}
	if _, err := depo.OturumOku(ctx, "yok"); !errors.Is(err, ErrOturumYok) {
		t.Errorf("hata = %v, beklenen ErrOturumYok", err)
	}
}
