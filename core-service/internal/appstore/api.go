package appstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/makeasinger/api/internal/config"
)

const (
	productionBaseURL = "https://api.storekit.itunes.apple.com"
	sandboxBaseURL    = "https://api.storekit-sandbox.itunes.apple.com"
	bearerTTL         = 20 * time.Minute
)

// API is a minimal App Store Server API client.
type API struct {
	cfg        config.AppStoreConfig
	http       *http.Client
	prodURL    string
	sandboxURL string
	now        func() time.Time
}

// NewAPI builds an API client; a nil httpClient uses a 15s-timeout default.
func NewAPI(cfg config.AppStoreConfig, httpClient *http.Client) *API {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &API{cfg: cfg, http: httpClient, prodURL: productionBaseURL, sandboxURL: sandboxBaseURL, now: time.Now}
}

// SubscriptionStatus fetches the latest transaction of a subscription group,
// trying production first and sandbox on 404. status is Apple's numeric
// subscription status (1 active, 2 expired, 3 billing retry, 4 grace, 5 revoked).
func (a *API) SubscriptionStatus(ctx context.Context, originalTransactionID string) (status int, signedTransaction, signedRenewal string, err error) {
	bearer, err := a.bearer()
	if err != nil {
		return 0, "", "", err
	}
	body, code, err := a.get(ctx, a.prodURL, originalTransactionID, bearer)
	if err != nil {
		return 0, "", "", err
	}
	if code == http.StatusNotFound {
		if body, code, err = a.get(ctx, a.sandboxURL, originalTransactionID, bearer); err != nil {
			return 0, "", "", err
		}
	}
	if code != http.StatusOK {
		return 0, "", "", fmt.Errorf("app store server api: unexpected status %d", code)
	}
	var resp struct {
		Data []struct {
			LastTransactions []struct {
				OriginalTransactionID string `json:"originalTransactionId"`
				Status                int    `json:"status"`
				SignedTransactionInfo string `json:"signedTransactionInfo"`
				SignedRenewalInfo     string `json:"signedRenewalInfo"`
			} `json:"lastTransactions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, "", "", fmt.Errorf("app store server api: decode: %w", err)
	}
	for _, g := range resp.Data {
		for _, lt := range g.LastTransactions {
			if lt.OriginalTransactionID == originalTransactionID {
				return lt.Status, lt.SignedTransactionInfo, lt.SignedRenewalInfo, nil
			}
		}
	}
	return 0, "", "", errors.New("app store server api: subscription not found in response")
}

func (a *API) get(ctx context.Context, base, id, bearer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/inApps/v1/subscriptions/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("app store server api: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("app store server api: read: %w", err)
	}
	return b, resp.StatusCode, nil
}

func (a *API) bearer() (string, error) {
	block, _ := pem.Decode([]byte(a.cfg.PrivateKey))
	if block == nil {
		return "", errors.New("app store .p8 key could not be parsed as PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("could not read the app store .p8 key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("app store key is not ECDSA (%T)", parsed)
	}
	iat := a.now()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": a.cfg.IssuerID,
		"iat": iat.Unix(),
		"exp": iat.Add(bearerTTL).Unix(),
		"aud": "appstoreconnect-v1",
		"bid": a.cfg.BundleID,
	})
	tok.Header["kid"] = a.cfg.KeyID
	return tok.SignedString(key)
}
