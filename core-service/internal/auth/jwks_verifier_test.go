package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// --- yardimcilar -----------------------------------------------------------

// kidIcin, bir RSA anahtarindan deterministik bir "kid" uretir: ayni anahtar
// hem JWKS'te yayinlanirken hem de token imzalanirken ayni kid'i tasimali,
// aksi halde keyfunc dogru anahtari secemez.
func kidIcin(anahtar *rsa.PrivateKey) string {
	toplam := sha256.Sum256(anahtar.N.Bytes())
	return base64.RawURLEncoding.EncodeToString(toplam[:8])
}

func rsaJWK(anahtar *rsa.PrivateKey, kid string) map[string]any {
	pub := anahtar.PublicKey
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// sahteJWKS, verilen anahtarlari yayinlayan bir JWKS ucu acar ve URL'ini
// doner. anahtarlar degistirilirse SONRAKI istek yeni kumeyi gorur --
// rotasyon testi bunu kullanir.
func sahteJWKS(t *testing.T, anahtarlar func() []map[string]any) string {
	t.Helper()
	sunucu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": anahtarlar()})
	}))
	t.Cleanup(sunucu.Close)
	return sunucu.URL
}

// sahteJWKSRSA, sahteJWKS'in yalnizca RSA anahtar listesi alan kisayolu.
func sahteJWKSRSA(t *testing.T, anahtarlar func() []*rsa.PrivateKey) string {
	return sahteJWKS(t, func() []map[string]any {
		var kumeler []map[string]any
		for _, a := range anahtarlar() {
			kumeler = append(kumeler, rsaJWK(a, kidIcin(a)))
		}
		return kumeler
	})
}

// tokenUret, verilen RSA anahtariyla RS256 imzali bir id_token uretir.
func tokenUret(t *testing.T, anahtar *rsa.PrivateKey, iss, aud, nonce string) string {
	t.Helper()
	simdi := time.Now()
	talepler := jwt.MapClaims{
		"iss":   iss,
		"aud":   aud,
		"sub":   "kullanici-1",
		"nonce": nonce,
		"iat":   simdi.Unix(),
		"exp":   simdi.Add(time.Hour).Unix(),
	}
	j := jwt.NewWithClaims(jwt.SigningMethodRS256, talepler)
	j.Header["kid"] = kidIcin(anahtar)
	s, err := j.SignedString(anahtar)
	if err != nil {
		t.Fatalf("token imzalanamadi: %v", err)
	}
	return s
}

// --- Step 1: EsnekBool (K8) -------------------------------------------------

func TestEsnekBoolStringVeBoolKabulEder(t *testing.T) {
	for _, d := range []struct {
		ad       string
		girdi    string
		beklenen bool
	}{
		{"bool true", `{"v":true}`, true},
		{"bool false", `{"v":false}`, false},
		{"string true", `{"v":"true"}`, true}, // Apple boyle gonderebilir
		{"string false", `{"v":"false"}`, false},
	} {
		t.Run(d.ad, func(t *testing.T) {
			var s struct{ V EsnekBool }
			if err := json.Unmarshal([]byte(d.girdi), &s); err != nil {
				t.Fatalf("cozumlenemedi: %v", err)
			}
			if bool(s.V) != d.beklenen {
				t.Errorf("= %v, beklenen %v", bool(s.V), d.beklenen)
			}
		})
	}
}

// Apple'in string gonderme ihtimali bool'u ELDEN CIKARMAMALI: bozuk bir
// deger sessizce false'a dusmemeli, HATA vermeli -- aksi halde
// "email_verified: bozuk" gelen bir token dogrulanmis sayilmaz ama
// akis da neden kirildigini soylemez.
func TestEsnekBoolBozukDegerHataVerir(t *testing.T) {
	var s struct{ V EsnekBool }
	if err := json.Unmarshal([]byte(`{"v":"belki"}`), &s); err == nil {
		t.Error("bozuk deger kabul edildi")
	}
	if err := json.Unmarshal([]byte(`{"v":3}`), &s); err == nil {
		t.Error("sayi kabul edildi")
	}
}

// --- Step 2: alg sabitleme ve nonce testleri ---------------------------------

const (
	denemeIssuer  = "https://ornek"
	denemeIstemci = "istemci"
)

// TestAlgSabitlenir, RS256 disinda bir algoritmayla imzalanmis bir token'in
// kabul edilmedigini dogrular.
//
// Saf "HS256 ile imzala ve reddedildigini gor" testi yeterli DEGILDIR:
// jwt/v5'in anahtar tipi kontrolu (keyfunc *rsa.PublicKey dondurur, HS256
// Verify() []byte bekler) bunu WithValidMethods'suz da yakalar ve mutasyon
// testinde hicbir sey kanitlamaz. Bunun yerine, saglayicinin JWKS'inin ELE
// GECIRILDIGI (DNS/cache zehirlenmesi) senaryoyu modelliyoruz: sahte JWKS,
// RSA anahtarinin yaninda SIMETRIK ("oct") bir anahtar da yayinliyor ve
// keyfunc/jwkset bunu GERCEKTEN bir []byte sirra cozumleyebiliyor (Private:
// true ile fetch edildigi icin). WithValidMethods olmadan bu sir ile
// imzalanmis bir HS256 token KABUL EDILIR; pinleme onu alg adina bakarak,
// anahtar tipine hic bakmadan reddeder.
func TestAlgSabitlenir(t *testing.T) {
	rsaAnahtar, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa anahtari uretilemedi: %v", err)
	}
	octKid := "oct-anahtar"
	octSir := []byte("saldirganin-ele-gecirdigi-jwks-sirri-0123456789")

	url := sahteJWKS(t, func() []map[string]any {
		return []map[string]any{
			rsaJWK(rsaAnahtar, kidIcin(rsaAnahtar)),
			{
				"kty": "oct",
				"kid": octKid,
				"alg": "HS256",
				"k":   base64.RawURLEncoding.EncodeToString(octSir),
			},
		}
	})

	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	v, err := NewYukariAkisDogrulayici(ctx, url, denemeIssuer, denemeIstemci)
	if err != nil {
		t.Fatalf("dogrulayici kurulamadi: %v", err)
	}
	defer v.Close()

	// Sagliksiz kontrol: dogru RS256 token gecmeli.
	iyiToken := tokenUret(t, rsaAnahtar, denemeIssuer, denemeIstemci, "n0")
	if _, err := v.DogrulaNonce(iyiToken, "n0"); err != nil {
		t.Fatalf("gecerli RS256 token reddedildi: %v", err)
	}

	// HS256 ile, ele gecirilmis JWKS'teki oct sirriyla imzalanmis saldirgan
	// token'i.
	simdi := time.Now()
	kotuToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":   denemeIssuer,
		"aud":   denemeIstemci,
		"sub":   "saldirgan",
		"nonce": "n-saldiri",
		"iat":   simdi.Unix(),
		"exp":   simdi.Add(time.Hour).Unix(),
	})
	kotuToken.Header["kid"] = octKid
	imzaliKotu, err := kotuToken.SignedString(octSir)
	if err != nil {
		t.Fatalf("saldirgan token imzalanamadi: %v", err)
	}

	if _, err := v.DogrulaNonce(imzaliKotu, "n-saldiri"); err == nil {
		t.Error("HS256 ile imzalanmis token kabul edildi: alg sabitleme calismiyor")
	}

	// "none" algoritmasi da ayni sekilde reddedilmeli.
	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss":   denemeIssuer,
		"aud":   denemeIstemci,
		"sub":   "saldirgan2",
		"nonce": "n-saldiri2",
		"iat":   simdi.Unix(),
		"exp":   simdi.Add(time.Hour).Unix(),
	})
	imzasiz, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("imzasiz token uretilemedi: %v", err)
	}
	if _, err := v.DogrulaNonce(imzasiz, "n-saldiri2"); err == nil {
		t.Error("none algoritmali token kabul edildi")
	}
}

func TestNonceEslesmezseReddedilir(t *testing.T) {
	anahtar, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := sahteJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{anahtar} })

	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	v, err := NewYukariAkisDogrulayici(ctx, url, denemeIssuer, denemeIstemci)
	if err != nil {
		t.Fatalf("dogrulayici kurulamadi: %v", err)
	}
	defer v.Close()

	// Dogru imzali, dogru iss/aud'lu ama nonce'u BASKA bir token
	// DogrulaNonce'tan hata almali: aksi halde saldirgan kendi
	// yetkilendirmesinden aldigi gecerli bir id_token'i kurbanin akisina
	// enjekte edebilir (token replay).
	token := tokenUret(t, anahtar, denemeIssuer, denemeIstemci, "saldirganin-nonce-u")
	if _, err := v.DogrulaNonce(token, "kurbanin-bekledigi-nonce"); err == nil {
		t.Error("eslesmeyen nonce kabul edildi")
	}
}

func TestNonceBosOlamaz(t *testing.T) {
	anahtar, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := sahteJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{anahtar} })

	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	v, err := NewYukariAkisDogrulayici(ctx, url, denemeIssuer, denemeIstemci)
	if err != nil {
		t.Fatalf("dogrulayici kurulamadi: %v", err)
	}
	defer v.Close()

	// Hem token'daki hem beklenen nonce bos ise "esit" sayilmamali: nonce'u
	// hic gondermeyen bir saglayici yanitinin replay korumasi yoktur ve
	// bunu sessizce kabul etmek korumayi kaldirir.
	token := tokenUret(t, anahtar, denemeIssuer, denemeIstemci, "")
	if _, err := v.DogrulaNonce(token, ""); err == nil {
		t.Error("bos nonce kabul edildi")
	}
}

func TestIssuerVeAudienceZorunlu(t *testing.T) {
	anahtar, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := sahteJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{anahtar} })

	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	v, err := NewYukariAkisDogrulayici(ctx, url, denemeIssuer, denemeIstemci)
	if err != nil {
		t.Fatalf("dogrulayici kurulamadi: %v", err)
	}
	defer v.Close()

	t.Run("baska iss reddedilir", func(t *testing.T) {
		token := tokenUret(t, anahtar, "https://baska-saglayici.ornek", denemeIstemci, "n1")
		if _, err := v.DogrulaNonce(token, "n1"); err == nil {
			t.Error("baska issuer'li token kabul edildi")
		}
	})

	t.Run("baska aud reddedilir", func(t *testing.T) {
		// aud bizim client_id'mizi icermiyorsa reddedilir -- yani baska bir
		// uygulamaya verilmis bir Google id_token'i bize giris saglamaz.
		token := tokenUret(t, anahtar, denemeIssuer, "baska-istemci", "n2")
		if _, err := v.DogrulaNonce(token, "n2"); err == nil {
			t.Error("baska audience'li token kabul edildi")
		}
	})
}

func TestSuresiDolmusTokenReddedilir(t *testing.T) {
	anahtar, _ := rsa.GenerateKey(rand.Reader, 2048)
	url := sahteJWKSRSA(t, func() []*rsa.PrivateKey { return []*rsa.PrivateKey{anahtar} })

	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	v, err := NewYukariAkisDogrulayici(ctx, url, denemeIssuer, denemeIstemci)
	if err != nil {
		t.Fatalf("dogrulayici kurulamadi: %v", err)
	}
	defer v.Close()

	simdi := time.Now()
	talepler := jwt.MapClaims{
		"iss":   denemeIssuer,
		"aud":   denemeIstemci,
		"sub":   "kullanici-1",
		"nonce": "n1",
		"iat":   simdi.Add(-2 * time.Hour).Unix(),
		"exp":   simdi.Add(-time.Hour).Unix(),
	}
	j := jwt.NewWithClaims(jwt.SigningMethodRS256, talepler)
	j.Header["kid"] = kidIcin(anahtar)
	dolmusToken, err := j.SignedString(anahtar)
	if err != nil {
		t.Fatalf("token imzalanamadi: %v", err)
	}

	if _, err := v.DogrulaNonce(dolmusToken, "n1"); err == nil {
		t.Error("suresi dolmus token kabul edildi")
	}
}

// --- yapicinin zorunlu alanlari ---------------------------------------------

func TestYukariAkisDogrulayiciZorunluAlanlar(t *testing.T) {
	ctx := context.Background()
	if _, err := NewYukariAkisDogrulayici(ctx, "", denemeIssuer, denemeIstemci); err == nil {
		t.Error("bos jwksURL kabul edildi")
	}
	if _, err := NewYukariAkisDogrulayici(ctx, "https://ornek/jwks", "", denemeIstemci); err == nil {
		t.Error("bos issuer kabul edildi")
	}
	if _, err := NewYukariAkisDogrulayici(ctx, "https://ornek/jwks", denemeIssuer, ""); err == nil {
		t.Error("bos audience kabul edildi")
	}
}

// --- Step 3: anahtar rotasyonu ----------------------------------------------

// TestAnahtarRotasyonuTakipEdilir'in OLCTUGU SEY: daha once hic
// gorulmemis bir kid ile imzalanmis bir id_token kabul edilir -- yani
// jwkset'in "bilinmeyen kid" aninda yeniden cekme davranisi (varsayilan,
// 5 dakikada bir sinirli) calisir.
//
// Bu test NewYukariAkisDogrulayici'ye verilen ctx'in iptal/olu kalma
// regresyonunu OLCMEZ: olcup olcmedigi dogrulandi (bkz. jwks_verifier.go
// NewYukariAkisDogrulayici yorumu ve jwkset@v0.5.17 http.go:83,89) --
// bilinmeyen-kid yenilemesi ctx'ten bagimsiz kendi ic context'ini
// kullanir. NewYukariAkisDogrulayici icinde ctx'i tekrar
// "context.WithTimeout(ctx, 30*time.Second); defer cancel()" ile
// degistirip bu testi calistirirsaniz GECER -- test bu mutasyonu
// yakalamaz. Bu bilerek boyle: ctx'in gercek bedeli (eski anahtarin
// sonsuza kadar cache'de kalmasi, 5 dakikalik pencerede ikinci bir yeni
// kid'in reddedilmesi) bu testin suresi icinde gozlenemez; sahte bir
// "ctx'i korur" etiketi, hicbir test olmamasindan daha kotu olurdu.
func TestAnahtarRotasyonuTakipEdilir(t *testing.T) {
	eski, _ := rsa.GenerateKey(rand.Reader, 2048)
	yeni, _ := rsa.GenerateKey(rand.Reader, 2048)

	kume := []*rsa.PrivateKey{eski}
	var kilit sync.Mutex
	url := sahteJWKSRSA(t, func() []*rsa.PrivateKey {
		kilit.Lock()
		defer kilit.Unlock()
		return kume
	})

	ctx, iptal := context.WithCancel(context.Background())
	defer iptal()
	v, err := NewYukariAkisDogrulayici(ctx, url, denemeIssuer, denemeIstemci)
	if err != nil {
		t.Fatalf("dogrulayici kurulamadi: %v", err)
	}
	defer v.Close()

	// Eski anahtarla imzali token gecer.
	if _, err := v.DogrulaNonce(tokenUret(t, eski, denemeIssuer, denemeIstemci, "n1"), "n1"); err != nil {
		t.Fatalf("eski anahtarli token reddedildi: %v", err)
	}

	// Saglayici anahtari dondurur.
	kilit.Lock()
	kume = []*rsa.PrivateKey{yeni}
	kilit.Unlock()

	// Yeni anahtarla imzali token da gecmeli: kid daha once gorulmedigi
	// icin jwkset bilinmeyen-kid yenilemesini tetikler. Bu yenileme
	// kendi (ctx'ten bagimsiz) ic context'ini kullanir -- dosyanin
	// basindaki not bunu neden olcmedigini aciklar.
	if _, err := v.DogrulaNonce(tokenUret(t, yeni, denemeIssuer, denemeIstemci, "n2"), "n2"); err != nil {
		t.Fatalf("rotasyon sonrasi token reddedildi (yenileme olu mu?): %v", err)
	}
}
