package middleware

// APIAuthModu, /api/* rotalarinin hangi dogrulama modunda kosacagini
// belirtir.
type APIAuthModu int

const (
	// ModOP: kendi OP'umuzun urettigi RS256 access token'i dogrulanir.
	ModOP APIAuthModu = iota
	// ModGateway: Traefik ForwardAuth'un yazdigi X-User-* header'lari
	// okunur (Faz 5'te kalkacak).
	ModGateway
	// ModLegacy: Zitadel JWKS ve/veya legacy HMAC zinciri
	// (Faz 5'te kalkacak).
	ModLegacy
)

func (m APIAuthModu) String() string {
	switch m {
	case ModOP:
		return "op"
	case ModGateway:
		return "gateway"
	default:
		return "legacy"
	}
}

// APIAuthModuSec, mod secimini YAPILANDIRMAYA gore yapar; calisma
// zamanindaki basari durumuna gore DEGIL.
//
// Kritik: secim `kimlikSunucu != nil` gibi bir calisma zamani
// degerinden okunursa, OP baslatilamadigi her durumda (DB erisilemez,
// migration patladi, anahtar yuklenemedi) sessizce legacy HMAC yoluna
// dusulur. Legacy yol ne exp ne iss ne aud istiyor ve varsayilan sirri
// herkesin bildigi bir dizge; yani gecici bir Postgres kesintisi
// /api/*'i suresiz, herkesin uretebilecegi jetonlara acardi.
// opIssuer doluyken ModOP DISINDA bir mod donmez: legacy ve gateway
// dallari SECILEMEZ.
func APIAuthModuSec(opIssuer string, gatewayEnabled bool) APIAuthModu {
	switch {
	case opIssuer != "":
		return ModOP
	case gatewayEnabled:
		return ModGateway
	default:
		return ModLegacy
	}
}
