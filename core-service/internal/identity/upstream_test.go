package identity

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// The state record is SINGLE-USE. If it could be read twice, an attacker
// could replay a callback URL they captured.
func TestRequestStore_StateSingleUse(t *testing.T) {
	rdb := testRedis(t)
	d := NewRequestStore(rdb)
	ctx := context.Background()

	requested := FederationState{
		AuthRequestID: "istek-1",
		Nonce:         "nonce-1",
		Provider:      "google",
		Cookie:        "cerez-degeri",
		CodeVerifier:  "dogrulayici-1",
	}
	if err := d.WriteState(ctx, "state-1", requested); err != nil {
		t.Fatalf("could not write state: %v", err)
	}
	readBack, err := d.TakeState(ctx, "state-1")
	if err != nil {
		t.Fatalf("could not read state: %v", err)
	}
	if readBack != requested {
		t.Errorf("read = %+v, want %+v", readBack, requested)
	}
	if _, err := d.TakeState(ctx, "state-1"); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("second read err = %v, want ErrStateNotFound", err)
	}
}

func TestRequestStore_UnknownStateNotFound(t *testing.T) {
	rdb := testRedis(t)
	d := NewRequestStore(rdb)
	if _, err := d.TakeState(context.Background(), "hic-yazilmadi"); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("err = %v, want ErrStateNotFound", err)
	}
}

func TestUpstream_AuthorizeURLGoogle(t *testing.T) {
	y := &Upstream{
		Name:         "google",
		ClientID:     "istemci-123",
		AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		Scopes:       []string{"openid", "email", "profile"},
		SupportsPKCE: true,
	}
	u, err := url.Parse(y.AuthorizeURL("st", "nn", "dogrulayici-en-az-43-karakter-olmali-ki-gecerli-olsun", "https://biz/federasyon/google/geri"))
	if err != nil {
		t.Fatalf("could not parse URL: %v", err)
	}
	q := u.Query()
	for field, want := range map[string]string{
		"client_id":             "istemci-123",
		"response_type":         "code",
		"scope":                 "openid email profile",
		"state":                 "st",
		"nonce":                 "nn",
		"redirect_uri":          "https://biz/federasyon/google/geri",
		"code_challenge_method": "S256",
	} {
		if q.Get(field) != want {
			t.Errorf("%s = %q, want %q", field, q.Get(field), want)
		}
	}
	// The S256 digest is sent, not the verifier ITSELF.
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge is empty")
	}
	if strings.Contains(u.String(), "dogrulayici-en-az-43") {
		t.Error("the code verifier was sent in plain text in the URL")
	}
}

// K4: Apple's PKCE support is not documented; sending an unknown
// parameter risks breaking the flow.
func TestUpstream_AuthorizeURLAppleOmitsPKCE(t *testing.T) {
	y := &Upstream{
		Name: "apple", ClientID: "tr.makesinger.giris",
		AuthURL:      "https://appleid.apple.com/auth/authorize",
		Scopes:       []string{"openid", "email", "name"},
		SupportsPKCE: false, FormPost: true,
	}
	u, _ := url.Parse(y.AuthorizeURL("st", "nn", "dogrulayici", "https://biz/federasyon/apple/geri"))
	q := u.Query()
	if q.Get("code_challenge") != "" || q.Get("code_challenge_method") != "" {
		t.Error("a PKCE parameter was sent to Apple")
	}
	// When the name/email scope is requested, Apple returns with form_post;
	// we ask for it EXPLICITLY because the callback handler has to expect
	// a POST (see K2).
	if q.Get("response_mode") != "form_post" {
		t.Errorf("response_mode = %q, want form_post", q.Get("response_mode"))
	}
}

// TestGenerateCodeVerifier: generateCodeVerifier must produce a long
// enough verifier and its S256 challenge; neither may be empty and
// different calls must give different values (a constant/predictable
// value would make PKCE ineffective).
func TestGenerateCodeVerifier(t *testing.T) {
	d1, m1, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("generateCodeVerifier: %v", err)
	}
	if len(d1) < 43 {
		t.Errorf("verifier length = %d, should be at least 43", len(d1))
	}
	if m1 == "" {
		t.Error("challenge is empty")
	}
	d2, _, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("second generateCodeVerifier: %v", err)
	}
	if d1 == d2 {
		t.Error("two calls produced the same verifier")
	}
}
