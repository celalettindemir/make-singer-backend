package kimlik

import (
	"context"
	"errors"
	"testing"
	"time"

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
	if oidcErr := d.RevokeToken(ctx, ikinci, "", "makesinger-mobil"); oidcErr != nil {
		t.Fatalf("RevokeToken: %v", oidcErr)
	}
	if _, err := d.jetonlar.RefreshOku(ctx, ikinci); !errors.Is(err, ErrJetonYok) {
		t.Errorf("iptal edilen jeton hala gecerli (err=%v)", err)
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

// Health, Postgres/Redis baglanmadiginda (testlerdeki gibi) hata
// dondurmemeli.
func TestHealthBaglantisizBasarili(t *testing.T) {
	d := testDepo(t)
	if err := d.Health(context.Background()); err != nil {
		t.Errorf("Health hata dondu: %v", err)
	}
}
