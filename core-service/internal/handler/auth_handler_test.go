package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

// OP modunda main.go authHandler'i NewAuthHandler(nil, "") ile kuruyor.
// Bu handler HICBIR jetonu dogrulamamali ve sessizce "her seyi kabul et"
// moduna DUSMEMELI: /auth/verify Traefik ForwardAuth tarafindan
// cagriliyor, 200 + X-User-Id donmesi kimlik uretmek demektir. Eskiden
// bu uc cfg.JWT.Secret ile kuruluyordu ve varsayilan sirla imzali
// SURESIZ bir jeton 200 aliyordu.
func TestVerifyOPModundaHicbirJetonuKabulEtmez(t *testing.T) {
	h := NewAuthHandler(nil, "")
	app := fiber.New()
	app.Get("/auth/verify", h.Verify)

	// Varsayilan sirla imzali, exp'siz (suresiz) legacy jeton.
	const varsayilanSir = "change-me-in-production"
	suresiz := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"userId": "saldirgan", "email": "saldirgan@ornek.dev",
	})
	suresizImzali, err := suresiz.SignedString([]byte(varsayilanSir))
	if err != nil {
		t.Fatalf("jeton uretilemedi: %v", err)
	}

	// Bos sirla imzali jeton: "sir bos ise her sey gecer" tuzagi.
	bosSirli := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"userId": "saldirgan",
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	bosSirliImzali, err := bosSirli.SignedString([]byte(""))
	if err != nil {
		t.Fatalf("jeton uretilemedi: %v", err)
	}

	// alg:none ile imzasiz jeton.
	imzasiz, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"userId": "saldirgan",
		"exp":    time.Now().Add(time.Hour).Unix(),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("imzasiz jeton uretilemedi: %v", err)
	}

	durumlar := map[string]string{
		"header yok":                     "",
		"varsayilan sirla suresiz jeton": "Bearer " + suresizImzali,
		"bos sirla imzali jeton":         "Bearer " + bosSirliImzali,
		"alg none":                       "Bearer " + imzasiz,
		"bozuk jeton":                    "Bearer abc.def.ghi",
	}
	for ad, yetki := range durumlar {
		r := httptest.NewRequest("GET", "/auth/verify", nil)
		if yetki != "" {
			r.Header.Set("Authorization", yetki)
		}
		y, err := app.Test(r, -1)
		if err != nil {
			t.Fatalf("%s: Test: %v", ad, err)
		}
		if y.StatusCode != fiber.StatusUnauthorized {
			t.Errorf("%s: durum = %d, beklenen 401", ad, y.StatusCode)
		}
		if uid := y.Header.Get("X-User-Id"); uid != "" {
			t.Errorf("%s: X-User-Id sizdi: %q", ad, uid)
		}
	}
}
