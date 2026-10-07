package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/makeasinger/api/internal/config"
)

// TokenVerifier defines the interface for JWT token verification
type TokenVerifier interface {
	Validate(tokenString string) (*Claims, error)
	Close() error
}

// EsnekBool, JSON'da hem true/false hem "true"/"false" olarak gelebilen bir
// bool'dur. Apple email_verified'i bazi akislarda STRING gonderir; bool'a
// cozumlemeye calisan kod orada sessizce hata verir ve giris kirilir.
type EsnekBool bool

// UnmarshalJSON, once normal bool olarak dener; basarisiz olursa string
// "true"/"false" kabul eder. Baska bir deger (sayi, "belki" gibi bozuk bir
// string) ACIKCA hata verir -- sessizce false'a dusmek, bozuk bir
// email_verified degerinin dogrulanmis sayilmasi riskini tasir.
func (e *EsnekBool) UnmarshalJSON(b []byte) error {
	var bl bool
	if err := json.Unmarshal(b, &bl); err == nil {
		*e = EsnekBool(bl)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("email_verified ne bool ne string")
	}
	switch s {
	case "true":
		*e = true
	case "false":
		*e = false
	default:
		return fmt.Errorf("email_verified beklenmeyen string")
	}
	return nil
}

// Claims represents the JWT claims from Zitadel. Ayrica yukari akis
// saglayicilarinin (Apple, Google) id_token'larini dogrularken de
// kullanilir; Nonce alani bu yuzden burada tasinir.
type Claims struct {
	UserID            string    `json:"sub"`
	Email             string    `json:"email,omitempty"`
	EmailVerified     EsnekBool `json:"email_verified,omitempty"`
	Name              string    `json:"name,omitempty"`
	PreferredUsername string    `json:"preferred_username,omitempty"`
	Roles             []string  `json:"roles,omitempty"`
	// Nonce, yukari akis saglayicisinin id_token'inda donen nonce'tur.
	// Sadece YukariAkisDogrulayici.DogrulaNonce tarafindan kontrol edilir.
	Nonce string `json:"nonce,omitempty"`
	jwt.RegisteredClaims
}

// JWKSVerifier implements TokenVerifier using JWKS
type JWKSVerifier struct {
	jwks     keyfunc.Keyfunc
	issuer   string
	audience string
}

// NewJWKSVerifier creates a new JWKS-based token verifier for Zitadel
func NewJWKSVerifier(cfg *config.ZitadelConfig) (*JWKSVerifier, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("zitadel issuer is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	jwksURL, err := discoverJWKSURL(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to discover JWKS URL: %w", err)
	}

	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS keyfunc: %w", err)
	}

	return &JWKSVerifier{
		jwks:     jwks,
		issuer:   cfg.Issuer,
		audience: cfg.ClientID,
	}, nil
}

// discoverJWKSURL fetches the OIDC discovery document and extracts the jwks_uri.
func discoverJWKSURL(ctx context.Context, issuer string) (string, error) {
	discoveryURL := fmt.Sprintf("%s/.well-known/openid-configuration", issuer)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create discovery request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch discovery document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery endpoint returned status %d", resp.StatusCode)
	}

	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("failed to decode discovery document: %w", err)
	}

	if doc.JWKSURI == "" {
		return "", fmt.Errorf("jwks_uri not found in discovery document")
	}

	return doc.JWKSURI, nil
}

// Validate validates a JWT token and returns the claims
func (v *JWKSVerifier) Validate(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, v.jwks.Keyfunc,
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Validate audience if configured
	if v.audience != "" {
		aud, err := claims.GetAudience()
		if err != nil {
			return nil, fmt.Errorf("failed to get audience: %w", err)
		}
		if !contains(aud, v.audience) {
			return nil, fmt.Errorf("invalid audience")
		}
	}

	return claims, nil
}

// Close releases resources used by the verifier
func (v *JWKSVerifier) Close() error {
	// keyfunc.Keyfunc is managed internally; no explicit cleanup needed
	return nil
}

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// YukariAkisDogrulayici, bir YUKARI AKIS saglayicisinin (Apple, Google)
// id_token'larini dogrular. Federasyon geri cagirma (callback) akisinda
// kullanilir: kullanici Apple/Google'da yetkilendirdikten sonra elde edilen
// id_token, burada iss/aud/alg ve nonce acisindan dogrulanir.
type YukariAkisDogrulayici struct {
	jwks     keyfunc.Keyfunc
	issuer   string
	audience string
}

// NewYukariAkisDogrulayici, bir YUKARI AKIS saglayicisinin (Apple, Google)
// id_token'larini dogrulayan bir dogrulayici kurar. jwksURL cagiran
// tarafindan verilir: Apple ve Google'in jwks_uri'si sabittir, her giriste
// discovery cekmek gereksiz bir ag turu ve bir hata kaynagidir.
//
// ctx, arka plan anahtar yenilemesinin yasam suresidir ve CAGIRANIN omru
// kadar yasamalidir. Eski NewJWKSVerifier'da oldugu gibi burada 30 saniyelik
// bir timeout ctx'i kurup "defer cancel()" ile hemen iptal etmek, anahtar
// kumesinin bir daha HIC yenilenmemesi demektir. Zitadel'de bu fark
// edilmedi cunku imzalama anahtari degismiyordu. Google anahtarlarini
// duzenli dondurur: olu yenileme, rotasyondan sonra butun federe girislerin
// sessizce kirilmasi demektir. (TestAnahtarRotasyonuTakipEdilir bunu olcer.)
func NewYukariAkisDogrulayici(ctx context.Context, jwksURL, issuer, audience string) (*YukariAkisDogrulayici, error) {
	if jwksURL == "" || issuer == "" || audience == "" {
		return nil, fmt.Errorf("jwksURL, issuer ve audience zorunlu")
	}
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("JWKS kurulamadi: %w", err)
	}
	return &YukariAkisDogrulayici{jwks: jwks, issuer: issuer, audience: audience}, nil
}

// DogrulaNonce, id_token'i dogrular ve nonce'un BEKLENEN degerle birebir
// ayni oldugunu da zorunlu kilar.
//
// Nonce olmadan: saldirgan kendi Google/Apple yetkilendirmesinden aldigi
// gecerli bir id_token'i kurbanin yarim kalmis akisina enjekte eder ve
// kurbanin tarayicisinda saldirganin hesabina giris olur. Imza, iss ve aud
// bu tokende de DOGRUDUR; onu reddeden tek sey nonce'tur. Karsilastirma
// sabit zamanlidir: zamanlama farkina bakarak nonce'u parca parca tahmin
// etmeye calisan bir saldirgana bilgi sizdirmamak icin.
func (v *YukariAkisDogrulayici) DogrulaNonce(tokenString, nonce string) (*Claims, error) {
	if nonce == "" {
		return nil, fmt.Errorf("nonce bos olamaz")
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, v.jwks.Keyfunc,
		// Alg ACIKCA sabitlenir: korumanin kutuphanenin anahtar tipi
		// kontrolune dayanmasi onu her yukseltmede kirilabilir kilar.
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		// Hata metnine tokenString veya ham claim degerleri KATILMAZ:
		// id_token loglara sizmamali.
		return nil, fmt.Errorf("id_token dogrulanamadi: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("id_token claim'leri gecersiz")
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return nil, fmt.Errorf("nonce eslesmedi")
	}
	if strings.TrimSpace(claims.UserID) == "" {
		return nil, fmt.Errorf("sub bos")
	}
	return claims, nil
}

// Close, dogrulayicinin kaynaklarini serbest birakir.
func (v *YukariAkisDogrulayici) Close() error {
	// keyfunc.Keyfunc kaynaklari ctx'in iptaliyle serbest kalir; burada
	// ek bir temizlik gerekmez. Metod, cagiran tarafta tutarli bir
	// "kapat" aliskanligi saglamak icin tutulur.
	return nil
}
