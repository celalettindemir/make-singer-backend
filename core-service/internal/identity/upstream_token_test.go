package identity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Fake token endpoint: captures the request so we can verify the sent fields.
func TestExchangeCode_SendsGoogleFields(t *testing.T) {
	var captured url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("metot = %s, beklenen POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		_ = r.ParseForm()
		captured = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"sahte.jwt.imza"}`))
	}))
	defer server.Close()

	y := &Upstream{Name: providerGoogle, ClientID: "ist", ClientSecret: "sir", TokenURL: server.URL, SupportsPKCE: true}
	idToken, err := y.ExchangeCode(context.Background(), server.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
	if err != nil {
		t.Fatalf("degisim basarisiz: %v", err)
	}
	if idToken != "sahte.jwt.imza" {
		t.Errorf("id_token = %q", idToken)
	}
	for field, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "kod-1",
		"redirect_uri":  "https://biz/geri",
		"client_id":     "ist",
		"client_secret": "sir",
		"code_verifier": "dogrulayici-1",
	} {
		if captured.Get(field) != want {
			t.Errorf("%s = %q, beklenen %q", field, captured.Get(field), want)
		}
	}
}

// code_verifier must NOT be sent to a provider that does not support PKCE (K4).
func TestExchangeCode_NoVerifierWithoutPKCE(t *testing.T) {
	var captured url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		captured = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"sahte.jwt.imza"}`))
	}))
	defer server.Close()

	y := &Upstream{Name: providerGoogle, ClientID: "ist", ClientSecret: "sir", TokenURL: server.URL, SupportsPKCE: false}
	if _, err := y.ExchangeCode(context.Background(), server.Client(), "kod-1", "dogrulayici-1", "https://biz/geri"); err != nil {
		t.Fatalf("degisim basarisiz: %v", err)
	}
	if captured.Get("code_verifier") != "" {
		t.Errorf("code_verifier = %q, PKCE desteklemeyen saglayiciya gonderilmemeli", captured.Get("code_verifier"))
	}
}

// If id_token does NOT come back, the flow must stop. Some providers
// answer errors with HTTP 200 and {"error":...}; counting the 200 as
// success without looking at the body means sending an empty id_token to
// verification.
func TestExchangeCode_MissingIDTokenFails(t *testing.T) {
	statuses := map[string]string{
		"id_token alani yok":   `{"access_token":"x"}`,
		"200 ile hata govdesi": `{"error":"invalid_grant"}`,
	}
	for name, body := range statuses {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			y := &Upstream{Name: providerGoogle, ClientID: "ist", ClientSecret: "sir", TokenURL: server.URL, SupportsPKCE: true}
			idToken, err := y.ExchangeCode(context.Background(), server.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
			if !errors.Is(err, ErrUpstreamResponse) {
				t.Errorf("err = %v, beklenen ErrYukariAkisYanit", err)
			}
			if idToken != "" {
				t.Errorf("idToken = %q, beklenen bos", idToken)
			}
		})
	}
}

func TestExchangeCode_HTTPErrorNotSwallowed(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"bir_sorun"}`))
			}))
			defer server.Close()

			y := &Upstream{Name: providerGoogle, ClientID: "ist", ClientSecret: "sir", TokenURL: server.URL, SupportsPKCE: true}
			idToken, err := y.ExchangeCode(context.Background(), server.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
			if err == nil {
				t.Fatal("hata bekleniyordu, nil geldi")
			}
			if idToken != "" {
				t.Errorf("idToken = %q, beklenen bos", idToken)
			}
		})
	}
}

// If the upstream does not answer, the flow must NOT WAIT FOREVER: a
// request without a timeout piles up goroutines holding the login endpoint.
func TestExchangeCode_AppliesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"gecikmis"}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	y := &Upstream{Name: providerGoogle, ClientID: "ist", ClientSecret: "sir", TokenURL: server.URL, SupportsPKCE: true}
	began := time.Now()
	_, err := y.ExchangeCode(ctx, server.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
	if err == nil {
		t.Fatal("zaman asimi hatasi bekleniyordu, nil geldi")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, beklenen context.DeadlineExceeded zincirinde", err)
	}
	if time.Since(began) > 1*time.Second {
		t.Errorf("JetonDegis cok uzun surdu: %v", time.Since(began))
	}
}
