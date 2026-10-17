package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/credit"
)

type fakeAppleVerifier struct{ err error }

func (f fakeAppleVerifier) Notification(string) (*appstore.Notification, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &appstore.Notification{UUID: "n1"}, nil
}

type fakePushAuth struct{ err error }

func (f fakePushAuth) Verify(string) error { return f.err }

type fakeQueue struct {
	tasks []*asynq.Task
	opts  [][]asynq.Option
	err   error
}

func (q *fakeQueue) Enqueue(t *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.err != nil {
		return nil, q.err
	}
	q.tasks = append(q.tasks, t)
	q.opts = append(q.opts, opts)
	return &asynq.TaskInfo{}, nil
}

func webhooksApp(p PurchaseProcessor, apple NotificationVerifier, google PushAuth, q Enqueuer) *fiber.App {
	h := NewWebhooksHandler(p, apple, google, q)
	app := fiber.New()
	app.Post("/api/webhooks/appstore", h.AppStore)
	app.Post("/api/webhooks/googleplay", h.GooglePlay)
	return app
}

func TestWebhookAppStore_BadSignature400(t *testing.T) {
	p := &fakeProcessor{}
	app := webhooksApp(p, fakeAppleVerifier{err: appstore.ErrInvalid}, nil, &fakeQueue{})
	status, out := postJSON(t, app, "/api/webhooks/appstore", `{"signedPayload":"x"}`, nil)
	if status != 400 || errorCode(out) != "VALIDATION_ERROR" {
		t.Fatalf("status %d, %v", status, out)
	}
	if p.appleCalls != 0 {
		t.Fatal("notification must not be applied")
	}
}

func TestWebhookAppStore_MissingPayload400(t *testing.T) {
	app := webhooksApp(&fakeProcessor{}, fakeAppleVerifier{}, nil, &fakeQueue{})
	if status, _ := postJSON(t, app, "/api/webhooks/appstore", `{}`, nil); status != 400 {
		t.Fatalf("status = %d", status)
	}
}

func TestWebhookAppStore_Processed200(t *testing.T) {
	q := &fakeQueue{}
	p := &fakeProcessor{}
	app := webhooksApp(p, fakeAppleVerifier{}, nil, q)
	if status, _ := postJSON(t, app, "/api/webhooks/appstore", `{"signedPayload":"x"}`, nil); status != 200 {
		t.Fatalf("status = %d", status)
	}
	if p.appleCalls != 1 || len(q.tasks) != 0 {
		t.Fatalf("calls %d, queued %d", p.appleCalls, len(q.tasks))
	}
}

func TestWebhookAppStore_ProcessErrorEnqueuesAnd200(t *testing.T) {
	q := &fakeQueue{}
	p := &fakeProcessor{appleErr: errors.New("db down")}
	app := webhooksApp(p, fakeAppleVerifier{}, nil, q)
	body := `{"signedPayload":"x"}`
	if status, _ := postJSON(t, app, "/api/webhooks/appstore", body, nil); status != 200 {
		t.Fatalf("status = %d", status)
	}
	if len(q.tasks) != 1 {
		t.Fatalf("queued %d tasks", len(q.tasks))
	}
	if q.tasks[0].Type() != "kredi:bildirim" {
		t.Fatalf("type = %q", q.tasks[0].Type())
	}
	var payload map[string]string
	if err := json.Unmarshal(q.tasks[0].Payload(), &payload); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(payload["body"])
	if payload["store"] != "appstore" || string(raw) != body {
		t.Fatalf("payload = %v", payload)
	}
	var queue string
	var retry int
	for _, o := range q.opts[0] {
		switch o.Type() {
		case asynq.QueueOpt:
			queue = o.Value().(string)
		case asynq.MaxRetryOpt:
			retry = o.Value().(int)
		}
	}
	if queue != credit.QueueCredit || retry != 10 {
		t.Fatalf("queue %q retry %d", queue, retry)
	}
}

func TestWebhookAppStore_InvalidInnerPayloadNotEnqueued(t *testing.T) {
	q := &fakeQueue{}
	p := &fakeProcessor{appleErr: credit.ErrPurchaseInvalid}
	app := webhooksApp(p, fakeAppleVerifier{}, nil, q)
	if status, _ := postJSON(t, app, "/api/webhooks/appstore", `{"signedPayload":"x"}`, nil); status != 200 {
		t.Fatalf("status = %d", status)
	}
	if len(q.tasks) != 0 {
		t.Fatal("an unverifiable payload must not be retried")
	}
}

func TestWebhookAppStore_EnqueueFailure500(t *testing.T) {
	p := &fakeProcessor{appleErr: errors.New("db down")}
	app := webhooksApp(p, fakeAppleVerifier{}, nil, &fakeQueue{err: errors.New("redis down")})
	if status, _ := postJSON(t, app, "/api/webhooks/appstore", `{"signedPayload":"x"}`, nil); status != 500 {
		t.Fatalf("status = %d", status)
	}
}

func googlePush(inner string) string {
	return `{"message":{"data":"` + base64.StdEncoding.EncodeToString([]byte(inner)) + `"}}`
}

func TestWebhookGooglePlay_NoBearer401(t *testing.T) {
	p := &fakeProcessor{}
	app := webhooksApp(p, nil, fakePushAuth{err: errors.New("no bearer")}, &fakeQueue{})
	status, out := postJSON(t, app, "/api/webhooks/googleplay", googlePush(`{"packageName":"p","testNotification":{}}`), nil)
	if status != http.StatusUnauthorized || errorCode(out) != "UNAUTHORIZED" {
		t.Fatalf("status %d, %v", status, out)
	}
	if p.googleCalls != 0 {
		t.Fatal("must not process")
	}
}

func TestWebhookGooglePlay_TestNotification200(t *testing.T) {
	p := &fakeProcessor{}
	app := webhooksApp(p, nil, fakePushAuth{}, &fakeQueue{})
	status, _ := postJSON(t, app, "/api/webhooks/googleplay", googlePush(`{"packageName":"p","testNotification":{"version":"1.0"}}`), nil)
	if status != 200 || p.googleCalls != 0 {
		t.Fatalf("status %d, calls %d", status, p.googleCalls)
	}
}

func TestWebhookGooglePlay_ProcessErrorEnqueuesAnd200(t *testing.T) {
	q := &fakeQueue{}
	p := &fakeProcessor{googleErr: errors.New("db down")}
	app := webhooksApp(p, nil, fakePushAuth{}, q)
	body := googlePush(`{"packageName":"p","voidedPurchaseNotification":{"purchaseToken":"t","orderId":"o","productType":1}}`)
	if status, _ := postJSON(t, app, "/api/webhooks/googleplay", body, nil); status != 200 {
		t.Fatalf("status = %d", status)
	}
	if len(q.tasks) != 1 {
		t.Fatalf("queued %d", len(q.tasks))
	}
	var payload map[string]string
	_ = json.Unmarshal(q.tasks[0].Payload(), &payload)
	if payload["store"] != "googleplay" {
		t.Fatalf("payload = %v", payload)
	}
}

func TestWebhookGooglePlay_UndecodableBodyAcked(t *testing.T) {
	app := webhooksApp(&fakeProcessor{}, nil, fakePushAuth{}, &fakeQueue{})
	if status, _ := postJSON(t, app, "/api/webhooks/googleplay", `{"message":{}}`, nil); status != 200 {
		t.Fatalf("status = %d", status)
	}
}
