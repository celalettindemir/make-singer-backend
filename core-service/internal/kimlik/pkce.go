package kimlik

import (
	"context"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// pkceZorlayici, kutuphanenin op.AuthorizeValidator uzanti noktasini
// karsilayarak /authorize'da S256 PKCE'yi ZORUNLU kilar.
//
// NEDEN BIZ ZORLUYORUZ: op.Config.CodeMethodS256 YALNIZCA discovery
// belgesini besler (pkg/op/op.go CodeMethodS256Supported ->
// pkg/op/discovery.go); hicbir dogrulamada kullanilmaz.
// ValidateAuthRequestClient code_challenge'a hic bakmaz,
// AuthorizeCodeChallenge method'u denetlemez ve
// oidc.VerifyCodeChallenge S256 disindaki method'da (plain veya BOS)
// duz string karsilastirmasi yapar — yani challenge == verifier olur ve
// PKCE fiilen devre disi kalir.
type pkceZorlayici struct {
	*op.Provider
}

var _ op.AuthorizeValidator = (*pkceZorlayici)(nil)

// ValidateAuthRequest, kutuphanenin varsayilan dogrulamasini kosar ve
// uzerine S256 PKCE sartini ekler.
//
// SIRA ONEMLI: once op.ValidateAuthRequestClient, SONRA PKCE. Cunku
// redirect_uri'yi dogrulayan kutuphanedir ve PKCE hatasi istemcinin
// redirect_uri'sine 302 ile doner (op.AuthRequestError). Dogrulanmamis
// bir redirect_uri'ye hata dondurmek ACIK YONLENDIRME olurdu.
func (z *pkceZorlayici) ValidateAuthRequest(
	ctx context.Context,
	authReq *oidc.AuthRequest,
	storage op.Storage,
	verifier *op.IDTokenHintVerifier,
) (string, error) {
	client, err := storage.GetClientByClientID(ctx, authReq.ClientID)
	if err != nil {
		return "", oidc.ErrInvalidRequestRedirectURI().
			WithDescription("unable to retrieve client by id").WithParent(err)
	}
	sub, err := op.ValidateAuthRequestClient(ctx, authReq, client, verifier)
	if err != nil {
		return "", err
	}
	if err := pkceS256Zorunlu(authReq); err != nil {
		return "", err
	}
	return sub, nil
}

// pkceS256Zorunlu, code_challenge'in var oldugunu ve method'un S256
// oldugunu dogrular. Bos method da REDDEDILIR: kutuphane bos method'u
// plain gibi (duz karsilastirma) isler.
//
// Reddin /authorize'da olmasi onemli: aksi halde kullanici giris
// sayfasina yonlendirilir, sifresini girer, kod uretilir ve hata ancak
// token ucunda ("PKCE required") ortaya cikar — yani kimlik bilgileri
// bastan olu bir akisa girilmis olur.
func pkceS256Zorunlu(authReq *oidc.AuthRequest) error {
	if authReq.CodeChallenge == "" {
		return oidc.ErrInvalidRequest().WithDescription(
			"code_challenge is required: this provider only accepts PKCE with code_challenge_method=S256")
	}
	if authReq.CodeChallengeMethod != oidc.CodeChallengeMethodS256 {
		return oidc.ErrInvalidRequest().WithDescription(
			"code_challenge_method must be S256")
	}
	return nil
}
