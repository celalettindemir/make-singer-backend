package appstore

import (
	"encoding/json"
	"time"
)

// Transaction is the decoded payload of a signed StoreKit 2 transaction
// (JWSTransaction). Dates arrive as epoch milliseconds.
type Transaction struct {
	TransactionID         string
	OriginalTransactionID string
	ProductID             string
	BundleID              string
	Type                  string
	Environment           string
	AppAccountToken       string
	PurchaseDate          *time.Time
	ExpiresDate           *time.Time
	RevocationDate        *time.Time
}

type transactionWire struct {
	TransactionID         string `json:"transactionId"`
	OriginalTransactionID string `json:"originalTransactionId"`
	ProductID             string `json:"productId"`
	BundleID              string `json:"bundleId"`
	Type                  string `json:"type"`
	Environment           string `json:"environment"`
	AppAccountToken       string `json:"appAccountToken"`
	PurchaseDate          *int64 `json:"purchaseDate"`
	ExpiresDate           *int64 `json:"expiresDate"`
	RevocationDate        *int64 `json:"revocationDate"`
}

// UnmarshalJSON decodes Apple's field names and millisecond dates.
func (t *Transaction) UnmarshalJSON(b []byte) error {
	var w transactionWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*t = Transaction{
		TransactionID:         w.TransactionID,
		OriginalTransactionID: w.OriginalTransactionID,
		ProductID:             w.ProductID,
		BundleID:              w.BundleID,
		Type:                  w.Type,
		Environment:           w.Environment,
		AppAccountToken:       w.AppAccountToken,
		PurchaseDate:          msToTime(w.PurchaseDate),
		ExpiresDate:           msToTime(w.ExpiresDate),
		RevocationDate:        msToTime(w.RevocationDate),
	}
	return nil
}

// RenewalInfo is the decoded payload of a signed renewal info (JWSRenewalInfo).
type RenewalInfo struct {
	AutoRenewStatus        int
	GracePeriodExpiresDate *time.Time
	// AutoRenewProductID is the product of the next renewal (it differs from
	// the current product when the user scheduled a downgrade).
	AutoRenewProductID string
}

// UnmarshalJSON decodes Apple's field names and millisecond dates.
func (r *RenewalInfo) UnmarshalJSON(b []byte) error {
	var w struct {
		AutoRenewStatus        int    `json:"autoRenewStatus"`
		GracePeriodExpiresDate *int64 `json:"gracePeriodExpiresDate"`
		AutoRenewProductID     string `json:"autoRenewProductId"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*r = RenewalInfo{AutoRenewStatus: w.AutoRenewStatus, GracePeriodExpiresDate: msToTime(w.GracePeriodExpiresDate), AutoRenewProductID: w.AutoRenewProductID}
	return nil
}

// Notification is a verified App Store Server Notification v2 with its
// data envelope flattened. The signed inner payloads are left for
// Verifier.Transaction / Verifier.Renewal.
type Notification struct {
	Type                  string
	Subtype               string
	UUID                  string
	Environment           string
	BundleID              string
	SignedTransactionInfo string
	SignedRenewalInfo     string
}

func msToTime(ms *int64) *time.Time {
	if ms == nil || *ms == 0 {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}
