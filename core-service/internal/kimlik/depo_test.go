package kimlik

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func testDepo(t *testing.T) *Depo {
	t.Helper()
	a, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	return NewDepo(testAuthCfg(), NewSahteUserStore(), NewSahteTokenStore(), nil, a)
}

func TestDepoArayuzuKarsilar(t *testing.T) {
	var _ op.Storage = testDepo(t)
}

// Yapilandirilmamis bir client ID ile jeton alinamamali.
func TestTanimsizIstemciReddedilir(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	if _, err := d.GetClientByClientID(ctx, "baska-uygulama"); err == nil {
		t.Error("tanimsiz istemci kabul edildi")
	}
	if _, err := d.GetClientByClientID(ctx, "makesinger-mobil"); err != nil {
		t.Errorf("tanimli istemci reddedildi: %v", err)
	}
}

// Public client secret tasimaz; secret ile dogrulama HER ZAMAN
// basarisiz olmali. Basarili donerse bos secret'la istemci taklit edilir.
func TestSecretIleDogrulamaHepReddedilir(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	for _, secret := range []string{"", "herhangi"} {
		if err := d.AuthorizeClientIDSecret(ctx, "makesinger-mobil", secret); err == nil {
			t.Errorf("secret %q kabul edildi", secret)
		}
	}
}

// Yeniden kullanilmis refresh jetonu kutuphanenin bekledigi hataya
// cevrilmeli; aksi halde istemciye 500 doner ve neden anlasilmaz.
func TestTekrarKullanimInvalidRefreshOlur(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	birinci, err := d.jetonlar.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := d.jetonlar.RefreshDondur(ctx, birinci, yeniRefresh("k1")); err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	if _, err := d.TokenRequestByRefreshToken(ctx, birinci); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
}

func TestJWTProfileDesteklenmez(t *testing.T) {
	d := testDepo(t)
	if _, err := d.ValidateJWTProfileScopes(context.Background(), "k1", []string{"openid"}); err == nil {
		t.Error("JWT profile kabul edildi")
	}
}

// GetRefreshTokenInfo, gecersiz bir jeton icin de op.ErrInvalidRefreshToken
// donmek ZORUNDA (kutuphane sozlesmesi); baska bir hata donerse cagiran
// yanlis yorumlar.
func TestGetRefreshTokenInfoGecersizIcinInvalidRefreshDoner(t *testing.T) {
	d := testDepo(t)
	if _, _, err := d.GetRefreshTokenInfo(context.Background(), "makesinger-mobil", "uydurma"); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
}

// GetRefreshTokenInfo, gecerli bir refresh jetonu icin userID ve tokenID
// dondurmeli.
func TestGetRefreshTokenInfoGecerli(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	jeton, err := d.jetonlar.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	userID, tokenID, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", jeton)
	if err != nil {
		t.Fatalf("GetRefreshTokenInfo: %v", err)
	}
	if userID != "k1" {
		t.Errorf("userID = %q, beklenen k1", userID)
	}
	if tokenID == "" {
		t.Error("tokenID bos")
	}
}

// RevokeToken bir refresh jetonunu hedef alirsa TUM AILE olmeli: ayni
// aileden baska bir jeton da artik gecersiz olmali.
//
// KRITIK: kutuphane RevokeToken'a HAM JETONU degil, GetRefreshTokenInfo'nun
// dondurdugu tokenID'yi gecirir (bkz. zitadel/oidc pkg/op/
// token_revocation.go:48-55 ve server_legacy.go:434-444). Bu test bilerek
// RevokeToken'i tokenID ile cagirir; ham jetonla cagirmak, RevokeToken'in
// ID'yi hic bulamadigi (ve aileyi hic iptal etmedigi) bir regresyonu
// yakalamaz.
func TestRevokeTokenRefreshAileyiIptalEder(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	birinci, err := d.jetonlar.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	ikinci, err := d.jetonlar.RefreshDondur(ctx, birinci, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	// Kutuphanenin gercekte yaptigi gibi: once ID'yi ogren.
	_, tokenID, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", ikinci)
	if err != nil {
		t.Fatalf("GetRefreshTokenInfo: %v", err)
	}
	// Sonra RevokeToken'i HAM JETONLA DEGIL, bu ID ile cagir.
	if oidcErr := d.RevokeToken(ctx, tokenID, "", "makesinger-mobil"); oidcErr != nil {
		t.Fatalf("RevokeToken: %v", oidcErr)
	}
	// Hedef alinan jeton olmus olmali...
	if _, err := d.jetonlar.RefreshOku(ctx, ikinci); !errors.Is(err, ErrJetonYok) {
		t.Errorf("iptal edilen jeton hala gecerli (err=%v)", err)
	}
	// ...ve AYNI AILEDEN, iptal cagrisinda hic adi gecmeyen bir baska jeton
	// da olmus olmali: aile iptali tek jetonu degil, TUM zinciri kapatir.
	ucuncu, err := d.jetonlar.RefreshDondur(ctx, ikinci, yeniRefresh("k1"))
	// Not: ikinci zaten RevokeToken ile iptal edildigi icin RefreshDondur
	// da basarisiz olmali; bu, "aile gercekten olu mu" sorusunun ikinci
	// bir kaniti.
	if err == nil {
		t.Fatalf("iptal edilmis ailede rotasyon basarili oldu, yeni jeton = %q", ucuncu)
	}
	if !errors.Is(err, ErrJetonYok) {
		t.Errorf("beklenmeyen hata turu: %v", err)
	}
}

// RevokeToken bir access tokenID'yi hedef alirsa yalnizca o kayit
// silinmeli.
func TestRevokeTokenAccessKaydiSiler(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	if err := d.jetonlar.AccessKaydet(ctx, "at1", "k1", "makesinger-mobil", []string{"openid"}, time.Now().UTC().Add(15*time.Minute)); err != nil {
		t.Fatalf("AccessKaydet: %v", err)
	}
	if oidcErr := d.RevokeToken(ctx, "at1", "k1", "makesinger-mobil"); oidcErr != nil {
		t.Fatalf("RevokeToken: %v", oidcErr)
	}
	if _, err := d.jetonlar.AccessOku(ctx, "at1"); !errors.Is(err, ErrJetonYok) {
		t.Errorf("iptal edilen access kaydi hala gecerli (err=%v)", err)
	}
}

// RevokeToken zaten gecersiz bir jetona (ne refresh ne access) hata
// dondurmemeli: RFC 7009 iptal islemini idempotent kilar.
func TestRevokeTokenBilinmeyenJetonSessizceBasarili(t *testing.T) {
	d := testDepo(t)
	if oidcErr := d.RevokeToken(context.Background(), "uydurma", "", "makesinger-mobil"); oidcErr != nil {
		t.Errorf("RevokeToken hata dondu: %v", oidcErr)
	}
}

// SetUserinfoFromScopes deprecated: bos implementasyon beklenir, hata
// vermemeli.
func TestSetUserinfoFromScopesBos(t *testing.T) {
	d := testDepo(t)
	if err := d.SetUserinfoFromScopes(context.Background(), nil, "k1", "makesinger-mobil", []string{"profile"}); err != nil {
		t.Errorf("SetUserinfoFromScopes hata dondu: %v", err)
	}
}

// Health FAIL-CLOSED olmali: SaglikBagla hic cagrilmadiysa (testlerdeki
// gibi), /healthz'in Postgres/Redis olu iken bile "sagliklı" demesini
// engellemek icin acikca hata donmeli.
func TestHealthBaglantisizHataDoner(t *testing.T) {
	d := testDepo(t)
	if err := d.Health(context.Background()); err == nil {
		t.Error("Health, baglanti yokken basarili dondu (fail-closed olmali)")
	}
}

// GetRefreshTokenInfo, jeton BASKA bir istemciye aitse de
// op.ErrInvalidRefreshToken donmeli; clientID parametresi yok sayilirsa
// bir istemci baskasinin refresh jetonunun userID/tokenID'sini ogrenebilir.
func TestGetRefreshTokenInfoBaskaIstemcininJetonuReddedilir(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	baskaIstemci := yeniRefresh("k1")
	baskaIstemci.ClientID = "makesinger-web"
	jeton, err := d.jetonlar.RefreshOlustur(ctx, baskaIstemci)
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, _, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", jeton); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Errorf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
}

// CreateAccessAndRefreshTokens, currentRefreshToken bos oldugunda
// (authorization code akisi) yeni bir aile baslatmali: donen access ve
// refresh jetonlarinin ikisi de gecerli olmali.
func TestCreateAccessAndRefreshTokensYeniAileBaslatir(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	istek := &AuthIstek{
		ID:       "istek1",
		ClientID: "makesinger-mobil",
		Subject:  "k1",
		Scopes:   []string{"openid", "offline_access"},
		AuthTime: time.Now().UTC(),
	}
	accessID, refreshToken, exp, err := d.CreateAccessAndRefreshTokens(ctx, istek, "")
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}
	if accessID == "" || refreshToken == "" {
		t.Fatalf("bos deger dondu: accessID=%q refreshToken=%q", accessID, refreshToken)
	}
	if !exp.After(time.Now().UTC()) {
		t.Errorf("access suresi gecmiste: %v", exp)
	}
	if _, err := d.jetonlar.AccessOku(ctx, accessID); err != nil {
		t.Errorf("access kaydi okunamadi: %v", err)
	}
	if kayit, err := d.jetonlar.RefreshOku(ctx, refreshToken); err != nil {
		t.Errorf("refresh jetonu okunamadi: %v", err)
	} else if kayit.UserID != "k1" || kayit.ClientID != "makesinger-mobil" {
		t.Errorf("refresh kaydi yanlis doldu: %+v", kayit)
	}
}

// CreateAccessAndRefreshTokens, currentRefreshToken doluysa (refresh_token
// akisi) rotasyon yapmali: eski jeton olmeli, yenisi gecerli olmali, ve
// eski jeton TEKRAR sunulursa op.ErrInvalidRefreshToken (invalid_grant)
// donmeli -- yeniden kullanim tespiti Depo seviyesinde de calismali.
func TestCreateAccessAndRefreshTokensRotasyonYapar(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	eski, err := d.jetonlar.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	istek := &refreshIstek{userID: "k1", clientID: "makesinger-mobil", scopes: []string{"openid"}}
	accessID, yeniJeton, _, err := d.CreateAccessAndRefreshTokens(ctx, istek, eski)
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}
	if accessID == "" || yeniJeton == "" || yeniJeton == eski {
		t.Fatalf("rotasyon beklenen sekilde calismadi: accessID=%q yeniJeton=%q", accessID, yeniJeton)
	}
	if _, err := d.jetonlar.RefreshOku(ctx, yeniJeton); err != nil {
		t.Errorf("yeni jeton gecersiz: %v", err)
	}
	// Eski jeton TEKRAR sunulursa: calinti tespiti tetiklenmeli ve dogru
	// OAuth hatasina (invalid_grant) cevrilmeli.
	if _, _, _, err := d.CreateAccessAndRefreshTokens(ctx, istek, eski); !errors.Is(err, oidc.ErrInvalidGrant()) {
		t.Errorf("hata = %v, beklenen oidc.ErrInvalidGrant()", err)
	}
}

// refreshAkisiTaklit, kutuphanenin refresh_token akisindaki CAGRI SIRASINI
// birebir taklit eder (bkz. zitadel/oidc pkg/op/token_refresh.go:36-44):
// once ValidateRefreshTokenRequest -> ... -> TokenRequestByRefreshToken
// cagrilir ve HATA DONERSE akis ABORT eder; CreateAccessAndRefreshTokens
// (dolayisiyla RefreshDondur) HIC cagrilmaz. Testlerin bu sirayi taklit
// etmesi sart: yeniden kullanim tespiti yalnizca RefreshDondur'da
// yasarsa uretimde hic calismaz, ama RefreshDondur'u dogrudan cagiran bir
// test bunu asla goremez.
func refreshAkisiTaklit(t *testing.T, d *Depo, refreshToken string) error {
	t.Helper()
	istek, err := d.TokenRequestByRefreshToken(context.Background(), refreshToken)
	if err != nil {
		return err
	}
	_, _, _, err = d.CreateAccessAndRefreshTokens(context.Background(), istek, refreshToken)
	return err
}

// BULGU (Critical) regresyon testi: kullanilmis bir refresh jetonu
// kutuphanenin gercek cagri sirasiyla sunuldugunda AILE IPTAL EDILMELI.
// Iptal yeniden yalnizca RefreshDondur'a tasinirsa bu test FAIL eder,
// cunku akis TokenRequestByRefreshToken'in hatasinda abort eder ve
// RefreshDondur'a hic ulasilmaz.
func TestTokenRequestByRefreshTokenYenidenKullanimdaAileyiIptalEder(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	eski, err := d.jetonlar.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	// Mesru istemci normal sekilde rotasyon yapiyor: eski jeton "used"
	// olur, yeni jeton gecerlidir.
	yeni, err := d.jetonlar.RefreshDondur(ctx, eski, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	if _, err := d.jetonlar.RefreshOku(ctx, yeni); err != nil {
		t.Fatalf("rotasyon sonrasi yeni jeton gecersiz: %v", err)
	}

	// Saldirgan caldigi (artik kullanilmis) jetonu sunuyor.
	if err := refreshAkisiTaklit(t, d, eski); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}

	// Asil olculen: ailenin GERI KALANI da olmus olmali. Mesru istemcinin
	// elindeki GUNCEL jeton artik calismamali.
	if _, err := d.jetonlar.RefreshOku(ctx, yeni); !errors.Is(err, ErrJetonYok) {
		t.Errorf("aile iptal edilmemis, guncel jeton hala gecerli (err=%v)", err)
	}
	if err := refreshAkisiTaklit(t, d, yeni); err == nil {
		t.Error("iptal edilmis aileyle refresh akisi basarili oldu")
	}
	// Depodaki her kayit iptal edilmis olmali: aile toptan olur.
	for _, kayit := range d.jetonlar.(*SahteTokenStore).TumKayitlar() {
		if kayit.RevokedAt == nil {
			t.Errorf("aile uyesi iptal edilmemis: id=%s", kayit.ID)
		}
	}
}

// Hata AYRISMAMASI: saldirgan, cevaba bakarak jetonun "bilinmiyor" mu
// "kullanilmis" mi oldugunu ayirt edememeli. Ayrisirsa, elindeki bir
// degerin gercek bir jeton olup olmadigini olcebilir.
func TestTokenRequestByRefreshTokenHatasiAyrismaz(t *testing.T) {
	d := testDepo(t)
	ctx := context.Background()
	eski, err := d.jetonlar.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, err := d.jetonlar.RefreshDondur(ctx, eski, yeniRefresh("k1")); err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	_, kullanilmisHata := d.TokenRequestByRefreshToken(ctx, eski)
	_, bilinmeyenHata := d.TokenRequestByRefreshToken(ctx, "uydurma-jeton")
	if kullanilmisHata == nil || bilinmeyenHata == nil {
		t.Fatalf("iki durumda da hata beklenir: kullanilmis=%v bilinmeyen=%v", kullanilmisHata, bilinmeyenHata)
	}
	if kullanilmisHata.Error() != bilinmeyenHata.Error() {
		t.Errorf("hatalar ayrisiyor: kullanilmis=%q bilinmeyen=%q", kullanilmisHata, bilinmeyenHata)
	}
}
