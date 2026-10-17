package credit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/googleplay"
)

// Purchase errors.
var (
	// ErrPurchaseInvalid: the store rejects the purchase or it is not ours.
	ErrPurchaseInvalid = errors.New("credit: purchase invalid")
	// ErrOwnedByOther: the purchase belongs to another living account (6-G15).
	ErrOwnedByOther = errors.New("credit: purchase owned by another account")
	// ErrStoreUnavailable: the store integration is not configured.
	ErrStoreUnavailable = errors.New("credit: store not configured")
)

// PurchaseResult statuses.
const (
	PurchaseCredited = "credited"
	PurchasePending  = "pending"
	PurchaseRecorded = "recorded"
)

// Store names (kredi.abonelik.magaza, kredi.satin_alma.magaza, ref prefix).
const (
	storeApple  = "apple"
	storeGoogle = "google"
)

// Subscription statuses (kredi.abonelik.durum).
const (
	subActive       = "aktif"
	subBillingIssue = "odeme_sorunu"
	subGrace        = "ek_sure"
	subExpired      = "bitti"
	subRefunded     = "iade"
)

// Environments (ortam).
const (
	envProduction = "production"
	envSandbox    = "sandbox"
)

// Internal product ids (P7).
const (
	productMonthly = "pro.monthly"
	productYearly  = "pro.yearly"
	// googleSubscription is the Google Play subscription product; its base
	// plans "monthly" and "yearly" map to productMonthly / productYearly.
	googleSubscription = "pro"
	turPaket           = "paket"
)

var packCredits = map[string]int{"credits.100": 100, "credits.300": 300, "credits.1000": 1000}

// AppleVerifier verifies App Store JWS payloads (appstore.Verifier).
type AppleVerifier interface {
	Transaction(jws string) (*appstore.Transaction, error)
	Renewal(jws string) (*appstore.RenewalInfo, error)
}

// AppleStatusAPI reads a subscription's state from the App Store Server API.
type AppleStatusAPI interface {
	SubscriptionStatus(ctx context.Context, originalID string) (status int, signedTransaction, signedRenewal string, err error)
}

// GooglePublisher is the Android Publisher surface (googleplay.Publisher).
type GooglePublisher interface {
	Subscription(ctx context.Context, token string) (*googleplay.SubscriptionPurchase, error)
	Product(ctx context.Context, productID, token string) (*googleplay.ProductPurchase, error)
	AcknowledgeSubscription(ctx context.Context, subscriptionID, token string) error
	ConsumeProduct(ctx context.Context, productID, token string) error
}

// PurchasesConfig configures Purchases.
type PurchasesConfig struct {
	// PlanMonthly is the plan bucket amount each period.
	PlanMonthly int
	// AppleProductPrefix + internal id = App Store product id.
	AppleProductPrefix string
	// PackageName is the Google Play package notifications must carry.
	PackageName string
}

// PurchaseResult is the answer of a purchase verification.
type PurchaseResult struct {
	Status  string  `json:"purchaseStatus"`
	Summary Summary `json:"credits"`
}

// Purchases turns verified store facts into ledger movements.
type Purchases struct {
	store    PurchaseStore
	svc      *Service
	apple    AppleVerifier
	appleAPI AppleStatusAPI
	google   GooglePublisher
	cfg      PurchasesConfig
	now      func() time.Time
}

// NewPurchases builds Purchases. A nil apple verifier or google publisher
// means that store is not configured; a nil appleAPI skips Apple resyncs.
func NewPurchases(store PurchaseStore, svc *Service, apple AppleVerifier, appleAPI AppleStatusAPI, google GooglePublisher, cfg PurchasesConfig, now func() time.Time) *Purchases {
	if now == nil {
		now = time.Now
	}
	return &Purchases{store: store, svc: svc, apple: apple, appleAPI: appleAPI, google: google, cfg: cfg, now: now}
}

// refillAt is the n-th monthly refill after anchor: n calendar months
// later, clamped to the last day of that month (P6).
func refillAt(anchor time.Time, n int) time.Time {
	y, m, d := anchor.Date()
	last := time.Date(y, m+time.Month(n)+1, 0, 0, 0, 0, 0, anchor.Location()).Day()
	return time.Date(y, m+time.Month(n), min(d, last),
		anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond(), anchor.Location())
}

// resolveOwner finds the owner of a purchase (6-G15):
//  1. the account token of a living account; another user than the
//     submitter gives ErrOwnedByOther;
//  2. the owner of the linked record (a living owner other than the
//     submitter gives ErrOwnedByOther);
//  3. the submitter;
//  4. "" for an ownerless record (webhook without token or link).
func (p *Purchases) resolveOwner(ctx context.Context, token string, linkedOriginal *Subscription, submitter string) (string, error) {
	if id, err := uuid.Parse(token); err == nil && token != "" {
		acct, err := p.store.AccountByStoreToken(ctx, id)
		if err != nil {
			return "", err
		}
		if acct != nil {
			if submitter != "" && acct.UserID != submitter {
				return "", ErrOwnedByOther
			}
			return acct.UserID, nil
		}
	}
	if linkedOriginal != nil && linkedOriginal.UserID != nil {
		if submitter != "" && *linkedOriginal.UserID != submitter {
			return "", ErrOwnedByOther
		}
		return *linkedOriginal.UserID, nil
	}
	return submitter, nil
}

func (p *Purchases) result(ctx context.Context, userID, status string) (PurchaseResult, error) {
	sum, err := p.svc.Summary(ctx, userID)
	if err != nil {
		return PurchaseResult{}, err
	}
	return PurchaseResult{Status: status, Summary: sum}, nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func live(status string) bool {
	return status == subActive || status == subGrace || status == subBillingIssue
}

// pack describes a one-time pack purchase.
type pack struct {
	store, orderID, product, token, env string
	raw                                 json.RawMessage
}

// applyPack records a pack and credits its owner once (ref <store>:<order>).
// A pack already credited to an account that was since deleted is never
// credited again.
func (p *Purchases) applyPack(ctx context.Context, pk pack, submitter string) (string, error) {
	credits := packCredits[pk.product]
	rec, err := p.store.PurchaseByOrder(ctx, pk.store, pk.orderID)
	if err != nil {
		return "", err
	}
	if rec != nil && (rec.Status == purchaseRefunded || (rec.UserID == nil && rec.Status == purchaseCredited)) {
		return PurchaseRecorded, nil
	}
	var linked *Subscription
	if rec != nil {
		linked = &Subscription{UserID: rec.UserID}
	}
	owner, err := p.resolveOwner(ctx, pk.token, linked, submitter)
	if err != nil {
		return "", err
	}
	record := PurchaseRecord{Store: pk.store, OrderID: pk.orderID, Product: pk.product, Credits: credits, Env: pk.env, Raw: pk.raw}
	if owner == "" {
		record.Status = purchaseOwnerless
		_, err := p.store.RecordPurchase(ctx, record)
		return PurchaseRecorded, err
	}
	// Claim the record first; the stored owner decides a race.
	record.UserID, record.Status = &owner, purchaseProcessing
	if _, err := p.store.RecordPurchase(ctx, record); err != nil {
		return "", err
	}
	if rec, err = p.store.PurchaseByOrder(ctx, pk.store, pk.orderID); err != nil {
		return "", err
	}
	if rec == nil || rec.UserID == nil || *rec.UserID != owner {
		if submitter != "" {
			return "", ErrOwnedByOther
		}
		return PurchaseRecorded, nil
	}
	if rec.Status == purchaseRefunded {
		return PurchaseRecorded, nil
	}
	if _, _, err := p.store.Credit(ctx, owner, turPaket, pk.store+":"+pk.orderID, pk.product, credits); err != nil {
		return "", err
	}
	record.Status = purchaseCredited
	if _, err := p.store.RecordPurchase(ctx, record); err != nil {
		return "", err
	}
	// A refund that landed between the claim and the credit saw no
	// credited row and clawed nothing back; do it here (same ref, once).
	if rec, err = p.store.PurchaseByOrder(ctx, pk.store, pk.orderID); err != nil {
		return "", err
	}
	if rec != nil && rec.Status == purchaseRefunded {
		if _, _, err := p.store.ClawBack(ctx, owner, pk.store+":"+pk.orderID, credits); err != nil {
			return "", err
		}
		return PurchaseRecorded, nil
	}
	return PurchaseCredited, nil
}

// refundPack marks a pack refunded and claws its credits back from the
// owner it was credited to (ref <store>:<order>, may go negative).
func (p *Purchases) refundPack(ctx context.Context, store, orderID, product, env string) error {
	rec, err := p.store.PurchaseByOrder(ctx, store, orderID)
	if err != nil {
		return err
	}
	if rec != nil && rec.Status == purchaseRefunded {
		return nil
	}
	if rec != nil && rec.UserID != nil && rec.Status == purchaseCredited {
		if _, _, err := p.store.ClawBack(ctx, *rec.UserID, store+":"+orderID, rec.Credits); err != nil {
			return err
		}
	}
	record := PurchaseRecord{Store: store, OrderID: orderID, Product: product, Credits: packCredits[product], Status: purchaseRefunded, Env: env}
	if rec != nil {
		record.Product, record.Credits, record.Env = rec.Product, rec.Credits, rec.Env
	}
	_, err = p.store.RecordPurchase(ctx, record)
	return err
}

// subFacts is a verified subscription state from a store.
type subFacts struct {
	store, originalID, product, status, env string
	periodStart, periodEnd                  time.Time
	autoRenew                               bool
	// refillRef is the plan_dolum ref of this paid period ("" = none).
	refillRef string
	// orderID is the store transaction / order id for the audit row.
	orderID string
	// token is the account token in the purchase ("" = none).
	token string
	// linkedID is a previous subscription this one replaces (Google).
	linkedID string
	raw      json.RawMessage
}

// applySubscription runs the §4.3 state machine for one verified state.
// It returns the purchase status and the owner ("" = ownerless).
func (p *Purchases) applySubscription(ctx context.Context, f subFacts, submitter string) (string, string, error) {
	cur, err := p.store.SubscriptionByOriginal(ctx, f.store, f.originalID)
	if err != nil {
		return "", "", err
	}
	linked := cur
	var replaced *Subscription
	if f.linkedID != "" && f.linkedID != f.originalID {
		if replaced, err = p.store.SubscriptionByOriginal(ctx, f.store, f.linkedID); err != nil {
			return "", "", err
		}
		if linked == nil {
			linked = replaced
		}
	}
	owner, err := p.resolveOwner(ctx, f.token, linked, submitter)
	if err != nil {
		return "", "", err
	}
	// A stored living owner keeps the subscription until account deletion.
	if cur != nil && cur.UserID != nil && *cur.UserID != owner {
		if submitter != "" {
			return "", "", ErrOwnedByOther
		}
		owner = *cur.UserID
	}

	next := Subscription{
		Store: f.store, OriginalID: f.originalID, UserID: strPtr(owner), Product: f.product,
		Status: f.status, PeriodEnd: f.periodEnd, AutoRenew: f.autoRenew, Env: f.env,
	}
	// claimOnly keeps the stored state and only claims an ownerless row:
	// for a state older than the stored one (an old order, even a refund
	// of it), and for any non-active state a client sends, since only
	// webhooks and Resync are authoritative for ending a subscription. A
	// client cannot reopen an ended or refunded row either (a JWS signed
	// before the refund replays as active) unless it brings a strictly
	// later period.
	claimOnly := cur != nil && (f.periodEnd.Before(cur.PeriodEnd) ||
		(submitter != "" && (f.status != subActive ||
			((cur.Status == subRefunded || cur.Status == subExpired) && !f.periodEnd.After(cur.PeriodEnd)))))
	if claimOnly {
		next = *cur
		if next.UserID == nil {
			next.UserID = strPtr(owner)
		}
	} else if f.product == productYearly {
		restart := cur == nil || cur.Product != f.product || cur.RefillAnchor == nil || !live(cur.Status)
		if restart && f.status == subActive {
			anchor := f.periodStart
			if anchor.IsZero() {
				anchor = p.now()
			}
			nr := refillAt(anchor, 1)
			next.RefillAnchor, next.NextRefill, next.RefillCount = &anchor, &nr, 0
		} else if cur != nil {
			next.RefillAnchor, next.NextRefill, next.RefillCount = cur.RefillAnchor, cur.NextRefill, cur.RefillCount
		}
	}
	if err := p.store.UpsertSubscription(ctx, next); err != nil {
		return "", "", err
	}
	// The stored owner decides a race between two claimants.
	stored, err := p.store.SubscriptionByOriginal(ctx, f.store, f.originalID)
	if err != nil {
		return "", "", err
	}
	if stored != nil && stored.UserID != nil && *stored.UserID != owner {
		if submitter != "" {
			return "", "", ErrOwnedByOther
		}
		owner = *stored.UserID
	}
	if stored != nil && stored.UserID == nil {
		owner = ""
	}
	if replaced != nil && replaced.OriginalID != f.originalID && live(replaced.Status) && !claimOnly {
		old := *replaced
		old.Status, old.NextRefill = subExpired, nil
		if err := p.store.UpsertSubscription(ctx, old); err != nil {
			return "", "", err
		}
	}
	if f.orderID != "" {
		status := subPurchaseStatus(owner, f.status)
		if claimOnly && status == purchaseCredited {
			status = purchaseRecorded
		}
		if _, err := p.store.RecordPurchase(ctx, PurchaseRecord{
			Store: f.store, OrderID: f.orderID, UserID: strPtr(owner), Product: f.product,
			Status: status, Env: f.env, Raw: f.raw,
		}); err != nil {
			return "", "", err
		}
	}
	if claimOnly || owner == "" {
		return PurchaseRecorded, owner, nil
	}
	switch {
	case f.status == subActive && f.refillRef != "":
		end := f.periodEnd
		if _, _, err := p.store.SetPlan(ctx, owner, turPlanDolum, f.refillRef, p.cfg.PlanMonthly, &end); err != nil {
			return "", "", err
		}
		return PurchaseCredited, owner, nil
	case (f.status == subExpired || f.status == subRefunded) && cur != nil && live(cur.Status):
		ref := f.store + ":" + f.originalID + ":" + strconv.FormatInt(f.periodEnd.Unix(), 10)
		if _, _, err := p.store.SetPlan(ctx, owner, turPlanBitis, ref, 0, nil); err != nil {
			return "", "", err
		}
	}
	return PurchaseRecorded, owner, nil
}

func subPurchaseStatus(owner, status string) string {
	switch {
	case status == subRefunded:
		return purchaseRefunded
	case owner == "":
		return purchaseOwnerless
	case status == subActive:
		return purchaseCredited
	default:
		return purchaseRecorded
	}
}

// ---- Apple ----

func appleEnv(env string) string {
	if env == "Sandbox" || env == "Xcode" {
		return envSandbox
	}
	return envProduction
}

// appleProduct maps an App Store product id to the internal id.
func (p *Purchases) appleProduct(sku string) (string, bool) {
	id, ok := strings.CutPrefix(sku, p.cfg.AppleProductPrefix)
	if !ok {
		return "", false
	}
	if _, isPack := packCredits[id]; isPack || id == productMonthly || id == productYearly {
		return id, true
	}
	return "", false
}

func (p *Purchases) verifyAppleTx(jws string) (*appstore.Transaction, error) {
	tx, err := p.apple.Transaction(jws)
	if errors.Is(err, appstore.ErrInvalid) {
		return nil, fmt.Errorf("%w: %v", ErrPurchaseInvalid, err)
	}
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// appleSubFacts builds the facts of a verified subscription transaction.
func (p *Purchases) appleSubFacts(tx *appstore.Transaction, product, status string, autoRenew bool) subFacts {
	f := subFacts{
		store: storeApple, originalID: tx.OriginalTransactionID, product: product, status: status,
		env: appleEnv(tx.Environment), autoRenew: autoRenew, orderID: tx.TransactionID,
		token: tx.AppAccountToken, raw: rawJSON(tx),
	}
	if tx.PurchaseDate != nil {
		f.periodStart = *tx.PurchaseDate
	}
	if tx.ExpiresDate != nil {
		f.periodEnd = *tx.ExpiresDate
	}
	if status == subActive {
		f.refillRef = storeApple + ":" + tx.TransactionID
	}
	return f
}

// applyAppleTx applies a verified transaction: a pack is credited, a
// subscription becomes active (refill) or, when already expired, bitti.
func (p *Purchases) applyAppleTx(ctx context.Context, tx *appstore.Transaction, renewal *appstore.RenewalInfo, submitter string) (string, string, error) {
	product, ok := p.appleProduct(tx.ProductID)
	if !ok {
		return "", "", fmt.Errorf("%w: unknown product %q", ErrPurchaseInvalid, tx.ProductID)
	}
	if _, isPack := packCredits[product]; isPack {
		st, err := p.applyPack(ctx, pack{
			store: storeApple, orderID: tx.TransactionID, product: product,
			token: tx.AppAccountToken, env: appleEnv(tx.Environment), raw: rawJSON(tx),
		}, submitter)
		return st, "", err
	}
	status := subActive
	if tx.ExpiresDate == nil || !tx.ExpiresDate.After(p.now()) {
		status = subExpired
	}
	autoRenew := true
	if renewal != nil {
		autoRenew = renewal.AutoRenewStatus == 1
	}
	return p.applySubscription(ctx, p.appleSubFacts(tx, product, status, autoRenew), submitter)
}

// VerifyApple applies a StoreKit 2 signed transaction sent by userID.
func (p *Purchases) VerifyApple(ctx context.Context, userID, signedTransaction string) (PurchaseResult, error) {
	if p.apple == nil {
		return PurchaseResult{}, ErrStoreUnavailable
	}
	tx, err := p.verifyAppleTx(signedTransaction)
	if err != nil {
		return PurchaseResult{}, err
	}
	if tx.RevocationDate != nil {
		return PurchaseResult{}, fmt.Errorf("%w: revoked", ErrPurchaseInvalid)
	}
	status, _, err := p.applyAppleTx(ctx, tx, nil, userID)
	if err != nil {
		return PurchaseResult{}, err
	}
	return p.result(ctx, userID, status)
}

// ApplyAppleNotification applies a verified App Store Server Notification.
func (p *Purchases) ApplyAppleNotification(ctx context.Context, n *appstore.Notification) error {
	if p.apple == nil {
		return ErrStoreUnavailable
	}
	switch n.Type {
	case "SUBSCRIBED", "DID_RENEW", "ONE_TIME_CHARGE", "DID_CHANGE_RENEWAL_PREF",
		"DID_CHANGE_RENEWAL_STATUS", "DID_FAIL_TO_RENEW", "GRACE_PERIOD_EXPIRED", "EXPIRED", "REFUND", "REVOKE":
	default:
		return nil // CONSUMPTION_REQUEST and unknown types need nothing.
	}
	tx, err := p.verifyAppleTx(n.SignedTransactionInfo)
	if err != nil {
		return err
	}
	var renewal *appstore.RenewalInfo
	if n.SignedRenewalInfo != "" {
		if renewal, err = p.apple.Renewal(n.SignedRenewalInfo); err != nil {
			return fmt.Errorf("%w: renewal info: %v", ErrPurchaseInvalid, err)
		}
	}
	product, ok := p.appleProduct(tx.ProductID)
	if !ok {
		log.Printf("credits: apple notification %s for unknown product %q ignored", n.Type, tx.ProductID)
		return nil
	}
	_, isPack := packCredits[product]
	refund := n.Type == "REFUND" || n.Type == "REVOKE" || tx.RevocationDate != nil

	switch {
	case refund:
		if isPack {
			return p.refundPack(ctx, storeApple, tx.TransactionID, product, appleEnv(tx.Environment))
		}
		return p.setAppleStatus(ctx, tx, renewal, product, subRefunded)
	case n.Type == "SUBSCRIBED" || n.Type == "DID_RENEW" || n.Type == "ONE_TIME_CHARGE" || n.Type == "DID_CHANGE_RENEWAL_PREF":
		_, _, err = p.applyAppleTx(ctx, tx, renewal, "")
		return err
	}
	if isPack {
		return nil
	}
	switch n.Type {
	case "DID_CHANGE_RENEWAL_STATUS":
		cur, err := p.store.SubscriptionByOriginal(ctx, storeApple, tx.OriginalTransactionID)
		if err != nil {
			return err
		}
		if cur == nil {
			_, _, err = p.applyAppleTx(ctx, tx, renewal, "")
			return err
		}
		if renewal != nil {
			cur.AutoRenew = renewal.AutoRenewStatus == 1
		} else {
			cur.AutoRenew = n.Subtype == "AUTO_RENEW_ENABLED"
		}
		return p.store.UpsertSubscription(ctx, *cur)
	case "DID_FAIL_TO_RENEW":
		status := subBillingIssue
		if n.Subtype == "GRACE_PERIOD" {
			status = subGrace
		}
		return p.setAppleStatus(ctx, tx, renewal, product, status)
	default: // GRACE_PERIOD_EXPIRED, EXPIRED
		return p.setAppleStatus(ctx, tx, renewal, product, subExpired)
	}
}

// setAppleStatus applies a non-purchase state change to a subscription.
func (p *Purchases) setAppleStatus(ctx context.Context, tx *appstore.Transaction, renewal *appstore.RenewalInfo, product, status string) error {
	autoRenew := status != subExpired && status != subRefunded
	if renewal != nil {
		autoRenew = renewal.AutoRenewStatus == 1
	}
	f := p.appleSubFacts(tx, product, status, autoRenew)
	_, _, err := p.applySubscription(ctx, f, "")
	return err
}

// ---- Google ----

var googleStates = map[string]string{
	"SUBSCRIPTION_STATE_ACTIVE":          subActive,
	"SUBSCRIPTION_STATE_CANCELED":        subActive,
	"SUBSCRIPTION_STATE_IN_GRACE_PERIOD": subGrace,
	"SUBSCRIPTION_STATE_ON_HOLD":         subBillingIssue,
	"SUBSCRIPTION_STATE_PAUSED":          subExpired,
	"SUBSCRIPTION_STATE_EXPIRED":         subExpired,
}

const googleStatePending = "SUBSCRIPTION_STATE_PENDING"

func googleEnv(test bool) string {
	if test {
		return envSandbox
	}
	return envProduction
}

// googleSubFacts maps a subscriptionsv2 purchase. ok is false for a state
// that is not recorded (pending) or a product that is not ours.
func googleSubFacts(token string, sp *googleplay.SubscriptionPurchase) (subFacts, bool) {
	status, known := googleStates[sp.State]
	if !known {
		return subFacts{}, false
	}
	for _, li := range sp.LineItems {
		if li.ProductID != googleSubscription {
			continue
		}
		var product string
		switch li.BasePlanID {
		case "monthly":
			product = productMonthly
		case "yearly":
			product = productYearly
		default:
			continue
		}
		f := subFacts{
			store: storeGoogle, originalID: token, product: product, status: status,
			env: googleEnv(sp.Test), periodEnd: li.Expiry, autoRenew: li.AutoRenew,
			orderID: sp.LatestOrderID, token: sp.AccountID, linkedID: sp.LinkedPurchaseToken,
			raw: rawJSON(sp),
		}
		if sp.State == "SUBSCRIPTION_STATE_CANCELED" {
			f.autoRenew = false
		}
		if status == subActive && sp.LatestOrderID != "" {
			f.refillRef = storeGoogle + ":" + sp.LatestOrderID
		}
		if product == productYearly && !li.Expiry.IsZero() {
			f.periodStart = li.Expiry.AddDate(-1, 0, 0)
		}
		return f, true
	}
	return subFacts{}, false
}

// applyGoogleSubscription applies a re-read subscription and acknowledges
// it once it belongs to an owner.
func (p *Purchases) applyGoogleSubscription(ctx context.Context, token string, sp *googleplay.SubscriptionPurchase, submitter string) (string, error) {
	if sp.State == googleStatePending {
		return PurchasePending, nil
	}
	f, ok := googleSubFacts(token, sp)
	if !ok {
		return "", fmt.Errorf("%w: subscription state %q", ErrPurchaseInvalid, sp.State)
	}
	status, owner, err := p.applySubscription(ctx, f, submitter)
	if err != nil {
		return "", err
	}
	if owner != "" && !sp.Acknowledged && f.status == subActive {
		if err := p.google.AcknowledgeSubscription(ctx, googleSubscription, token); err != nil {
			log.Printf("credits: google acknowledge failed token=%s: %v", token, err)
		}
	}
	return status, nil
}

// applyGoogleProduct applies a re-read pack purchase and consumes it once
// it is credited.
func (p *Purchases) applyGoogleProduct(ctx context.Context, productID, token string, pp *googleplay.ProductPurchase, submitter string) (string, error) {
	switch pp.PurchaseState {
	case 0:
	case 2:
		return PurchasePending, nil
	default:
		return "", fmt.Errorf("%w: purchase state %d", ErrPurchaseInvalid, pp.PurchaseState)
	}
	if pp.OrderID == "" {
		return "", fmt.Errorf("%w: no order id", ErrPurchaseInvalid)
	}
	status, err := p.applyPack(ctx, pack{
		store: storeGoogle, orderID: pp.OrderID, product: productID,
		token: pp.AccountID, env: googleEnv(pp.Test), raw: rawJSON(pp),
	}, submitter)
	if err != nil {
		return "", err
	}
	if pp.ConsumptionState == 0 && p.packCredited(ctx, pp.OrderID) {
		if err := p.google.ConsumeProduct(ctx, productID, token); err != nil {
			log.Printf("credits: google consume failed order=%s: %v", pp.OrderID, err)
		}
	}
	return status, nil
}

// packCredited reports whether the Google order was credited to someone.
func (p *Purchases) packCredited(ctx context.Context, orderID string) bool {
	rec, err := p.store.PurchaseByOrder(ctx, storeGoogle, orderID)
	return err == nil && rec != nil && rec.Status == purchaseCredited
}

// VerifyGoogle applies a Google Play purchase sent by userID.
func (p *Purchases) VerifyGoogle(ctx context.Context, userID, productID, purchaseToken string) (PurchaseResult, error) {
	if p.google == nil {
		return PurchaseResult{}, ErrStoreUnavailable
	}
	if purchaseToken == "" {
		return PurchaseResult{}, fmt.Errorf("%w: empty token", ErrPurchaseInvalid)
	}
	var status string
	switch _, isPack := packCredits[productID]; {
	case isPack:
		pp, err := p.google.Product(ctx, productID, purchaseToken)
		if errors.Is(err, googleplay.ErrNotFound) {
			return PurchaseResult{}, fmt.Errorf("%w: %v", ErrPurchaseInvalid, err)
		}
		if err != nil {
			return PurchaseResult{}, err
		}
		if status, err = p.applyGoogleProduct(ctx, productID, purchaseToken, pp, userID); err != nil {
			return PurchaseResult{}, err
		}
	case productID == googleSubscription || productID == productMonthly || productID == productYearly:
		sp, err := p.google.Subscription(ctx, purchaseToken)
		if errors.Is(err, googleplay.ErrNotFound) {
			return PurchaseResult{}, fmt.Errorf("%w: %v", ErrPurchaseInvalid, err)
		}
		if err != nil {
			return PurchaseResult{}, err
		}
		if status, err = p.applyGoogleSubscription(ctx, purchaseToken, sp, userID); err != nil {
			return PurchaseResult{}, err
		}
	default:
		return PurchaseResult{}, fmt.Errorf("%w: unknown product %q", ErrPurchaseInvalid, productID)
	}
	return p.result(ctx, userID, status)
}

// ApplyGoogleNotification applies a real-time developer notification. The
// notification is only a trigger: the purchase is re-read from Google.
func (p *Purchases) ApplyGoogleNotification(ctx context.Context, n *googleplay.DeveloperNotification) error {
	if p.google == nil {
		return ErrStoreUnavailable
	}
	if n.PackageName != p.cfg.PackageName {
		log.Printf("credits: google notification for package %q ignored", n.PackageName)
		return nil
	}
	switch {
	case n.Subscription != nil:
		token := n.Subscription.PurchaseToken
		sp, err := p.google.Subscription(ctx, token)
		if errors.Is(err, googleplay.ErrNotFound) {
			log.Printf("credits: google subscription not found token=%s", token)
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := p.applyGoogleSubscription(ctx, token, sp, ""); err != nil && !errors.Is(err, ErrPurchaseInvalid) {
			return err
		}
		return nil
	case n.OneTime != nil:
		o := n.OneTime
		if _, isPack := packCredits[o.SKU]; !isPack {
			log.Printf("credits: google one-time notification for unknown product %q ignored", o.SKU)
			return nil
		}
		pp, err := p.google.Product(ctx, o.SKU, o.PurchaseToken)
		if errors.Is(err, googleplay.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := p.applyGoogleProduct(ctx, o.SKU, o.PurchaseToken, pp, ""); err != nil && !errors.Is(err, ErrPurchaseInvalid) {
			return err
		}
		return nil
	case n.Voided != nil:
		return p.applyGoogleVoided(ctx, n.Voided)
	}
	return nil
}

// applyGoogleVoided applies a refund or chargeback. Google offers no
// lookup for voided purchases on the Publisher surface used here, so the
// authenticated push is acted on (accepted ruling).
func (p *Purchases) applyGoogleVoided(ctx context.Context, v *googleplay.VoidedNotification) error {
	if v.ProductType == 1 { // subscription
		cur, err := p.store.SubscriptionByOriginal(ctx, storeGoogle, v.PurchaseToken)
		if err != nil || cur == nil {
			return err
		}
		periodEnd := cur.PeriodEnd
		// A voided order older than the latest one refunds a past period:
		// only that order is marked, the current period stays.
		sp, err := p.google.Subscription(ctx, v.PurchaseToken)
		if err != nil && !errors.Is(err, googleplay.ErrNotFound) {
			return err
		}
		if sp != nil && sp.LatestOrderID != "" && sp.LatestOrderID != v.OrderID {
			periodEnd = time.Time{}
		}
		f := subFacts{
			store: storeGoogle, originalID: cur.OriginalID, product: cur.Product, status: subRefunded,
			env: cur.Env, periodEnd: periodEnd, orderID: v.OrderID,
		}
		_, _, err = p.applySubscription(ctx, f, "")
		return err
	}
	rec, err := p.store.PurchaseByOrder(ctx, storeGoogle, v.OrderID)
	if err != nil {
		return err
	}
	if rec == nil {
		// Unknown order: leave a refunded tombstone so a later POST of
		// this purchase can never credit it.
		return p.refundPack(ctx, storeGoogle, v.OrderID, "", envProduction)
	}
	return p.refundPack(ctx, storeGoogle, v.OrderID, rec.Product, rec.Env)
}

// ---- Resync ----

var appleAPIStatus = map[int]string{1: subActive, 2: subExpired, 3: subBillingIssue, 4: subGrace, 5: subRefunded}

// Resync re-reads a subscription from its store and applies the state.
func (p *Purchases) Resync(ctx context.Context, s Subscription) error {
	switch s.Store {
	case storeApple:
		if p.apple == nil || p.appleAPI == nil {
			log.Printf("credits: apple resync skipped (not configured) original=%s", s.OriginalID)
			return nil
		}
		code, signedTx, signedRenewal, err := p.appleAPI.SubscriptionStatus(ctx, s.OriginalID)
		if err != nil {
			return err
		}
		status, ok := appleAPIStatus[code]
		if !ok {
			return fmt.Errorf("credit: unknown apple subscription status %d", code)
		}
		tx, err := p.verifyAppleTx(signedTx)
		if err != nil {
			return err
		}
		var renewal *appstore.RenewalInfo
		if signedRenewal != "" {
			if renewal, err = p.apple.Renewal(signedRenewal); err != nil {
				return fmt.Errorf("%w: renewal info: %v", ErrPurchaseInvalid, err)
			}
		}
		product, ok := p.appleProduct(tx.ProductID)
		if !ok {
			return fmt.Errorf("%w: unknown product %q", ErrPurchaseInvalid, tx.ProductID)
		}
		autoRenew := status == subActive
		if renewal != nil {
			autoRenew = renewal.AutoRenewStatus == 1
		}
		f := p.appleSubFacts(tx, product, status, autoRenew)
		_, _, err = p.applySubscription(ctx, f, "")
		return err
	case storeGoogle:
		if p.google == nil {
			log.Printf("credits: google resync skipped (not configured) token=%s", s.OriginalID)
			return nil
		}
		sp, err := p.google.Subscription(ctx, s.OriginalID)
		if errors.Is(err, googleplay.ErrNotFound) {
			log.Printf("credits: google subscription not found on resync token=%s", s.OriginalID)
			return nil
		}
		if err != nil {
			return err
		}
		_, err = p.applyGoogleSubscription(ctx, s.OriginalID, sp, "")
		return err
	}
	return fmt.Errorf("credit: unknown store %q", s.Store)
}

// ---- Refill ----

const (
	// maxRefillSteps bounds the catch-up of one subscription per run.
	maxRefillSteps = 12
	// resyncGrace is how long past the period end a subscription waits
	// for its renewal notification before it is re-read from the store.
	resyncGrace = time.Hour
	// actionTTL is how long spend-action rows are kept.
	actionTTL = 24 * time.Hour
)

// RunRefill refills the monthly plan of yearly subscriptions, re-reads
// overdue subscriptions from their stores and prunes old action rows.
// Per-subscription failures are logged and retried by the next run; only
// store-level failures are returned.
func (p *Purchases) RunRefill(ctx context.Context) (refilled, resynced int, err error) {
	now := p.now()
	due, err := p.store.DueRefills(ctx, now)
	if err != nil {
		return 0, 0, err
	}
	for _, s := range due {
		n, err := p.refillSubscription(ctx, s, now)
		refilled += n
		if err != nil {
			log.Printf("credits: refill failed store=%s original=%s: %v", s.Store, s.OriginalID, err)
		}
	}
	overdue, err := p.store.OverdueSubscriptions(ctx, now.Add(-resyncGrace))
	if err != nil {
		return refilled, 0, err
	}
	for _, s := range overdue {
		if err := p.Resync(ctx, s); err != nil {
			log.Printf("credits: resync failed store=%s original=%s: %v", s.Store, s.OriginalID, err)
			continue
		}
		resynced++
	}
	if _, err := p.store.PruneActions(ctx, now.Add(-actionTTL)); err != nil {
		return refilled, resynced, err
	}
	return refilled, resynced, nil
}

// refillSubscription takes the due refill steps of one yearly
// subscription. Each step sets the plan bucket under its month ref
// (idempotent), then advances the schedule with a guarded update, so a
// concurrent write that changed the schedule is never overwritten.
func (p *Purchases) refillSubscription(ctx context.Context, s Subscription, now time.Time) (int, error) {
	if s.UserID == nil || s.RefillAnchor == nil || s.NextRefill == nil {
		return 0, nil
	}
	steps := 0
	for ; steps < maxRefillSteps; steps++ {
		if s.NextRefill.After(now) || (!s.PeriodEnd.IsZero() && !s.NextRefill.Before(s.PeriodEnd)) {
			break
		}
		end := s.PeriodEnd
		var periodEnd *time.Time
		if !end.IsZero() {
			periodEnd = &end
		}
		ref := s.Store + ":" + s.OriginalID + ":" + s.NextRefill.UTC().Format("2006-01")
		if _, _, err := p.store.SetPlan(ctx, *s.UserID, turPlanDolum, ref, p.cfg.PlanMonthly, periodEnd); err != nil {
			return steps, err
		}
		next := refillAt(*s.RefillAnchor, s.RefillCount+2)
		moved, err := p.store.AdvanceRefill(ctx, s, next)
		if err != nil {
			return steps, err
		}
		if !moved {
			// Someone else changed the schedule; the next run re-reads it.
			return steps, nil
		}
		s.RefillCount++
		s.NextRefill = &next
	}
	return steps, nil
}
