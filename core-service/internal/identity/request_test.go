package identity

import (
	"context"
	"errors"
	"reflect"
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
	var _ op.AuthRequest = &AuthRequest{}
}

func TestIstekOlusturVeOku(t *testing.T) {
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{
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
	okunan, err := depo.GetByID(ctx, istek.GetID())
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
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{
		ClientID:     "makesinger-mobil",
		ResponseType: oidc.ResponseTypeCode,
	}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.SaveCode(ctx, istek.GetID(), "kod123"); err != nil {
		t.Fatalf("KodKaydet: %v", err)
	}
	if _, err := depo.GetByCode(ctx, "kod123"); err != nil {
		t.Fatalf("ilk okuma basarisiz: %v", err)
	}
	if _, err := depo.GetByCode(ctx, "kod123"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("ikinci okuma hatasi = %v, beklenen ErrIstekYok", err)
	}
}

func TestOlmayanIstek(t *testing.T) {
	depo := NewRequestStore(testRedis(t))
	if _, err := depo.GetByID(context.Background(), "yok"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("hata = %v, beklenen ErrIstekYok", err)
	}
}

// Giris tamamlanmadan istek "done" olmamali: aksi halde kod, kullanici
// hic dogrulanmadan verilir.
func TestIstekBaslangictaTamamlanmamis(t *testing.T) {
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if istek.Done() {
		t.Error("yeni istek tamamlanmis gorunuyor")
	}
	if istek.GetSubject() != "" {
		t.Errorf("subject = %q, bos olmali", istek.GetSubject())
	}
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("TamamlandiIsaretle: %v", err)
	}
	okunan, err := depo.GetByID(ctx, istek.GetID())
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
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("ilk TamamlandiIsaretle: %v", err)
	}
	// 15 kez ust uste baska kimliklerle dene: hepsi reddedilmeli.
	for i := 0; i < 15; i++ {
		if err := depo.MarkDone(ctx, istek.GetID(), "saldirgan", []string{amrPassword}); !errors.Is(err, ErrRequestAlreadyCompleted) {
			t.Fatalf("%d. deneme hatasi = %v, beklenen ErrIstekZatenTamamlandi", i+1, err)
		}
	}
	okunan, err := depo.GetByID(ctx, istek.GetID())
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
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("ilk TamamlandiIsaretle: %v", err)
	}
	ilk, err := depo.GetByID(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("ikinci TamamlandiIsaretle (ayni kullanici) hata dondu: %v", err)
	}
	ikinci, err := depo.GetByID(ctx, istek.GetID())
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
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	const ipucu = "kullanici-42"
	birinci, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, ipucu)
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if birinci.GetID() == ipucu {
		t.Fatal("istek ID'si id_token_hint sub'una esitlendi")
	}
	if birinci.HintSubject != ipucu {
		t.Errorf("IpucuSubject = %q, beklenen %q", birinci.HintSubject, ipucu)
	}
	// Ipucu Done()'u etkilememeli: kullanici henuz giris yapmadi.
	if birinci.Done() || birinci.GetSubject() != "" {
		t.Error("ipucu ile istek tamamlanmis gorunuyor")
	}
	// Es zamanli ikinci akis ayni anahtari EZMEMELI.
	ikinci, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, ipucu)
	if err != nil {
		t.Fatalf("ikinci Olustur: %v", err)
	}
	if ikinci.GetID() == birinci.GetID() {
		t.Fatal("ayni ipucu ile iki istek ayni ID'yi aldi")
	}
}

// K6: sifreyle giris sonrasi GetAMR() ["pwd"] donmeli.
func TestAMRSifreyleGiristePwd(t *testing.T) {
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("TamamlandiIsaretle: %v", err)
	}
	okunan, err := depo.GetByID(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if got := okunan.GetAMR(); len(got) != 1 || got[0] != "pwd" {
		t.Errorf("GetAMR() = %v, beklenen [pwd]", got)
	}
}

// K6: Google ile giren kullanicinin tokeninda "pwd" YAZMAMALI.
func TestAMRFederasyondaPwdDegil(t *testing.T) {
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrFederation}); err != nil {
		t.Fatalf("TamamlandiIsaretle: %v", err)
	}
	okunan, err := depo.GetByID(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	got := okunan.GetAMR()
	if len(got) != 1 || got[0] != amrFederation {
		t.Errorf("GetAMR() = %v, beklenen [%s]", got, amrFederation)
	}
	for _, v := range got {
		if v == "pwd" {
			t.Errorf("GetAMR() = %v, 'pwd' ICERMEMELI", got)
		}
	}
}

// Varsayilan sessizce "pwd" OLMAMALI: amr vermeyi unutan bir cagiran
// yanlis bir beyan uretmemeli, bos uretmeli.
func TestAMRVarsayilanPwdDegil(t *testing.T) {
	istek := &AuthRequest{}
	if len(istek.GetAMR()) != 0 {
		t.Errorf("GetAMR() = %v, beklenen bos", istek.GetAMR())
	}
}

// amr ikinci (no-op) cagrida EZILMEMELI: ilk gercek giris yonteminin
// dogru olani kaydeder.
func TestAMRNoOpdaEzilmez(t *testing.T) {
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	istek, err := depo.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Olustur: %v", err)
	}
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("ilk TamamlandiIsaretle: %v", err)
	}
	// Ayni kullanici icin ikinci cagri, FARKLI bir amr ile gelse bile
	// NO-OP olmali ve ilk amr'i EZMEMELI.
	if err := depo.MarkDone(ctx, istek.GetID(), "kullanici-1", []string{amrFederation}); err != nil {
		t.Fatalf("ikinci TamamlandiIsaretle (no-op): %v", err)
	}
	okunan, err := depo.GetByID(ctx, istek.GetID())
	if err != nil {
		t.Fatalf("IDileOku: %v", err)
	}
	if got := okunan.GetAMR(); len(got) != 1 || got[0] != amrPassword {
		t.Errorf("GetAMR() = %v, beklenen [%s] (ezilmemis olmali)", got, amrPassword)
	}
}

// amr Postgres'teki refresh_tokens.amr kolonuna da dogru dusmeli:
// yenileme sonrasi token'da hala dogru amr olmali. Sahte (bellek ici)
// TokenStore kullanilir: olculen Depo'nun AuthIstek -> RefreshKayit ->
// refresh rotasyonu uzerinden amr'i tasima mantigi, Postgres suruculugu
// degil (o ayri bir testle, jeton_depo_pg_test.go'da olculuyor).
func TestAMRRefreshYenilemesindeKorunur(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	istek := &AuthRequest{
		ID:       "istek-amr-1",
		ClientID: "makesinger-mobil",
		Subject:  "kullanici-1",
		Scopes:   []string{"openid", "offline_access"},
		AuthTime: time.Now().UTC(),
		AMR:      []string{amrFederation},
	}
	_, refreshToken, _, err := d.CreateAccessAndRefreshTokens(ctx, istek, "")
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}
	kayit, err := d.tokens.GetRefresh(ctx, refreshToken)
	if err != nil {
		t.Fatalf("RefreshOku: %v", err)
	}
	if len(kayit.AMR) != 1 || kayit.AMR[0] != amrFederation {
		t.Fatalf("ilk kayitta amr = %v, beklenen [%s]", kayit.AMR, amrFederation)
	}
	// Kutuphanenin gercek cagri sirasini taklit et: refresh akisinda
	// TokenRequestByRefreshToken amr'i tasiyan RefreshTokenRequest'i
	// uretir, CreateAccessAndRefreshTokens onu rotasyona verir.
	refreshIstegi, err := d.TokenRequestByRefreshToken(ctx, refreshToken)
	if err != nil {
		t.Fatalf("TokenRequestByRefreshToken: %v", err)
	}
	_, yeniRefreshToken, _, err := d.CreateAccessAndRefreshTokens(ctx, refreshIstegi, refreshToken)
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens (rotasyon): %v", err)
	}
	yeniKayit, err := d.tokens.GetRefresh(ctx, yeniRefreshToken)
	if err != nil {
		t.Fatalf("RefreshOku (yeni): %v", err)
	}
	if len(yeniKayit.AMR) != 1 || yeniKayit.AMR[0] != amrFederation {
		t.Errorf("rotasyon sonrasi amr = %v, beklenen [%s]", yeniKayit.AMR, amrFederation)
	}
}

// Cerez baglamasi, auth istegiyle ayni depoda tutulur ve okunabilir
// olmali; eslesmeyen bir cerez degeri ErrOturumYok dondurmeli.
func TestOturumBaglamaYazVeOku(t *testing.T) {
	depo := NewRequestStore(testRedis(t))
	ctx := context.Background()
	// Kume: bir cerez birden fazla istege bagli olabilir (paralel
	// sekmeler, bkz. oturumBaglamaUstSinir). Redis'e giden JSON tur
	// yolculugunu da dogruluyoruz: alanlar disa acik olmazsa
	// encoding/json onlari sessizce atlar ve baglama kaybolur.
	var baglama SessionBinding
	baglama.Add("istek-1", "csrf-1")
	baglama.Add("istek-2", "csrf-2")
	if err := depo.WriteSession(ctx, "oturum-1", baglama, requestTTL); err != nil {
		t.Fatalf("OturumYaz: %v", err)
	}
	okunan, err := depo.ReadSession(ctx, "oturum-1")
	if err != nil {
		t.Fatalf("OturumOku: %v", err)
	}
	if !reflect.DeepEqual(okunan, baglama) {
		t.Errorf("baglama = %+v, beklenen %+v", okunan, baglama)
	}
	for _, d := range []struct{ id, csrf string }{{"istek-1", "csrf-1"}, {"istek-2", "csrf-2"}} {
		csrf, bagliMi := okunan.CSRFFor(d.id)
		if !bagliMi || csrf != d.csrf {
			t.Errorf("CSRFBul(%q) = (%q, %v), beklenen (%q, true)", d.id, csrf, bagliMi, d.csrf)
		}
	}
	if _, bagliMi := okunan.CSRFFor("istek-3"); bagliMi {
		t.Error("baglanmamis id icin CSRFBul true dondu")
	}
	if _, err := depo.ReadSession(ctx, "yok"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("hata = %v, beklenen ErrOturumYok", err)
	}
}
