package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUpstreamResponse signals that an unexpected response came from the
// upstream provider's token endpoint: an HTTP error, an undecodable body, or
// a missing id_token / {"error":...} that came with a 200. The error body
// itself is never added to this error (see ExchangeCode): it may contain
// pieces of the code or client_secret.
var ErrUpstreamResponse = errors.New("yukari akis saglayicisi beklenmeyen yanit verdi")

// upstreamTimeout is the upper bound applied to the upstream token endpoint.
// A provider that does not respond without a timeout accumulates the
// goroutines holding the login endpoint.
const upstreamTimeout = 10 * time.Second

// upstreamResponseLimit is the maximum number of bytes to read from the
// token endpoint's response body. io.LimitReader is used instead of
// io.ReadAll so that a malicious or broken response cannot fill memory.
const upstreamResponseLimit = 1 << 20

// ExchangeCode exchanges the authorization code received from the upstream
// provider (Apple or Google) for an id_token at the token endpoint. The
// returned string is an UNVERIFIED id_token; the caller is responsible for
// passing it to Task 2's auth.VerifyWithNonce -- this function does not
// check the signature or the nonce.
func (ic *Upstream) ExchangeCode(
	ctx context.Context, client *http.Client, code, codeVerifier, callbackURL string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
	defer cancel()

	secret := ic.ClientSecret
	if ic.Name == providerApple {
		var err error
		// K5: regenerated on every exchange, not cached.
		secret, err = appleClientSecret(ic.AppleTeamID, ic.AppleKeyID, ic.ClientID, ic.AppleKeyP8, time.Now())
		if err != nil {
			return "", fmt.Errorf("apple client_secret uretilemedi: %w", err)
		}
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", callbackURL)
	form.Set("client_id", ic.ClientID)
	form.Set("client_secret", secret)
	if ic.SupportsPKCE {
		form.Set("code_verifier", codeVerifier)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, ic.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("istek kurulamadi: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("yukari akisa ulasilamadi: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	// The body is read with a LIMIT: so that a malicious or broken response
	// cannot fill memory.
	body, err := io.ReadAll(io.LimitReader(response.Body, upstreamResponseLimit))
	if err != nil {
		return "", fmt.Errorf("yanit okunamadi: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		// THE ERROR BODY IS NOT LOGGED and NOT SHOWN TO THE USER: it may contain
		// pieces of the code or secrets. Only the status code.
		return "", fmt.Errorf("%w: durum %d", ErrUpstreamResponse, response.StatusCode)
	}
	var decoded struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("%w: govde cozumlenemedi", ErrUpstreamResponse)
	}
	// 200 + {"error":...} is a REAL situation; counting a 200 as success
	// without looking at the body would mean sending an empty id_token to
	// verification.
	if decoded.Error != "" || decoded.IDToken == "" {
		return "", fmt.Errorf("%w: id_token yok", ErrUpstreamResponse)
	}
	return decoded.IDToken, nil
}
