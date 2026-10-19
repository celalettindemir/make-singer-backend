package handler

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
)

func (f *fakeLyrics) GenerateStream(ctx context.Context, _ *model.LyricsGenerateRequest, onLine func(string) error) (int, error) {
	return f.stream(ctx, onLine)
}

func (f *fakeLyrics) RewriteStream(ctx context.Context, _ *model.LyricsRewriteRequest, onLine func(string) error) (int, error) {
	return f.stream(ctx, onLine)
}

// streamApp serves the stream routes; heartbeat 0 keeps the default.
func streamApp(gen *fakeLyrics, sp credit.Spender, heartbeat time.Duration) *fiber.App {
	h := NewLyricsHandler(gen, validator.New())
	if sp != nil {
		h.WithCredits(sp)
	}
	h.heartbeat = heartbeat
	app := fiber.New()
	app.Post("/generate/stream", asUser("kullanici-a"), h.GenerateStream)
	app.Post("/rewrite/stream", asUser("kullanici-a"), h.RewriteStream)
	return app
}

// emits returns a fake stream that sends the lines, then returns err.
func emits(err error, lines ...string) func(context.Context, func(string) error) (int, error) {
	return func(_ context.Context, on func(string) error) (int, error) {
		for i, l := range lines {
			if e := on(l); e != nil {
				return i, e
			}
		}
		return len(lines), err
	}
}

func streamCall(t *testing.T, app *fiber.App, path, body, actionID string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if actionID != "" {
		req.Header.Set("X-Action-Id", actionID)
	}
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func TestLyricsStream_EventOrder(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = emits(nil, "Gece", "Şehir")
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true, Bal: 322}
	resp, body := streamCall(t, streamApp(gen, sp, 0), "/generate/stream", lyricsGenBody, actionA)
	want := "event: start\ndata: {\"balance\":322}\n\n" +
		"event: line\ndata: {\"text\":\"Gece\"}\n\n" +
		"event: line\ndata: {\"text\":\"Şehir\"}\n\n" +
		"event: done\ndata: {\"lines\":2}\n\n"
	if body != want {
		t.Errorf("body =\n%q\nwant\n%q", body, want)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" ||
		resp.Header.Get("Cache-Control") != "no-cache" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("status %d headers %v", resp.StatusCode, resp.Header)
	}
	if len(sp.ActionRefunds) != 0 {
		t.Errorf("refunds = %d, want 0", len(sp.ActionRefunds))
	}
}

func TestLyricsStream_RewriteRoute(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = emits(nil, "bir")
	_, body := streamCall(t, streamApp(gen, nil, 0), "/rewrite/stream", lyricsRewBody, "")
	if !strings.Contains(body, "event: done\ndata: {\"lines\":1}") || !strings.HasPrefix(body, "event: start\ndata: {\"balance\":0}") {
		t.Errorf("body = %q", body)
	}
}

func TestLyricsStream_PreStreamErrorsAreJSON(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		sp     *credit.FakeSpender
		action string
		status int
		code   string
	}{
		{"bad body", `{`, &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}, actionA, 400, "VALIDATION_ERROR"},
		{"invalid", `{}`, &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}, actionA, 400, "VALIDATION_ERROR"},
		{"insufficient", lyricsGenBody, &credit.FakeSpender{IsEnforced: true, Insufficient: true, Required: 2, Bal: 1}, actionA, 402, ""},
		{"expired", lyricsGenBody, &credit.FakeSpender{IsEnforced: true, Expired: true}, actionA, 400, "VALIDATION_ERROR"},
		{"missing action id", lyricsGenBody, &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}, "", 400, "VALIDATION_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gen := newFakeLyrics()
			gen.stream = emits(nil, "x")
			resp, body := streamCall(t, streamApp(gen, tc.sp, 0), "/generate/stream", tc.body, tc.action)
			if resp.StatusCode != tc.status || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
				t.Fatalf("status %d type %q body %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
			}
			if tc.code != "" && !strings.Contains(body, tc.code) {
				t.Errorf("body %s lacks %s", body, tc.code)
			}
		})
	}
}

func TestLyricsStream_ErrorBeforeLineRefunds(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = emits(errors.New("boom"))
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true, Bal: 5}
	_, body := streamCall(t, streamApp(gen, sp, 0), "/generate/stream", lyricsGenBody, actionA)
	want := "event: start\ndata: {\"balance\":5}\n\n" +
		"event: error\ndata: {\"code\":\"AI_ERROR\",\"message\":\"Yapay zekâ servisi şu anda yanıt veremiyor, lütfen tekrar deneyin.\"}\n\n"
	if body != want {
		t.Errorf("body =\n%q", body)
	}
	if len(sp.ActionRefunds) != 1 {
		t.Errorf("refunds = %d, want 1", len(sp.ActionRefunds))
	}
}

func TestLyricsStream_ErrorMessageFollowsLanguage(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = emits(errors.New("boom"))
	req := httptest.NewRequest(http.MethodPost, "/generate/stream", strings.NewReader(lyricsGenBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	resp, err := streamApp(gen, nil, 0).Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "The AI service could not respond") {
		t.Errorf("body = %s", raw)
	}
}

func TestLyricsStream_ErrorAfterLineNoRefund(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = emits(errors.New("boom"), "Gece")
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	_, body := streamCall(t, streamApp(gen, sp, 0), "/generate/stream", lyricsGenBody, actionA)
	if !strings.Contains(body, "event: line") || !strings.Contains(body, "event: error") || strings.Contains(body, "event: done") {
		t.Errorf("body = %q", body)
	}
	if len(sp.ActionRefunds) != 0 {
		t.Errorf("refunds = %d, want 0", len(sp.ActionRefunds))
	}
}

func TestLyricsStream_EmptyIsErrorAndRefund(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = emits(nil)
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	_, body := streamCall(t, streamApp(gen, sp, 0), "/generate/stream", lyricsGenBody, actionA)
	if !strings.Contains(body, "event: error") || strings.Contains(body, "event: done") {
		t.Errorf("body = %q", body)
	}
	if len(sp.ActionRefunds) != 1 {
		t.Errorf("refunds = %d, want 1", len(sp.ActionRefunds))
	}
}

func TestLyricsStream_FollowUpErrorDoesNotRefund(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = emits(errors.New("boom"))
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	app := streamApp(gen, sp, 0)
	streamCall(t, app, "/generate/stream", lyricsGenBody, actionA) // charged + refunded
	streamCall(t, app, "/generate/stream", lyricsGenBody, actionA) // follow-up: not charged
	if len(sp.ActionRefunds) != 1 {
		t.Errorf("refunds = %d, want 1", len(sp.ActionRefunds))
	}
}

func TestLyricsStream_Heartbeat(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = func(_ context.Context, on func(string) error) (int, error) {
		time.Sleep(70 * time.Millisecond)
		return 1, on("Gece")
	}
	_, body := streamCall(t, streamApp(gen, nil, 20*time.Millisecond), "/generate/stream", lyricsGenBody, "")
	ping, line := strings.Index(body, ": ping\n\n"), strings.Index(body, "event: line")
	if ping < 0 || line < 0 || ping > line {
		t.Errorf("ping at %d, line at %d, body %q", ping, line, body)
	}
}

func TestLyricsStream_ClientGoneCancelsProvider(t *testing.T) {
	gen := newFakeLyrics()
	cancelled := make(chan struct{})
	var started atomic.Bool
	gen.stream = func(ctx context.Context, on func(string) error) (int, error) {
		started.Store(true)
		if err := on("Gece"); err != nil {
			return 0, err
		}
		select {
		case <-ctx.Done():
			close(cancelled)
			return 1, ctx.Err()
		case <-time.After(10 * time.Second):
			return 1, nil
		}
	}
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	app := streamApp(gen, sp, 30*time.Millisecond)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/generate/stream", strings.NewReader(lyricsGenBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Action-Id", actionA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(resp.Body)
	for {
		l, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.HasPrefix(l, "event: line") {
			break
		}
	}
	resp.Body.Close()

	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("provider context was not cancelled after the client left")
	}
	time.Sleep(50 * time.Millisecond)
	if len(sp.ActionRefunds) != 0 {
		t.Errorf("refunds = %d, want 0", len(sp.ActionRefunds))
	}
}

func TestLyricsStream_PanicBecomesErrorAndRefunds(t *testing.T) {
	gen := newFakeLyrics()
	gen.stream = func(context.Context, func(string) error) (int, error) { panic("sdk edge case") }
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	_, body := streamCall(t, streamApp(gen, sp, 0), "/generate/stream", lyricsGenBody, actionA)
	if !strings.Contains(body, "event: error") || strings.Contains(body, "event: done") {
		t.Errorf("body = %q", body)
	}
	if len(sp.ActionRefunds) != 1 {
		t.Errorf("refunds = %d, want 1", len(sp.ActionRefunds))
	}
}
