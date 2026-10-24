package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFCM_SendsV1Message(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"name":"projects/demo/messages/1"}`))
	}))
	defer srv.Close()
	f := &FCM{projectID: "demo", baseURL: srv.URL, client: srv.Client()}
	err := f.Send(context.Background(), "tok-1", Message{Title: "MakeASinger", Body: "hi", Data: map[string]string{"jobId": "j1"}})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/projects/demo/messages:send" {
		t.Errorf("path = %q", path)
	}
	msg := body["message"].(map[string]any)
	if msg["token"] != "tok-1" {
		t.Errorf("token = %v", msg["token"])
	}
	n := msg["notification"].(map[string]any)
	if n["title"] != "MakeASinger" || n["body"] != "hi" {
		t.Errorf("notification = %v", n)
	}
	if msg["data"].(map[string]any)["jobId"] != "j1" {
		t.Errorf("data = %v", msg["data"])
	}
	ch := msg["android"].(map[string]any)["notification"].(map[string]any)["channel_id"]
	if ch != "uretim" {
		t.Errorf("channel_id = %v", ch)
	}
}

func TestFCM_UnregisteredMaps(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"404", 404, `{"error":{"status":"NOT_FOUND"}}`, ErrUnregistered},
		{"error code", 400, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, ErrUnregistered},
		{"status field", 403, `{"error":{"status":"UNREGISTERED"}}`, ErrUnregistered},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			f := &FCM{projectID: "demo", baseURL: srv.URL, client: srv.Client()}
			if err := f.Send(context.Background(), "t", Message{}); !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestFCM_OtherErrorIsNotUnregistered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":{"status":"INTERNAL"}}`))
	}))
	defer srv.Close()
	f := &FCM{projectID: "demo", baseURL: srv.URL, client: srv.Client()}
	err := f.Send(context.Background(), "t", Message{})
	if err == nil || errors.Is(err, ErrUnregistered) {
		t.Errorf("err = %v", err)
	}
}

func TestNewFCM_RejectsBadCredentials(t *testing.T) {
	if _, err := NewFCM([]byte(`not json`), nil); err == nil {
		t.Error("want error for invalid JSON")
	}
	if _, err := NewFCM([]byte(`{"type":"service_account"}`), nil); err == nil {
		t.Error("want error for missing project_id/key")
	}
}
