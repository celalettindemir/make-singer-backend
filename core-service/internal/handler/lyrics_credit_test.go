package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/model"
)

const (
	lyricsGenBody = `{"genre":"pop","sectionType":"verse","vibes":["happy"]}`
	lyricsRewBody = `{"currentLyrics":"la la","genre":"pop","sectionType":"verse","vibes":["happy"]}`
	actionA       = "11111111-1111-4111-8111-111111111111"
)

type fakeLyrics struct {
	mock     bool
	err      error
	drafts   [][]string
	lines    []string
	genCalls int
	rewCalls int
}

func newFakeLyrics() *fakeLyrics {
	return &fakeLyrics{drafts: [][]string{{"a line"}}, lines: []string{"a line"}}
}

func (f *fakeLyrics) Generate(context.Context, *model.LyricsGenerateRequest) (*model.LyricsGenerateResponse, error) {
	f.genCalls++
	if f.err != nil {
		return nil, f.err
	}
	return &model.LyricsGenerateResponse{Drafts: f.drafts}, nil
}

func (f *fakeLyrics) Rewrite(context.Context, *model.LyricsRewriteRequest) (*model.LyricsRewriteResponse, error) {
	f.rewCalls++
	if f.err != nil {
		return nil, f.err
	}
	return &model.LyricsRewriteResponse{Lines: f.lines}, nil
}

func (f *fakeLyrics) IsMock() bool { return f.mock }

func lyricsApp(gen *fakeLyrics, sp credit.Spender) *fiber.App {
	h := NewLyricsHandler(gen, validator.New())
	if sp != nil {
		h.WithCredits(sp)
	}
	app := fiber.New()
	app.Post("/generate", asUser("kullanici-a"), h.Generate)
	app.Post("/rewrite", asUser("kullanici-a"), h.Rewrite)
	return app
}

func lyricsCall(t *testing.T, app *fiber.App, path, body, actionID string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if actionID != "" {
		req.Header.Set("X-Action-Id", actionID)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errDetails(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	e, _ := body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d
}

func TestLyrics_EnforcedMissingActionID400(t *testing.T) {
	gen := newFakeLyrics()
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	status, body := lyricsCall(t, lyricsApp(gen, sp), "/generate", lyricsGenBody, "")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if errDetails(t, body)["X-Action-Id"] != "required" {
		t.Errorf("details = %v, want X-Action-Id required", errDetails(t, body))
	}
	if len(sp.Actions) != 0 || gen.genCalls != 0 {
		t.Errorf("spend calls %d, generator calls %d, want 0 and 0", len(sp.Actions), gen.genCalls)
	}
}

func TestLyrics_ShadowMissingActionIDOK(t *testing.T) {
	gen := newFakeLyrics()
	sp := &credit.FakeSpender{IsEnforced: false, ChargedFirst: true, Bal: 40}
	status, body := lyricsCall(t, lyricsApp(gen, sp), "/generate", lyricsGenBody, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["balance"] != float64(40) {
		t.Errorf("balance = %v, want 40", body["balance"])
	}
	if len(sp.Actions) != 0 {
		t.Errorf("shadow mode made %d spend calls, want 0", len(sp.Actions))
	}
}

func TestLyrics_InvalidActionIDNotUUID400(t *testing.T) {
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	status, body := lyricsCall(t, lyricsApp(newFakeLyrics(), sp), "/rewrite", lyricsRewBody, "not-a-uuid")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if errDetails(t, body)["X-Action-Id"] != "required" {
		t.Errorf("details = %v, want X-Action-Id required", errDetails(t, body))
	}
	if len(sp.Actions) != 0 {
		t.Errorf("spend calls = %d, want 0", len(sp.Actions))
	}
}

func TestLyrics_GenerateChargesFirstRequestOnly(t *testing.T) {
	gen := newFakeLyrics()
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true, Bal: 7}
	app := lyricsApp(gen, sp)
	for i := 0; i < 2; i++ {
		status, body := lyricsCall(t, app, "/generate", lyricsGenBody, actionA)
		if status != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200", i, status)
		}
		if body["balance"] != float64(7) {
			t.Errorf("call %d balance = %v, want 7", i, body["balance"])
		}
	}
	if len(sp.Actions) != 2 {
		t.Fatalf("SpendAction calls = %d, want 2", len(sp.Actions))
	}
	if sp.Actions[0].Op != credit.OpLyricsGenerate || sp.Actions[0].ID != actionA || sp.Actions[0].User != "kullanici-a" {
		t.Errorf("spend call = %+v", sp.Actions[0])
	}
	if len(sp.ActionRefunds) != 0 {
		t.Errorf("refunds = %d, want 0", len(sp.ActionRefunds))
	}
}

func TestLyrics_Insufficient402(t *testing.T) {
	gen := newFakeLyrics()
	sp := &credit.FakeSpender{IsEnforced: true, Insufficient: true, Required: 3, Bal: 1, Debt: 70}
	status, body := lyricsCall(t, lyricsApp(gen, sp), "/generate", lyricsGenBody, actionA)
	if status != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", status)
	}
	d := errDetails(t, body)
	if d["required"] != float64(3) || d["balance"] != float64(1) || d["debt"] != float64(70) {
		t.Errorf("details = %v, want required 3 balance 1", d)
	}
	if gen.genCalls != 0 {
		t.Errorf("generator ran %d times, want 0", gen.genCalls)
	}
}

func TestLyrics_FirstRequestAIErrorRefunds(t *testing.T) {
	gen := newFakeLyrics()
	gen.err = errors.New("groq down")
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	status, body := lyricsCall(t, lyricsApp(gen, sp), "/generate", lyricsGenBody, actionA)
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	if body["error"].(map[string]any)["code"] != "AI_ERROR" {
		t.Errorf("body = %v, want AI_ERROR", body)
	}
	if len(sp.ActionRefunds) != 1 || sp.ActionRefunds[0].ID != actionA || sp.ActionRefunds[0].Op != credit.OpLyricsGenerate {
		t.Errorf("refunds = %+v, want one for the action", sp.ActionRefunds)
	}
}

func TestLyrics_FirstRequestEmptyDraftRefunds(t *testing.T) {
	cases := []struct {
		name, path, body string
		set              func(*fakeLyrics)
		op               credit.Operation
	}{
		{"no drafts", "/generate", lyricsGenBody, func(f *fakeLyrics) { f.drafts = [][]string{} }, credit.OpLyricsGenerate},
		{"blank drafts", "/generate", lyricsGenBody, func(f *fakeLyrics) { f.drafts = [][]string{{" ", ""}, {"\t"}} }, credit.OpLyricsGenerate},
		{"blank rewrite", "/rewrite", lyricsRewBody, func(f *fakeLyrics) { f.lines = []string{"  ", ""} }, credit.OpLyricsRewrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gen := newFakeLyrics()
			tc.set(gen)
			sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
			status, body := lyricsCall(t, lyricsApp(gen, sp), tc.path, tc.body, actionA)
			if status != http.StatusBadGateway || body["error"].(map[string]any)["code"] != "AI_ERROR" {
				t.Fatalf("status = %d body = %v, want 502 AI_ERROR", status, body)
			}
			if len(sp.ActionRefunds) != 1 || sp.ActionRefunds[0].Op != tc.op {
				t.Errorf("refunds = %+v, want one for %s", sp.ActionRefunds, tc.op)
			}
		})
	}
}

func TestLyrics_FollowUpErrorNoRefund(t *testing.T) {
	gen := newFakeLyrics()
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	app := lyricsApp(gen, sp)
	if status, _ := lyricsCall(t, app, "/generate", lyricsGenBody, actionA); status != http.StatusOK {
		t.Fatalf("first status = %d, want 200", status)
	}
	gen.err = errors.New("groq down")
	if status, _ := lyricsCall(t, app, "/generate", lyricsGenBody, actionA); status != http.StatusBadGateway {
		t.Fatalf("follow-up status = %d, want 502", status)
	}
	if len(sp.ActionRefunds) != 0 {
		t.Errorf("refunds = %+v, want none for a follow-up", sp.ActionRefunds)
	}
}

func TestLyrics_ActionExpired400(t *testing.T) {
	gen := newFakeLyrics()
	sp := &credit.FakeSpender{IsEnforced: true, Expired: true}
	status, body := lyricsCall(t, lyricsApp(gen, sp), "/rewrite", lyricsRewBody, actionA)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if errDetails(t, body)["X-Action-Id"] != "action_expired" {
		t.Errorf("details = %v, want action_expired", errDetails(t, body))
	}
	if gen.rewCalls != 0 {
		t.Errorf("generator ran %d times, want 0", gen.rewCalls)
	}
}

func TestLyrics_UnknownOperationIs500(t *testing.T) {
	sp := &credit.FakeSpender{IsEnforced: true, Err: credit.ErrUnknownOperation}
	status, _ := lyricsCall(t, lyricsApp(newFakeLyrics(), sp), "/generate", lyricsGenBody, actionA)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
}

func TestLyrics_MockModeNoDebit(t *testing.T) {
	gen := newFakeLyrics()
	gen.mock = true
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true, Bal: 25}
	status, body := lyricsCall(t, lyricsApp(gen, sp), "/rewrite", lyricsRewBody, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["balance"] != float64(25) {
		t.Errorf("balance = %v, want 25", body["balance"])
	}
	if len(sp.Actions) != 0 {
		t.Errorf("mock mode made %d spend calls, want 0", len(sp.Actions))
	}
}

func TestLyrics_NilSpenderBalanceZero(t *testing.T) {
	status, body := lyricsCall(t, lyricsApp(newFakeLyrics(), nil), "/generate", lyricsGenBody, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["balance"] != float64(0) {
		t.Errorf("balance = %v, want 0", body["balance"])
	}
}

func TestLyrics_NoUserWithSpender401(t *testing.T) {
	h := NewLyricsHandler(newFakeLyrics(), validator.New()).WithCredits(&credit.FakeSpender{IsEnforced: true})
	app := fiber.New()
	app.Post("/generate", asUser(""), h.Generate)
	status, _ := lyricsCall(t, app, "/generate", lyricsGenBody, actionA)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestLyrics_ShadowBalanceFailureStillOK(t *testing.T) {
	sp := &credit.FakeSpender{IsEnforced: false, Err: errors.New("db down")}
	status, body := lyricsCall(t, lyricsApp(newFakeLyrics(), sp), "/generate", lyricsGenBody, "")
	if status != http.StatusOK || body["balance"] != float64(0) {
		t.Errorf("status = %d balance = %v, want 200 and 0", status, body["balance"])
	}
}

func TestLyrics_EnforcedMockBalanceFailure500(t *testing.T) {
	gen := newFakeLyrics()
	gen.mock = true
	sp := &credit.FakeSpender{IsEnforced: true, Err: errors.New("db down")}
	status, _ := lyricsCall(t, lyricsApp(gen, sp), "/generate", lyricsGenBody, "")
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", status)
	}
}

func TestLyrics_UnchargedEmptyResultKeepsOldBehavior(t *testing.T) {
	gen := newFakeLyrics()
	gen.drafts = [][]string{{"  "}}
	// Shadow mode charges nothing, so the empty-result 502 does not apply.
	sp := &credit.FakeSpender{IsEnforced: false}
	if status, _ := lyricsCall(t, lyricsApp(gen, sp), "/generate", lyricsGenBody, ""); status != http.StatusOK {
		t.Errorf("shadow: status = %d, want 200", status)
	}
	// Nil spender: same.
	if status, _ := lyricsCall(t, lyricsApp(gen, nil), "/generate", lyricsGenBody, ""); status != http.StatusOK {
		t.Errorf("nil spender: status = %d, want 200", status)
	}
}

func TestLyricsHandler_BadSectionIndexIs400NoCharge(t *testing.T) {
	gen := newFakeLyrics()
	sp := &credit.FakeSpender{IsEnforced: true, ChargedFirst: true}
	app := lyricsApp(gen, sp)
	bodies := map[string]string{
		"/generate": `{"genre":"pop","sectionType":"chorus","vibes":["happy"],"song":[{"type":"verse"}],"sectionIndex":0}`,
		"/rewrite":  `{"currentLyrics":"la","genre":"pop","sectionType":"verse","vibes":["happy"],"song":[{"type":"verse"}]}`,
	}
	for path, body := range bodies {
		status, out := lyricsCall(t, app, path, body, actionA)
		if status != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, status)
		}
		if errDetails(t, out)["sectionIndex"] != "invalid" {
			t.Errorf("%s details = %v", path, errDetails(t, out))
		}
	}
	if len(sp.Actions) != 0 || gen.genCalls != 0 || gen.rewCalls != 0 {
		t.Errorf("spends %d, gen %d, rew %d; want none", len(sp.Actions), gen.genCalls, gen.rewCalls)
	}
}

func TestLyricsHandler_ContextRequestOK(t *testing.T) {
	gen := newFakeLyrics()
	body := `{"genre":"pop","sectionType":"chorus","vibes":["happy"],"topic":"t","bpm":96,"song":[{"type":"verse","lyrics":"a"},{"type":"chorus"}],"sectionIndex":1}`
	if status, _ := lyricsCall(t, lyricsApp(gen, nil), "/generate", body, actionA); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}
