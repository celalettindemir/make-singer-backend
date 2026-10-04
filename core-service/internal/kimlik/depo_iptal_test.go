package kimlik

import (
	"context"
	"errors"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// izlemeTokenStore, KullaniciIptal'in cagrilip cagrilmadigini izler.
type izlemeTokenStore struct {
	*SahteTokenStore
	kullaniciIptalSayisi int
}

func (s *izlemeTokenStore) KullaniciIptal(ctx context.Context, userID, clientID string) error {
	s.kullaniciIptalSayisi++
	return s.SahteTokenStore.KullaniciIptal(ctx, userID, clientID)
}

// testDepoJetonlu, depo_test.go'daki testDepo'nun belirli bir
// TokenStore ile kurulan surumudur (orada sabit NewSahteTokenStore
// kullanilir).
func testDepoJetonlu(t *testing.T, jetonlar TokenStore) *Depo {
	t.Helper()
	anahtar, err := AnahtarYukle(testPEM(t))
	if err != nil {
		t.Fatalf("AnahtarYukle: %v", err)
	}
	return NewDepo(testAuthCfg(), NewSahteUserStore(), jetonlar, nil, anahtar)
}

// M2: parametresiz GET /end_session, bos userID/clientID ile
// TerminateSession'a dusuyor ve Postgres'e bos dizgeyi uuid olarak
// gonderiyordu: 500 + "invalid input syntax for type uuid" (SQLSTATE
// 22P02) + HER istekte bir ERROR log satiri (log kirliligi + kucuk
// amplifikasyon). Bos kimlikle iptal edilecek jeton zaten yok, bu
// yuzden erken donmek dogru.
func TestTerminateSessionBosKimlikleDepoyaDokunmaz(t *testing.T) {
	izleme := &izlemeTokenStore{SahteTokenStore: NewSahteTokenStore()}
	d := testDepoJetonlu(t, izleme)
	ctx := context.Background()

	durumlar := []struct{ userID, clientID string }{
		{"", ""},
		{"", "makesinger-mobil"},
		{"k1", ""},
	}
	for _, c := range durumlar {
		if err := d.TerminateSession(ctx, c.userID, c.clientID); err != nil {
			t.Errorf("TerminateSession(%q, %q) = %v, beklenen nil", c.userID, c.clientID, err)
		}
	}
	if izleme.kullaniciIptalSayisi != 0 {
		t.Errorf("bos kimlikle depo %d kez cagrildi, beklenen 0", izleme.kullaniciIptalSayisi)
	}

	// NEGATIF KONTROL: koruma, GECERLI cagriyi da kesmesin.
	if err := d.TerminateSession(ctx, "k1", "makesinger-mobil"); err != nil {
		t.Fatalf("gecerli TerminateSession = %v", err)
	}
	if izleme.kullaniciIptalSayisi != 1 {
		t.Errorf("gecerli cagri sonrasi sayac = %d, beklenen 1", izleme.kullaniciIptalSayisi)
	}
}

// M7: GetRefreshTokenInfo, ErrJetonTekrar gorunce aileyi IPTAL
// ETMIYORDU. Somurulebilir degildi (saldirgan /revoke akisinda jeton
// kazanmiyor), ama mimarinin tamami "yeniden kullanim => aileyi oldur"
// ilkesine dayaniyorken bir tespit kanali sessizce kayboluyordu.
func TestGetRefreshTokenInfoYenidenKullanimdaAileyiIptalEder(t *testing.T) {
	depo := NewSahteTokenStore()
	d := testDepoJetonlu(t, depo)
	ctx := context.Background()

	eski, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	yeni, err := depo.RefreshDondur(ctx, eski, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshDondur: %v", err)
	}
	// Rotasyondan sonra yeni jeton gecerli olmali (aile henuz canli).
	if _, err := depo.RefreshOku(ctx, yeni); err != nil {
		t.Fatalf("rotasyon sonrasi yeni jeton gecersiz: %v", err)
	}

	// KULLANILMIS jetonu sun: kutuphane sozlesmesi geregi
	// op.ErrInvalidRefreshToken donmeli (davranis DEGISMEMELI; /revoke
	// RFC 7009 §2.2 geregi 200 donmeye devam etsin).
	if _, _, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", eski); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}

	// ... ve AILE iptal edilmis olmali: yeni jeton da artik gecersiz.
	if _, err := depo.RefreshOku(ctx, yeni); err == nil {
		t.Error("yeniden kullanimdan sonra aile iptal edilmedi: yeni jeton hala gecerli")
	}
}

// NEGATIF KONTROL: aile iptali YALNIZCA yeniden kullanimda olmali.
// Bilinmeyen bir jeton icin hicbir aile olmemeli, aksi halde yukaridaki
// test "her cagri aileyi olduruyor" durumunda da gecerdi.
func TestGetRefreshTokenInfoBilinmeyenJetonAileyiIptalEtmez(t *testing.T) {
	depo := NewSahteTokenStore()
	d := testDepoJetonlu(t, depo)
	ctx := context.Background()

	gecerli, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
	if err != nil {
		t.Fatalf("RefreshOlustur: %v", err)
	}
	if _, _, err := d.GetRefreshTokenInfo(ctx, "makesinger-mobil", "boyle-bir-jeton-yok"); !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("hata = %v, beklenen op.ErrInvalidRefreshToken", err)
	}
	if _, err := depo.RefreshOku(ctx, gecerli); err != nil {
		t.Errorf("bilinmeyen jeton yuzunden gecerli jeton iptal edildi: %v", err)
	}
}

// GECERLI bir refresh jetonu icin GetRefreshTokenInfo hala dogru
// bilgiyi dondurmeli: M7 duzeltmesi mutlu yolu kirmasin.
func TestGetRefreshTokenInfoGecerliJetonuDondurur(t *testing.T) {
	depo := NewSahteTokenStore()
	d := testDepoJetonlu(t, depo)
	ctx := context.Background()

	jeton, err := depo.RefreshOlustur(ctx, yeniRefresh("k1"))
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
		t.Error("tokenID bos dondu")
	}
}

// M3: discovery belgesi YALNIZCA gercekten destekledigimiz akislari
// ilan etmeli. Kutuphane (v3.51.8) bu alanlari yapilandirmadan okumaz,
// SABIT uretir; op.Config'de kisitlayan bir alan YOK, bu yuzden belgeyi
// yayindan once duzeltiyoruz.
func TestDiscoveryDuzeltDesteklenmeyenAkislariAyiklar(t *testing.T) {
	// Kutuphanenin urettigi belgeyi taklit et.
	belge := &oidc.DiscoveryConfiguration{
		ResponseTypesSupported: []string{"code", "id_token", "id_token token"},
		GrantTypesSupported: []oidc.GrantType{
			oidc.GrantTypeCode,
			oidc.GrantTypeImplicit,
			oidc.GrantTypeRefreshToken,
			oidc.GrantTypeBearer,
		},
		DeviceAuthorizationEndpoint: "https://ornek.dev/device_authorization",
		// Duzeltmenin DOKUNMAMASI gereken bir alan.
		CodeChallengeMethodsSupported: []oidc.CodeChallengeMethod{oidc.CodeChallengeMethodS256},
	}
	discoveryDuzelt(belge)

	if len(belge.ResponseTypesSupported) != 1 || belge.ResponseTypesSupported[0] != "code" {
		t.Errorf("response_types_supported = %v, beklenen [code]", belge.ResponseTypesSupported)
	}
	for _, yasak := range []oidc.GrantType{oidc.GrantTypeImplicit, oidc.GrantTypeBearer, oidc.GrantTypeDeviceCode} {
		for _, g := range belge.GrantTypesSupported {
			if g == yasak {
				t.Errorf("grant_types_supported desteklenmeyen %q iceriyor: %v", yasak, belge.GrantTypesSupported)
			}
		}
	}
	// Destekledigimiz ikisi KALMALI (ayiklama fazla kesmesin).
	for _, gerekli := range []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken} {
		bulundu := false
		for _, g := range belge.GrantTypesSupported {
			if g == gerekli {
				bulundu = true
			}
		}
		if !bulundu {
			t.Errorf("grant_types_supported %q icermiyor: %v", gerekli, belge.GrantTypesSupported)
		}
	}
	if belge.DeviceAuthorizationEndpoint != "" {
		t.Errorf("device_authorization_endpoint = %q, beklenen bos", belge.DeviceAuthorizationEndpoint)
	}
	if len(belge.CodeChallengeMethodsSupported) != 1 {
		t.Errorf("code_challenge_methods_supported bozuldu: %v", belge.CodeChallengeMethodsSupported)
	}
}
