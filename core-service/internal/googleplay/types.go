// Package googleplay verifies Google Play purchases through the Android
// Publisher API and authenticates Pub/Sub push deliveries (RTDN).
package googleplay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when Google answers 404 or 410 for a token.
var ErrNotFound = errors.New("googleplay: purchase not found")

// ErrUnauthorized is returned when a push request fails OIDC verification.
var ErrUnauthorized = errors.New("googleplay: unauthorized push")

// LineItem is one subscription line item.
type LineItem struct {
	ProductID  string
	BasePlanID string
	Expiry     time.Time
	AutoRenew  bool
	// LatestSuccessfulOrderID is the order of the line item's latest
	// successful charge (lineItems[].latestSuccessfulOrderId).
	LatestSuccessfulOrderID string
	// DeferredProductID and DeferredBasePlanID name the replacement that
	// starts at the period end (lineItems[].deferredItemReplacement).
	DeferredProductID  string
	DeferredBasePlanID string
}

// SubscriptionPurchase is the subset of subscriptionsv2 the backend uses.
type SubscriptionPurchase struct {
	State               string
	LatestOrderID       string
	LinkedPurchaseToken string
	AccountID           string
	Test                bool
	Acknowledged        bool
	LineItems           []LineItem
}

// ProductPurchase is the subset of a one-time product purchase the backend uses.
type ProductPurchase struct {
	OrderID          string
	PurchaseState    int
	ConsumptionState int
	AccountID        string
	Test             bool
}

// SubscriptionNotification is an RTDN subscription event.
type SubscriptionNotification struct {
	NotificationType int
	PurchaseToken    string
	SubscriptionID   string
}

// OneTimeNotification is an RTDN one-time product event.
type OneTimeNotification struct {
	NotificationType int
	PurchaseToken    string
	SKU              string
}

// VoidedNotification is an RTDN voided purchase event.
type VoidedNotification struct {
	PurchaseToken string
	OrderID       string
	ProductType   int
}

// DeveloperNotification is a decoded real-time developer notification.
type DeveloperNotification struct {
	PackageName  string
	Subscription *SubscriptionNotification
	OneTime      *OneTimeNotification
	Voided       *VoidedNotification
	Test         bool
}

type pushEnvelope struct {
	Message struct {
		Data string `json:"data"`
	} `json:"message"`
}

type rawNotification struct {
	PackageName  string `json:"packageName"`
	Subscription *struct {
		NotificationType int    `json:"notificationType"`
		PurchaseToken    string `json:"purchaseToken"`
		SubscriptionID   string `json:"subscriptionId"`
	} `json:"subscriptionNotification"`
	OneTime *struct {
		NotificationType int    `json:"notificationType"`
		PurchaseToken    string `json:"purchaseToken"`
		SKU              string `json:"sku"`
	} `json:"oneTimeProductNotification"`
	Voided *struct {
		PurchaseToken string `json:"purchaseToken"`
		OrderID       string `json:"orderId"`
		ProductType   int    `json:"productType"`
	} `json:"voidedPurchaseNotification"`
	Test json.RawMessage `json:"testNotification"`
}

// DecodePush decodes a Pub/Sub push body into a DeveloperNotification.
func DecodePush(body []byte) (*DeveloperNotification, error) {
	var env pushEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("googleplay: push body: %w", err)
	}
	if env.Message.Data == "" {
		return nil, errors.New("googleplay: push message has no data")
	}
	data, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		if data, err = base64.URLEncoding.DecodeString(env.Message.Data); err != nil {
			return nil, fmt.Errorf("googleplay: push data base64: %w", err)
		}
	}
	var raw rawNotification
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("googleplay: notification json: %w", err)
	}
	n := &DeveloperNotification{PackageName: raw.PackageName, Test: len(raw.Test) > 0 && string(raw.Test) != "null"}
	if s := raw.Subscription; s != nil {
		n.Subscription = &SubscriptionNotification{s.NotificationType, s.PurchaseToken, s.SubscriptionID}
	}
	if o := raw.OneTime; o != nil {
		n.OneTime = &OneTimeNotification{o.NotificationType, o.PurchaseToken, o.SKU}
	}
	if v := raw.Voided; v != nil {
		n.Voided = &VoidedNotification{v.PurchaseToken, v.OrderID, v.ProductType}
	}
	return n, nil
}
