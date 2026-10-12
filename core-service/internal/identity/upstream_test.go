package identity

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// Durum kaydi TEK KULLANIMLIKTIR. Iki kez okunabilirse saldirgan
// yakaladigi bir callback URL'ini tekrar oynatabilir.
func TestDurumTekKullanimlik(t *testing.T) {
	rdb := testRedis(t)
	d := NewRequestStore(rdb)
	ctx := context.Background()

	istenen := FederationState{
		AuthRequestID: "istek-1",
		Nonce:         "nonce-1",
		Provider:      "google",
		Cookie:        "cerez-degeri",
		CodeVerifier:  "dogrulayici-1",
	}
	if err := d.WriteState(ctx, "state-1", istenen); err != nil {
		t.Fatalf("durum yazilamadi: %v", err)
	}
	okunan, err := d.TakeState(ctx, "state-1")
	if err != nil {
		t.Fatalf("durum okunamadi: %v", err)
	}
	if okunan != istenen {
		t.Errorf("okunan = %+v, beklenen %+v", okunan, istenen)
	}
	if _, err := d.TakeState(ctx, "state-1"); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("ikinci okuma err = %v, beklenen ErrDurumYok", err)
	}
}

func TestBilinmeyenDurumErrDurumYok(t *testing.T) {
	rdb := testRedis(t)
	d := NewRequestStore(rdb)
	if _, err := d.TakeState(context.Background(), "hic-yazilmadi"); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("err = %v, beklenen ErrDurumYok", err)
	}
}

func TestAuthorizeURLGoogle(t *testing.T) {
	y := &Upstream{
		Name:         "google",
		ClientID:     "istemci-123",
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		Scopes:       []string{"openid", "email", "profile"},
		SupportsPKCE: true,
	}
	u, err := url.Parse(y.AuthorizeURL("st", "nn", "dogrulayici-en-az-43-karakter-olmali-ki-gecerli-olsun", "https://biz/federasyon/google/geri"))
	if err != nil {
		t.Fatalf("URL cozumlenemedi: %v", err)
	}
	q := u.Query()
	for alan, beklenen := range map[string]string{
		"client_id":             "istemci-123",
		"response_type":         "code",
		"scope":                 "openid email profile",
		"state":                 "st",
		"nonce":                 "nn",
		"redirect_uri":          "https://biz/federasyon/google/geri",
		"code_challenge_method": "S256",
	} {
		if q.Get(alan) != beklenen {
			t.Errorf("%s = %q, beklenen %q", alan, q.Get(alan), beklenen)
		}
	}
	// Dogrulayicinin KENDISI degil, S256 ozeti gonderilir.
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge bos")
	}
	if strings.Contains(u.String(), "dogrulayici-en-az-43") {
		t.Error("kod dogrulayicisi URL'de duz metin gonderilmis")
	}
}

// K4: Apple'in PKCE destegi dokumante degil; bilinmeyen parametre
// gondermek akisi kirma riskidir.
func TestAuthorizeURLApplePKCEGondermez(t *testing.T) {
	y := &Upstream{
		Name: "apple", ClientID: "tr.makesinger.giris",
		AuthURL:      "https://appleid.apple.com/auth/authorize",
		Scopes:       []string{"openid", "email", "name"},
		SupportsPKCE: false, FormPost: true,
	}
	u, _ := url.Parse(y.AuthorizeURL("st", "nn", "dogrulayici", "https://biz/federasyon/apple/geri"))
	q := u.Query()
	if q.Get("code_challenge") != "" || q.Get("code_challenge_method") != "" {
		t.Error("Apple'a PKCE parametresi gonderilmis")
	}
	// name/email scope'u istenince Apple form_post ile doner; bunu
	// ACIKCA istiyoruz, cunku geri donus handler'i POST'u beklemek
	// zorunda (bkz. K2).
	if q.Get("response_mode") != "form_post" {
		t.Errorf("response_mode = %q, beklenen form_post", q.Get("response_mode"))
	}
}

// kodDogrulayiciUret, PKCE icin yeterince uzun bir dogrulayici ve onun
// S256 meydan okumasini uretmeli; ikisi de bos olmamali ve farkli
// cagrilar farkli degerler vermeli (sabit/tahmin edilebilir deger
// PKCE'yi etkisiz kilar).
func TestKodDogrulayiciUret(t *testing.T) {
	d1, m1, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("kodDogrulayiciUret: %v", err)
	}
	if len(d1) < 43 {
		t.Errorf("dogrulayici uzunlugu = %d, en az 43 olmali", len(d1))
	}
	if m1 == "" {
		t.Error("meydan okuma bos")
	}
	d2, _, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("ikinci kodDogrulayiciUret: %v", err)
	}
	if d1 == d2 {
		t.Error("iki cagri ayni dogrulayiciyi uretti")
	}
}
