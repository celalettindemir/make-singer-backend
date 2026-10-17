package googleplay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestPublisher(t *testing.T, h http.HandlerFunc) *Publisher {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewPublisherWithClient(srv.Client(), "com.example.app", srv.URL)
}

func TestPublisher_SubscriptionParsesV2(t *testing.T) {
	p := newTestPublisher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/androidpublisher/v3/applications/com.example.app/purchases/subscriptionsv2/tokens/tok1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"subscriptionState":"SUBSCRIPTION_STATE_ACTIVE","latestOrderId":"GPA.1","linkedPurchaseToken":"old",
"acknowledgementState":"ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED","externalAccountIdentifiers":{"obfuscatedExternalAccountId":"acc"},
"testPurchase":{},"lineItems":[{"productId":"plan","expiryTime":"2026-11-01T10:00:00Z","offerDetails":{"basePlanId":"monthly"},"autoRenewingPlan":{"autoRenewEnabled":true}}]}`))
	})
	got, err := p.Subscription(context.Background(), "tok1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "SUBSCRIPTION_STATE_ACTIVE" || got.LatestOrderID != "GPA.1" || got.LinkedPurchaseToken != "old" ||
		got.AccountID != "acc" || !got.Test || !got.Acknowledged || len(got.LineItems) != 1 {
		t.Fatalf("bad parse: %+v", got)
	}
	li := got.LineItems[0]
	if li.ProductID != "plan" || li.BasePlanID != "monthly" || !li.AutoRenew || !li.Expiry.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("bad line item: %+v", li)
	}
}

func TestPublisher_SubscriptionRealPurchaseIsNotTest(t *testing.T) {
	p := newTestPublisher(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"subscriptionState":"SUBSCRIPTION_STATE_PENDING"}`))
	})
	got, err := p.Subscription(context.Background(), "t")
	if err != nil || got.Test || got.Acknowledged {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestPublisher_ProductParses(t *testing.T) {
	p := newTestPublisher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/androidpublisher/v3/applications/com.example.app/purchases/products/pack/tokens/tok" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"orderId":"GPA.2","purchaseState":0,"consumptionState":1,"obfuscatedExternalAccountId":"acc","purchaseType":0}`))
	})
	got, err := p.Product(context.Background(), "pack", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if got.OrderID != "GPA.2" || got.PurchaseState != 0 || got.ConsumptionState != 1 || got.AccountID != "acc" || !got.Test {
		t.Fatalf("bad parse: %+v", got)
	}
}

func TestPublisher_ProductAbsentPurchaseTypeIsReal(t *testing.T) {
	p := newTestPublisher(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderId":"GPA.3"}`))
	})
	got, err := p.Product(context.Background(), "pack", "tok")
	if err != nil || got.Test {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestPublisher_404IsErrNotFound(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusGone} {
		p := newTestPublisher(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
		if _, err := p.Subscription(context.Background(), "t"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("status %d: want ErrNotFound, got %v", code, err)
		}
	}
}

func TestPublisher_ServerErrorIsNotErrNotFound(t *testing.T) {
	p := newTestPublisher(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	_, err := p.Product(context.Background(), "p", "t")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestPublisher_AcknowledgeAndConsumePaths(t *testing.T) {
	var got []string
	p := newTestPublisher(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.EscapedPath())
		w.WriteHeader(http.StatusNoContent)
	})
	if err := p.AcknowledgeSubscription(context.Background(), "sub1", "tok"); err != nil {
		t.Fatal(err)
	}
	if err := p.ConsumeProduct(context.Background(), "pack", "tok"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/purchases/subscriptions/sub1/tokens/tok:acknowledge",
		"POST /androidpublisher/v3/applications/com.example.app/purchases/products/pack/tokens/tok:consume",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v", got)
	}
}
