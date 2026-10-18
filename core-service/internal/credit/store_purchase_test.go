package credit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStore_AccountByStoreToken(t *testing.T) {
	t.Skip("moved to lots in Task 3")
	s, u := newStore(t)
	ctx := context.Background()
	a, err := s.EnsureAccount(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AccountByStoreToken(ctx, a.StoreToken)
	if err != nil || got == nil || got.UserID != u {
		t.Fatalf("by token: %+v %v", got, err)
	}
	if got, err := s.AccountByStoreToken(ctx, uuid.New()); err != nil || got != nil {
		t.Fatalf("unknown token: %+v %v", got, err)
	}
}

func TestStore_UpsertSubscriptionKeepsOwner(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	end := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	sub := Subscription{Store: "apple", OriginalID: "o1", UserID: &u, Product: "pro.monthly", Status: "aktif", PeriodEnd: end, AutoRenew: true, Env: "production"}
	if err := s.UpsertSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub.UserID, sub.Status = nil, "bitti"
	if err := s.UpsertSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	got, err := s.SubscriptionByOriginal(ctx, "apple", "o1")
	if err != nil || got == nil || got.UserID == nil || *got.UserID != u || got.Status != "bitti" || !got.PeriodEnd.Equal(end) {
		t.Fatalf("got %+v %v", got, err)
	}
	if got, err := s.SubscriptionByOriginal(ctx, "google", "o1"); err != nil || got != nil {
		t.Fatalf("other store: %+v %v", got, err)
	}
}

func TestStore_RecordPurchaseStatusRules(t *testing.T) {
	s, u := newStore(t)
	ctx := context.Background()
	rec := PurchaseRecord{Store: "google", OrderID: "GPA.1", Product: "credits.100", Credits: 100, Status: purchaseOwnerless, Env: "production", Raw: []byte(`{"a":1}`)}
	if ins, err := s.RecordPurchase(ctx, rec); err != nil || !ins {
		t.Fatalf("insert: %v %v", ins, err)
	}
	rec.UserID, rec.Status = &u, purchaseCredited
	if ins, err := s.RecordPurchase(ctx, rec); err != nil || ins {
		t.Fatalf("claim: %v %v", ins, err)
	}
	other := uuid.NewString()
	rec.UserID, rec.Status = &other, purchaseProcessing
	if _, err := s.RecordPurchase(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.PurchaseByOrder(ctx, "google", "GPA.1")
	if err != nil || got == nil || got.UserID == nil || *got.UserID != u || got.Status != purchaseCredited || got.Credits != 100 {
		t.Fatalf("after claim: %+v %v", got, err)
	}
	rec.Status = purchaseRefunded
	if _, err := s.RecordPurchase(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.Status = purchaseCredited
	if _, err := s.RecordPurchase(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PurchaseByOrder(ctx, "google", "GPA.1"); got.Status != purchaseRefunded {
		t.Fatalf("refunded row moved to %q", got.Status)
	}
	if got, err := s.PurchaseByOrder(ctx, "google", "missing"); err != nil || got != nil {
		t.Fatalf("missing: %+v %v", got, err)
	}
}
