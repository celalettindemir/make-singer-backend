package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/googleplay"
)

type fakeProcessor struct {
	result      credit.PurchaseResult
	err         error
	appleErr    error
	googleErr   error
	gotUser     string
	gotProduct  string
	appleCalls  int
	googleCalls int
}

func (f *fakeProcessor) VerifyApple(_ context.Context, userID, _ string) (credit.PurchaseResult, error) {
	f.gotUser = userID
	return f.result, f.err
}

func (f *fakeProcessor) VerifyGoogle(_ context.Context, userID, productID, _ string) (credit.PurchaseResult, error) {
	f.gotUser, f.gotProduct = userID, productID
	return f.result, f.err
}

func (f *fakeProcessor) ApplyAppleNotification(context.Context, *appstore.Notification) error {
	f.appleCalls++
	return f.appleErr
}

func (f *fakeProcessor) ApplyGoogleNotification(context.Context, *googleplay.DeveloperNotification) error {
	f.googleCalls++
	return f.googleErr
}

func purchasesApp(userID string, p PurchaseProcessor) *fiber.App {
	h := NewPurchasesHandler(p, validator.New())
	app := fiber.New()
	g := app.Group("/api/purchases", asUser(userID))
	g.Post("/apple", h.Apple)
	g.Post("/google", h.Google)
	return app
}

func postJSON(t *testing.T, app *fiber.App, path, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestPurchasesApple_200Credited(t *testing.T) {
	p := &fakeProcessor{result: credit.PurchaseResult{Status: credit.PurchaseCredited, Summary: credit.Summary{Balance: 100}}}
	status, out := postJSON(t, purchasesApp("u1", p), "/api/purchases/apple", `{"signedTransaction":"jws"}`, nil)
	if status != 200 {
		t.Fatalf("status = %d, body %v", status, out)
	}
	if out["purchaseStatus"] != "credited" {
		t.Fatalf("purchaseStatus = %v", out["purchaseStatus"])
	}
	if _, ok := out["credits"].(map[string]any); !ok {
		t.Fatalf("credits missing: %v", out)
	}
	if p.gotUser != "u1" {
		t.Fatalf("user = %q", p.gotUser)
	}
}

func TestPurchasesApple_InvalidBody400(t *testing.T) {
	app := purchasesApp("u1", &fakeProcessor{})
	for _, body := range []string{`not json`, `{}`, `{"signedTransaction":""}`} {
		status, out := postJSON(t, app, "/api/purchases/apple", body, nil)
		if status != 400 || errorCode(out) != "VALIDATION_ERROR" {
			t.Fatalf("body %q: status %d, %v", body, status, out)
		}
	}
}

func TestPurchases_NoUser401(t *testing.T) {
	status, _ := postJSON(t, purchasesApp("", &fakeProcessor{}), "/api/purchases/apple", `{"signedTransaction":"x"}`, nil)
	if status != 401 {
		t.Fatalf("status = %d", status)
	}
}

func TestPurchases_Invalid422(t *testing.T) {
	p := &fakeProcessor{err: errors.Join(credit.ErrPurchaseInvalid)}
	status, out := postJSON(t, purchasesApp("u1", p), "/api/purchases/apple", `{"signedTransaction":"x"}`, nil)
	if status != 422 || errorCode(out) != "PURCHASE_INVALID" {
		t.Fatalf("status %d, %v", status, out)
	}
}

func TestPurchases_OwnedByOther409(t *testing.T) {
	p := &fakeProcessor{err: credit.ErrOwnedByOther}
	status, out := postJSON(t, purchasesApp("u1", p), "/api/purchases/apple", `{"signedTransaction":"x"}`, nil)
	if status != 409 || errorCode(out) != "PURCHASE_OWNED_BY_OTHER" {
		t.Fatalf("status %d, %v", status, out)
	}
}

func TestPurchases_NotConfigured503(t *testing.T) {
	p := &fakeProcessor{err: credit.ErrStoreUnavailable}
	status, out := postJSON(t, purchasesApp("u1", p), "/api/purchases/google", `{"productId":"pro","purchaseToken":"t"}`, nil)
	if status != 503 || errorCode(out) != "SERVICE_ERROR" {
		t.Fatalf("status %d, %v", status, out)
	}
}

func TestPurchases_UnexpectedError500(t *testing.T) {
	p := &fakeProcessor{err: errors.New("db down")}
	status, out := postJSON(t, purchasesApp("u1", p), "/api/purchases/apple", `{"signedTransaction":"x"}`, nil)
	if status != 500 || errorCode(out) != "SERVICE_ERROR" {
		t.Fatalf("status %d, %v", status, out)
	}
}

func TestPurchasesGoogle_UnknownProduct422(t *testing.T) {
	p := &fakeProcessor{err: credit.ErrPurchaseInvalid}
	status, out := postJSON(t, purchasesApp("u1", p), "/api/purchases/google", `{"productId":"credits.999","purchaseToken":"t"}`, nil)
	if status != 422 || errorCode(out) != "PURCHASE_INVALID" {
		t.Fatalf("status %d, %v", status, out)
	}
	if p.gotProduct != "credits.999" {
		t.Fatalf("product = %q", p.gotProduct)
	}
}

func TestPurchasesGoogle_MissingToken400(t *testing.T) {
	status, _ := postJSON(t, purchasesApp("u1", &fakeProcessor{}), "/api/purchases/google", `{"productId":"pro"}`, nil)
	if status != 400 {
		t.Fatalf("status = %d", status)
	}
}

func TestPurchases_TransientStoreFailure503(t *testing.T) {
	p := &fakeProcessor{err: fmt.Errorf("%w: googleplay: unexpected status 500", credit.ErrStoreTransient)}
	status, out := postJSON(t, purchasesApp("u1", p), "/api/purchases/google", `{"productId":"pro","purchaseToken":"t"}`, nil)
	if status != 503 || errorCode(out) != "SERVICE_ERROR" {
		t.Fatalf("status %d, %v", status, out)
	}
}
