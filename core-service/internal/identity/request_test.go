package identity

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// testRedis wants a local Redis. If there is none the test is skipped: unit
// tests have to pass without a network (see Global Constraints).
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("no local Redis, skipping")
	}
	t.Cleanup(func() { _ = rdb.FlushDB(context.Background()).Err(); _ = rdb.Close() })
	return rdb
}

func TestAuthRequest_SatisfiesInterface(t *testing.T) {
	var _ op.AuthRequest = &AuthRequest{}
}

func TestRequestStore_CreateAndGet(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{
		ClientID:            "makesinger-mobil",
		RedirectURI:         "com.makesinger.app:/oauth2redirect",
		Scopes:              oidc.SpaceDelimitedArray{oidc.ScopeOpenID},
		ResponseType:        oidc.ResponseTypeCode,
		CodeChallenge:       "abc",
		CodeChallengeMethod: oidc.CodeChallengeMethodS256,
		State:               "durum1",
		Nonce:               "n1",
	}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	readBack, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if readBack.GetNonce() != "n1" {
		t.Errorf("nonce = %q, want n1", readBack.GetNonce())
	}
	if readBack.GetCodeChallenge() == nil || readBack.GetCodeChallenge().Challenge != "abc" {
		t.Errorf("code challenge was lost: %+v", readBack.GetCodeChallenge())
	}
}

// The code must be SINGLE-USE. If it can be used a second time, anyone who
// grabs a code from the address bar or the logs gets a token.
func TestRequestStore_CodeSingleUse(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{
		ClientID:     "makesinger-mobil",
		ResponseType: oidc.ResponseTypeCode,
	}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SaveCode(ctx, req.GetID(), "kod123"); err != nil {
		t.Fatalf("SaveCode: %v", err)
	}
	if _, err := store.GetByCode(ctx, "kod123"); err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if _, err := store.GetByCode(ctx, "kod123"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("second read error = %v, want ErrRequestNotFound", err)
	}
}

func TestRequestStore_MissingRequest(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	if _, err := store.GetByID(context.Background(), "yok"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("error = %v, want ErrRequestNotFound", err)
	}
}

// The request must not be "done" before the login completes: otherwise the
// code would be issued without the user ever being verified.
func TestAuthRequest_InitiallyNotDone(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.Done() {
		t.Error("a new request looks completed")
	}
	if req.GetSubject() != "" {
		t.Errorf("subject = %q, should be empty", req.GetSubject())
	}
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	readBack, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !readBack.Done() || readBack.GetSubject() != "kullanici-1" {
		t.Errorf("completed request is wrong: done=%v subject=%q", readBack.Done(), readBack.GetSubject())
	}
}

// C1 extra finding: an already completed request cannot be rebound to
// ANOTHER user; Subject is NOT OVERWRITTEN.
func TestRequestStore_MarkDoneKeepsSubject(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("first MarkDone: %v", err)
	}
	// Try 15 times in a row with other identities: all must be rejected.
	for i := 0; i < 15; i++ {
		if err := store.MarkDone(ctx, req.GetID(), "saldirgan", []string{amrPassword}); !errors.Is(err, ErrRequestAlreadyCompleted) {
			t.Fatalf("attempt %d error = %v, want ErrRequestAlreadyCompleted", i+1, err)
		}
	}
	readBack, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if readBack.GetSubject() != "kullanici-1" {
		t.Errorf("subject = %q, OVERWRITTEN (want kullanici-1)", readBack.GetSubject())
	}
}

// A second call for the SAME user must be a NO-OP: an honest double submit
// (sending the form twice, the back button) must not show an error and
// AuthTime must keep the real login moment.
func TestRequestStore_MarkDoneSameUserNoOp(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("first MarkDone: %v", err)
	}
	first, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("second MarkDone (same user) returned an error: %v", err)
	}
	second, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !second.GetAuthTime().Equal(first.GetAuthTime()) {
		t.Error("AuthTime was rewritten; it should have been a no-op")
	}
}

// M4: the third parameter given to Create is the "sub" of id_token_hint, it
// is NOT a request ID. The request ID must ALWAYS be a fresh UUID; otherwise
// two concurrent flows overwrite the same Redis key.
func TestRequestStore_IDIsNotHintSubject(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	const hint = "kullanici-42"
	firstRecord, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, hint)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if firstRecord.GetID() == hint {
		t.Fatal("request ID was made equal to the id_token_hint sub")
	}
	if firstRecord.HintSubject != hint {
		t.Errorf("HintSubject = %q, want %q", firstRecord.HintSubject, hint)
	}
	// The hint must not affect Done(): the user has not logged in yet.
	if firstRecord.Done() || firstRecord.GetSubject() != "" {
		t.Error("a request with a hint looks completed")
	}
	// A concurrent second flow must NOT overwrite the same key.
	second, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, hint)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if second.GetID() == firstRecord.GetID() {
		t.Fatal("two requests with the same hint got the same ID")
	}
}

// K6: after a password login GetAMR() must return ["pwd"].
func TestAMR_PasswordLoginIsPwd(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	readBack, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got := readBack.GetAMR(); len(got) != 1 || got[0] != "pwd" {
		t.Errorf("GetAMR() = %v, want [pwd]", got)
	}
}

// K6: the token of a user who logged in with Google must NOT say "pwd".
func TestAMR_FederationIsNotPwd(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrFederation}); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	readBack, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	got := readBack.GetAMR()
	if len(got) != 1 || got[0] != amrFederation {
		t.Errorf("GetAMR() = %v, want [%s]", got, amrFederation)
	}
	for _, v := range got {
		if v == "pwd" {
			t.Errorf("GetAMR() = %v, must NOT contain 'pwd'", got)
		}
	}
}

// The default must NOT silently be "pwd": a caller that forgot to give amr
// must not produce a false statement, it must produce an empty one.
func TestAMR_DefaultIsNotPwd(t *testing.T) {
	req := &AuthRequest{}
	if len(req.GetAMR()) != 0 {
		t.Errorf("GetAMR() = %v, want empty", req.GetAMR())
	}
}

// amr must NOT BE OVERWRITTEN on the second (no-op) call: it records the
// right one, the first real login method.
func TestAMR_NotOverwrittenOnNoOp(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	req, err := store.Create(ctx, &oidc.AuthRequest{ClientID: "makesinger-mobil"}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrPassword}); err != nil {
		t.Fatalf("first MarkDone: %v", err)
	}
	// Even if the second call for the same user comes with a DIFFERENT amr it
	// must be a NO-OP and must NOT overwrite the first amr.
	if err := store.MarkDone(ctx, req.GetID(), "kullanici-1", []string{amrFederation}); err != nil {
		t.Fatalf("second MarkDone (no-op): %v", err)
	}
	readBack, err := store.GetByID(ctx, req.GetID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got := readBack.GetAMR(); len(got) != 1 || got[0] != amrPassword {
		t.Errorf("GetAMR() = %v, want [%s] (must not be overwritten)", got, amrPassword)
	}
}

// amr must also land correctly in the refresh_tokens.amr column in Postgres:
// after a refresh the token must still carry the right amr. The fake
// (in-memory) TokenStore is used: what is measured is the logic of the
// Storage carrying amr through AuthRequest -> RefreshRecord -> refresh
// rotation, not the Postgres driver (that is measured by a separate test in
// token_store_pg_test.go).
func TestAMR_PreservedOnRefresh(t *testing.T) {
	d := newTestStorage(t)
	ctx := context.Background()
	req := &AuthRequest{
		ID:       "istek-amr-1",
		ClientID: "makesinger-mobil",
		Subject:  "kullanici-1",
		Scopes:   []string{"openid", "offline_access"},
		AuthTime: time.Now().UTC(),
		AMR:      []string{amrFederation},
	}
	_, refreshToken, _, err := d.CreateAccessAndRefreshTokens(ctx, req, "")
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}
	record, err := d.tokens.GetRefresh(ctx, refreshToken)
	if err != nil {
		t.Fatalf("GetRefresh: %v", err)
	}
	if len(record.AMR) != 1 || record.AMR[0] != amrFederation {
		t.Fatalf("amr at the first record = %v, want [%s]", record.AMR, amrFederation)
	}
	// Imitate the library's real call order: in the refresh flow
	// TokenRequestByRefreshToken produces the RefreshTokenRequest that carries
	// amr, and CreateAccessAndRefreshTokens hands it to rotation.
	refreshReq, err := d.TokenRequestByRefreshToken(ctx, refreshToken)
	if err != nil {
		t.Fatalf("TokenRequestByRefreshToken: %v", err)
	}
	_, rotatedRefreshToken, _, err := d.CreateAccessAndRefreshTokens(ctx, refreshReq, refreshToken)
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens (rotation): %v", err)
	}
	newRecord, err := d.tokens.GetRefresh(ctx, rotatedRefreshToken)
	if err != nil {
		t.Fatalf("GetRefresh (new): %v", err)
	}
	if len(newRecord.AMR) != 1 || newRecord.AMR[0] != amrFederation {
		t.Errorf("amr after rotation = %v, want [%s]", newRecord.AMR, amrFederation)
	}
}

// The cookie binding is kept in the same store as the auth request and must
// be readable; a cookie value that does not match must return
// ErrSessionNotFound.
func TestRequestStore_WriteAndReadSession(t *testing.T) {
	store := NewRequestStore(testRedis(t))
	ctx := context.Background()
	// Set: one cookie can be bound to more than one request (parallel tabs, see
	// maxSessionBindings). We also verify the JSON round trip through Redis:
	// if the fields are not exported, encoding/json silently skips them and the
	// binding is lost.
	var binding SessionBinding
	binding.Add("istek-1", "csrf-1")
	binding.Add("istek-2", "csrf-2")
	if err := store.WriteSession(ctx, "oturum-1", binding, requestTTL); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	readBack, err := store.ReadSession(ctx, "oturum-1")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if !reflect.DeepEqual(readBack, binding) {
		t.Errorf("binding = %+v, want %+v", readBack, binding)
	}
	for _, d := range []struct{ id, csrf string }{{"istek-1", "csrf-1"}, {"istek-2", "csrf-2"}} {
		csrf, isBound := readBack.CSRFFor(d.id)
		if !isBound || csrf != d.csrf {
			t.Errorf("CSRFFor(%q) = (%q, %v), want (%q, true)", d.id, csrf, isBound, d.csrf)
		}
	}
	if _, isBound := readBack.CSRFFor("istek-3"); isBound {
		t.Error("CSRFFor returned true for an unbound id")
	}
	if _, err := store.ReadSession(ctx, "yok"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("error = %v, want ErrSessionNotFound", err)
	}
}
