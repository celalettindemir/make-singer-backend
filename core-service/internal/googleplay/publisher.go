package googleplay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/makeasinger/api/internal/config"
)

const (
	defaultBaseURL = "https://androidpublisher.googleapis.com"
	publisherScope = "https://www.googleapis.com/auth/androidpublisher"
)

// Publisher is an Android Publisher API v3 client.
type Publisher struct {
	client  *http.Client
	pkg     string
	baseURL string
}

// NewPublisher builds a client authenticated with the service account key.
func NewPublisher(ctx context.Context, cfg config.GooglePlayConfig, baseURL string) (*Publisher, error) {
	if cfg.PackageName == "" || cfg.ServiceAccountJSON == "" {
		return nil, errors.New("googleplay: package name and service account are required")
	}
	// The key comes from our own operator-controlled config, not untrusted input.
	creds, err := google.CredentialsFromJSON(ctx, []byte(cfg.ServiceAccountJSON), publisherScope) //nolint:staticcheck // SA1019: required by the task; trusted source
	if err != nil {
		return nil, fmt.Errorf("googleplay: service account: %w", err)
	}
	client := oauth2.NewClient(ctx, creds.TokenSource)
	client.Timeout = 30 * time.Second
	return NewPublisherWithClient(client, cfg.PackageName, baseURL), nil
}

// NewPublisherWithClient builds a client around an existing http.Client (tests).
func NewPublisherWithClient(httpClient *http.Client, packageName, baseURL string) *Publisher {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Publisher{client: httpClient, pkg: packageName, baseURL: strings.TrimRight(baseURL, "/")}
}

func (p *Publisher) path(parts ...string) string {
	b := p.baseURL + "/androidpublisher/v3/applications/" + url.PathEscape(p.pkg) + "/purchases"
	for _, s := range parts {
		b += "/" + url.PathEscape(s)
	}
	return b
}

func (p *Publisher) do(ctx context.Context, method, u string, out any) error {
	var body io.Reader
	if method == http.MethodPost {
		body = bytes.NewReader([]byte("{}"))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("googleplay: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("googleplay: unexpected status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("googleplay: decode: %w", err)
	}
	return nil
}

type subscriptionV2 struct {
	SubscriptionState          string `json:"subscriptionState"`
	LatestOrderID              string `json:"latestOrderId"`
	LinkedPurchaseToken        string `json:"linkedPurchaseToken"`
	AcknowledgementState       string `json:"acknowledgementState"`
	ExternalAccountIdentifiers struct {
		ObfuscatedExternalAccountID string `json:"obfuscatedExternalAccountId"`
	} `json:"externalAccountIdentifiers"`
	TestPurchase *json.RawMessage `json:"testPurchase"`
	LineItems    []struct {
		ProductID    string `json:"productId"`
		ExpiryTime   string `json:"expiryTime"`
		OfferDetails struct {
			BasePlanID string `json:"basePlanId"`
		} `json:"offerDetails"`
		AutoRenewingPlan *struct {
			AutoRenewEnabled bool `json:"autoRenewEnabled"`
		} `json:"autoRenewingPlan"`
	} `json:"lineItems"`
}

// Subscription fetches a subscription purchase (subscriptionsv2).
func (p *Publisher) Subscription(ctx context.Context, token string) (*SubscriptionPurchase, error) {
	var raw subscriptionV2
	if err := p.do(ctx, http.MethodGet, p.path("subscriptionsv2", "tokens", token), &raw); err != nil {
		return nil, err
	}
	out := &SubscriptionPurchase{
		State:               raw.SubscriptionState,
		LatestOrderID:       raw.LatestOrderID,
		LinkedPurchaseToken: raw.LinkedPurchaseToken,
		AccountID:           raw.ExternalAccountIdentifiers.ObfuscatedExternalAccountID,
		Test:                raw.TestPurchase != nil,
		Acknowledged:        raw.AcknowledgementState == "ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED",
	}
	for _, li := range raw.LineItems {
		item := LineItem{ProductID: li.ProductID, BasePlanID: li.OfferDetails.BasePlanID}
		if li.ExpiryTime != "" {
			t, err := time.Parse(time.RFC3339Nano, li.ExpiryTime)
			if err != nil {
				return nil, fmt.Errorf("googleplay: expiryTime: %w", err)
			}
			item.Expiry = t
		}
		if li.AutoRenewingPlan != nil {
			item.AutoRenew = li.AutoRenewingPlan.AutoRenewEnabled
		}
		out.LineItems = append(out.LineItems, item)
	}
	return out, nil
}

// Product fetches a one-time product purchase.
func (p *Publisher) Product(ctx context.Context, productID, token string) (*ProductPurchase, error) {
	var raw struct {
		OrderID                     string `json:"orderId"`
		PurchaseState               int    `json:"purchaseState"`
		ConsumptionState            int    `json:"consumptionState"`
		ObfuscatedExternalAccountID string `json:"obfuscatedExternalAccountId"`
		PurchaseType                *int   `json:"purchaseType"`
	}
	if err := p.do(ctx, http.MethodGet, p.path("products", productID, "tokens", token), &raw); err != nil {
		return nil, err
	}
	return &ProductPurchase{
		OrderID:          raw.OrderID,
		PurchaseState:    raw.PurchaseState,
		ConsumptionState: raw.ConsumptionState,
		AccountID:        raw.ObfuscatedExternalAccountID,
		Test:             raw.PurchaseType != nil && *raw.PurchaseType == 0,
	}, nil
}

// AcknowledgeSubscription acknowledges a subscription purchase.
func (p *Publisher) AcknowledgeSubscription(ctx context.Context, subscriptionID, token string) error {
	return p.do(ctx, http.MethodPost, p.path("subscriptions", subscriptionID, "tokens", token)+":acknowledge", nil)
}

// ConsumeProduct consumes a one-time product purchase.
func (p *Publisher) ConsumeProduct(ctx context.Context, productID, token string) error {
	return p.do(ctx, http.MethodPost, p.path("products", productID, "tokens", token)+":consume", nil)
}
