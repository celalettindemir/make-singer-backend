package kimlik

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Sahte token ucu: istegi yakalar ki gonderilen alanlari dogrulayabilelim.
func TestJetonDegisGoogleAlanlariGonderir(t *testing.T) {
	var alinan url.Values
	sunucu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("metot = %s, beklenen POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		_ = r.ParseForm()
		alinan = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"sahte.jwt.imza"}`))
	}))
	defer sunucu.Close()

	y := &YukariAkis{Ad: saglayiciGoogle, IstemciID: "ist", IstemciSir: "sir", TokenURL: sunucu.URL, PKCEDestekli: true}
	idToken, err := y.JetonDegis(context.Background(), sunucu.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
	if err != nil {
		t.Fatalf("degisim basarisiz: %v", err)
	}
	if idToken != "sahte.jwt.imza" {
		t.Errorf("id_token = %q", idToken)
	}
	for alan, beklenen := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "kod-1",
		"redirect_uri":  "https://biz/geri",
		"client_id":     "ist",
		"client_secret": "sir",
		"code_verifier": "dogrulayici-1",
	} {
		if alinan.Get(alan) != beklenen {
			t.Errorf("%s = %q, beklenen %q", alan, alinan.Get(alan), beklenen)
		}
	}
}

// PKCE desteklemeyen saglayiciya code_verifier GONDERILMEMELI (K4).
func TestJetonDegisPKCEsizSaglayiciyaDogrulayiciGondermez(t *testing.T) {
	var alinan url.Values
	sunucu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		alinan = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"sahte.jwt.imza"}`))
	}))
	defer sunucu.Close()

	y := &YukariAkis{Ad: saglayiciGoogle, IstemciID: "ist", IstemciSir: "sir", TokenURL: sunucu.URL, PKCEDestekli: false}
	if _, err := y.JetonDegis(context.Background(), sunucu.Client(), "kod-1", "dogrulayici-1", "https://biz/geri"); err != nil {
		t.Fatalf("degisim basarisiz: %v", err)
	}
	if alinan.Get("code_verifier") != "" {
		t.Errorf("code_verifier = %q, PKCE desteklemeyen saglayiciya gonderilmemeli", alinan.Get("code_verifier"))
	}
}

// id_token DONMEZSE akis durmali. Bazi saglayicilar hata durumunda
// HTTP 200 ile {"error":...} doner; govdeye bakmadan 200'u basari
// saymak, bos bir id_token'i dogrulamaya gondermek demektir.
func TestJetonDegisIdTokenYokHataVerir(t *testing.T) {
	durumlar := map[string]string{
		"id_token alani yok":   `{"access_token":"x"}`,
		"200 ile hata govdesi": `{"error":"invalid_grant"}`,
	}
	for ad, govde := range durumlar {
		t.Run(ad, func(t *testing.T) {
			sunucu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(govde))
			}))
			defer sunucu.Close()

			y := &YukariAkis{Ad: saglayiciGoogle, IstemciID: "ist", IstemciSir: "sir", TokenURL: sunucu.URL, PKCEDestekli: true}
			idToken, err := y.JetonDegis(context.Background(), sunucu.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
			if !errors.Is(err, ErrYukariAkisYanit) {
				t.Errorf("err = %v, beklenen ErrYukariAkisYanit", err)
			}
			if idToken != "" {
				t.Errorf("idToken = %q, beklenen bos", idToken)
			}
		})
	}
}

func TestJetonDegisHTTPHatasiYutulmaz(t *testing.T) {
	for _, durum := range []int{http.StatusInternalServerError, http.StatusBadRequest} {
		t.Run(http.StatusText(durum), func(t *testing.T) {
			sunucu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(durum)
				_, _ = w.Write([]byte(`{"error":"bir_sorun"}`))
			}))
			defer sunucu.Close()

			y := &YukariAkis{Ad: saglayiciGoogle, IstemciID: "ist", IstemciSir: "sir", TokenURL: sunucu.URL, PKCEDestekli: true}
			idToken, err := y.JetonDegis(context.Background(), sunucu.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
			if err == nil {
				t.Fatal("hata bekleniyordu, nil geldi")
			}
			if idToken != "" {
				t.Errorf("idToken = %q, beklenen bos", idToken)
			}
		})
	}
}

// Yukari akis cevap vermezse akis SONSUZA KADAR BEKLEMEMELI: zaman
// asimi olmayan bir istek, giris ucunu tutan goroutine'leri biriktirir.
func TestJetonDegisZamanAsimiUygular(t *testing.T) {
	sunucu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id_token":"gecikmis"}`))
	}))
	defer sunucu.Close()

	ctx, iptal := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer iptal()

	y := &YukariAkis{Ad: saglayiciGoogle, IstemciID: "ist", IstemciSir: "sir", TokenURL: sunucu.URL, PKCEDestekli: true}
	basla := time.Now()
	_, err := y.JetonDegis(ctx, sunucu.Client(), "kod-1", "dogrulayici-1", "https://biz/geri")
	if err == nil {
		t.Fatal("zaman asimi hatasi bekleniyordu, nil geldi")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, beklenen context.DeadlineExceeded zincirinde", err)
	}
	if time.Since(basla) > 1*time.Second {
		t.Errorf("JetonDegis cok uzun surdu: %v", time.Since(basla))
	}
}
