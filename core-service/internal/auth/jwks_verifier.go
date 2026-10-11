package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

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

// Claims, yukari akis saglayicilarinin (Apple, Google) id_token
// claim'leridir; Nonce alani bu yuzden burada tasinir.
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
// ctx, keyfunc.NewDefaultCtx'e OLDUGU GIBI iletilir ve CAGIRANIN omru
// kadar yasamalidir. Burada 30 saniyelik bir
// timeout ctx'i kurup "defer cancel()" ile hemen iptal etmek YANLISTIR --
// ama ONEMLI: bunun bedeli, ilk okumada tahmin edilenden daha dar.
//
// ctx SADECE saatlik periyodik arka plan yenilemesinin yasam suresidir
// (bkz. jwkset@v0.5.17 http.go:89 dogrudan: "NewDefaultHTTPClientCtx is
// the same as NewDefaultHTTPClient, but with a context that can end the
// refresh goroutine."; storage.go:278-295 bu goroutine'in options.Ctx'in
// Done()'ina gore CIKTIGINI gosterir). Rotasyonun kendisi -- yeni bir kid
// gorulmesi -- BU CTX'E BAGLI DEGILDIR: jwkset'in varsayilan davranisi
// (http.go:83, "3. Refresh remote HTTP resources if a key with an
// unknown key ID is trying to be read, with a rate limit of 5 minutes.")
// bilinmeyen kid'i ANINDA, ctx'ten bagimsiz bir ic context ile yeniden
// ceker. (TestAnahtarRotasyonuTakipEdilir bunu olcer -- ve ctx iptal
// edilmis olsa bile gecer; bkz. o testin yanindaki not.)
//
// ctx iptal edilirse/olu kalirsa gercek bedel budur:
//
//	(a) saglayicinin ARTIK YAYINLAMADIGI (orn. ele gecirildigi icin geri
//	    cekilen) bir anahtar, surec yeniden baslamadan bellekte sonsuza
//	    kadar GECERLI SAYILIR -- hic bir zaman silinmez;
//	(b) saatlik proaktif yenileme olu oldugundan, 5 dakikalik pencere
//	    icinde arka arkaya IKI YENI kid gelirse ikincisi dogrulanamaz
//	    (ayni rate limiter, tazelenmeden ikinci bir anlik yenilemeye izin
//	    vermez).
//
// Ikisi de ciddi ama "rotasyon aninda butun girisler kirilir" kadar
// dramatik degil; bu metodun ctx'i cagiranin verdigi gibi tutmasi yeterli,
// ayrica bir yapilandirma/clock enjeksiyonu GEREKMEZ.
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
