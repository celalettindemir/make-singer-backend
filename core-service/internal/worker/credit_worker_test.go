package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/makeasinger/api/internal/appstore"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/googleplay"
)

type fakeApplier struct {
	apple     []*appstore.Notification
	google    []*googleplay.DeveloperNotification
	appleErr  error
	googleErr error
}

func (f *fakeApplier) ApplyAppleNotification(_ context.Context, n *appstore.Notification) error {
	f.apple = append(f.apple, n)
	return f.appleErr
}

func (f *fakeApplier) ApplyGoogleNotification(_ context.Context, n *googleplay.DeveloperNotification) error {
	f.google = append(f.google, n)
	return f.googleErr
}

type fakeNotifVerifier struct {
	gotPayload string
	err        error
}

func (f *fakeNotifVerifier) Notification(p string) (*appstore.Notification, error) {
	f.gotPayload = p
	if f.err != nil {
		return nil, f.err
	}
	return &appstore.Notification{UUID: "n1"}, nil
}

func notificationTask(store, body string) *asynq.Task {
	payload, _ := json.Marshal(map[string]string{"store": store, "body": base64.StdEncoding.EncodeToString([]byte(body))})
	return asynq.NewTask(credit.TaskTypeNotification, payload)
}

func TestCreditWorker_ReplaysAppStoreBody(t *testing.T) {
	p := &fakeApplier{}
	v := &fakeNotifVerifier{}
	w := &CreditWorker{p: p, apple: v}
	if err := w.ProcessNotification(context.Background(), notificationTask("appstore", `{"signedPayload":"jws"}`)); err != nil {
		t.Fatal(err)
	}
	if v.gotPayload != "jws" || len(p.apple) != 1 || p.apple[0].UUID != "n1" {
		t.Fatalf("payload %q, applied %v", v.gotPayload, p.apple)
	}
}

func TestCreditWorker_ProcessErrorIsReturnedForRetry(t *testing.T) {
	boom := errors.New("db down")
	w := &CreditWorker{p: &fakeApplier{appleErr: boom}, apple: &fakeNotifVerifier{}}
	err := w.ProcessNotification(context.Background(), notificationTask("appstore", `{"signedPayload":"jws"}`))
	if !errors.Is(err, boom) || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreditWorker_InvalidInnerPayloadDropped(t *testing.T) {
	w := &CreditWorker{p: &fakeApplier{appleErr: credit.ErrPurchaseInvalid}, apple: &fakeNotifVerifier{}}
	if err := w.ProcessNotification(context.Background(), notificationTask("appstore", `{"signedPayload":"jws"}`)); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestCreditWorker_NilAppleVerifierDropsReplay(t *testing.T) {
	p := &fakeApplier{}
	w := NewCreditWorker(nil, nil)
	w.p = p
	if err := w.ProcessNotification(context.Background(), notificationTask("appstore", `{"signedPayload":"jws"}`)); err != nil {
		t.Fatal(err)
	}
	if len(p.apple) != 0 {
		t.Fatal("must not apply")
	}
}

func TestCreditWorker_ReplaysGooglePush(t *testing.T) {
	p := &fakeApplier{}
	w := &CreditWorker{p: p}
	inner := `{"packageName":"pkg","voidedPurchaseNotification":{"purchaseToken":"t","orderId":"o","productType":1}}`
	body := `{"message":{"data":"` + base64.StdEncoding.EncodeToString([]byte(inner)) + `"}}`
	if err := w.ProcessNotification(context.Background(), notificationTask("googleplay", body)); err != nil {
		t.Fatal(err)
	}
	if len(p.google) != 1 || p.google[0].Voided == nil || p.google[0].PackageName != "pkg" {
		t.Fatalf("applied %+v", p.google)
	}
}

func TestCreditWorker_MalformedPayloadSkipsRetry(t *testing.T) {
	w := &CreditWorker{p: &fakeApplier{}}
	for _, task := range []*asynq.Task{
		asynq.NewTask(credit.TaskTypeNotification, []byte("nope")),
		notificationTask("mystery", "x"),
		notificationTask("googleplay", "not json"),
	} {
		if err := w.ProcessNotification(context.Background(), task); !errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("err = %v", err)
		}
	}
}

type fakeRefiller struct {
	err   error
	calls int
}

func (f *fakeRefiller) RunRefill(context.Context) (int, int, error) {
	f.calls++
	return 2, 1, f.err
}

func TestCreditWorker_ProcessRefillRunsAndFailsOnError(t *testing.T) {
	r := &fakeRefiller{}
	w := &CreditWorker{r: r}
	if err := w.ProcessRefill(context.Background(), asynq.NewTask(credit.TaskTypeRefill, nil)); err != nil || r.calls != 1 {
		t.Fatalf("err = %v calls = %d", err, r.calls)
	}
	r.err = errors.New("db down")
	err := w.ProcessRefill(context.Background(), asynq.NewTask(credit.TaskTypeRefill, nil))
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

type fakeExpirer struct {
	batches []int // lots swept per call
	calls   int
	err     error
}

func (f *fakeExpirer) ExpireLots(_ context.Context, _ time.Time, limit int) (int, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	if f.calls > len(f.batches) {
		return 0, nil
	}
	if limit != 500 {
		panic("batch must be 500")
	}
	return f.batches[f.calls-1], nil
}

type fakeSweeper struct {
	calls int
	err   error
}

func (f *fakeSweeper) Sweep(context.Context, time.Time) (int, int, error) {
	f.calls++
	return 1, 2, f.err
}

func TestCreditWorker_ExpireRunsBatchesThenSweep(t *testing.T) {
	e := &fakeExpirer{batches: []int{500, 500, 30}}
	s := &fakeSweeper{}
	w := (&CreditWorker{}).WithExpiry(e, s)
	if err := w.ProcessExpire(context.Background(), asynq.NewTask(credit.TaskTypeExpire, nil)); err != nil {
		t.Fatal(err)
	}
	if e.calls != 3 || s.calls != 1 {
		t.Fatalf("expire calls %d, sweep calls %d; want 3 and 1", e.calls, s.calls)
	}
}

func TestCreditWorker_ExpireErrorStillSweepsAndFails(t *testing.T) {
	e := &fakeExpirer{err: errors.New("db down")}
	s := &fakeSweeper{err: errors.New("sweep down")}
	w := (&CreditWorker{}).WithExpiry(e, s)
	err := w.ProcessExpire(context.Background(), asynq.NewTask(credit.TaskTypeExpire, nil))
	if err == nil || s.calls != 1 {
		t.Fatalf("err %v, sweep calls %d; want an error and the sweep to run", err, s.calls)
	}
}
