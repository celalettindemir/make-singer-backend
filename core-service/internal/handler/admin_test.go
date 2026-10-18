package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/makeasinger/api/internal/credit"
	"github.com/makeasinger/api/internal/middleware"
)

// Fixtures: the examples of OpenAPI (native/api/examples).
const (
	exListCampaigns = `{"summary": {"active": {"id": 101, "name": "Sonbahar lansmanı", "quota": 500, "perUser": 50, "validDays": 75, "endsAt": "2026-12-31", "status": "active", "startedAt": "2026-10-12T09:00:00Z", "stoppedAt": null, "createdBy": "celal258@gmail.com", "joined": 312}, "distributedThisMonth": {"credits": 15600, "users": 312}, "remainingQuota": {"people": 188, "credits": 9400}}, "items": [{"id": 101, "name": "Sonbahar lansmanı", "quota": 500, "perUser": 50, "validDays": 75, "endsAt": "2026-12-31", "status": "active", "startedAt": "2026-10-12T09:00:00Z", "stoppedAt": null, "createdBy": "celal258@gmail.com", "joined": 312}, {"id": 100, "name": "Beta duyurusu", "quota": 200, "perUser": 24, "validDays": 60, "endsAt": null, "status": "stopped", "startedAt": "2026-09-20T09:00:00Z", "stoppedAt": "2026-10-12T09:00:00Z", "createdBy": "celal258@gmail.com", "joined": 143}]}`
	exCreateReq     = `{"name": "Kış kampanyası", "quota": 300, "perUser": 40, "validDays": 60, "endsAt": "2027-01-31"}`
	exCreated       = `{"campaign": {"id": 102, "name": "Kış kampanyası", "quota": 300, "perUser": 40, "validDays": 60, "endsAt": "2027-01-31", "status": "active", "startedAt": "2026-10-18T12:00:00Z", "stoppedAt": null, "createdBy": "celal258@gmail.com", "joined": 0}, "replaced": {"id": 101, "name": "Sonbahar lansmanı", "quota": 500, "perUser": 50, "validDays": 75, "endsAt": "2026-12-31", "status": "stopped", "startedAt": "2026-10-12T09:00:00Z", "stoppedAt": "2026-10-18T12:00:00Z", "createdBy": "celal258@gmail.com", "joined": 312}}`
	exCampaign      = `{"campaign": {"id": 101, "name": "Sonbahar lansmanı", "quota": 500, "perUser": 50, "validDays": 75, "endsAt": "2026-12-31", "status": "active", "startedAt": "2026-10-12T09:00:00Z", "stoppedAt": null, "createdBy": "celal258@gmail.com", "joined": 312}, "distributed": 15600, "used": 4836, "usedPercent": 31, "recentJoins": [{"userId": "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "shortId": "8F3A21", "amount": 50, "joinedAt": "2026-10-18T10:02:00Z"}, {"userId": "41d7b9e2-3a50-4c86-9f1d-7e2a6b8c0d35", "shortId": "41D7B9", "amount": 50, "joinedAt": "2026-10-18T08:47:00Z"}]}`
	exStop409       = `{"error": {"code": "CAMPAIGN_NOT_ACTIVE", "message": "The campaign is not active."}}`
	exUsers         = `{"items": [{"userId": "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "shortId": "8F3A21", "email": "ayse@example.org", "balance": 84, "plan": {"tier": "plus", "status": "active"}, "campaignName": "Sonbahar lansmanı", "createdAt": "2026-10-18T10:02:00Z"}, {"userId": "41d7b9e2-3a50-4c86-9f1d-7e2a6b8c0d35", "shortId": "41D7B9", "email": "mert@example.org", "balance": 50, "plan": null, "campaignName": "Sonbahar lansmanı", "createdAt": "2026-10-18T08:47:00Z"}], "nextCursor": null}`
	exUser          = `{"user": {"userId": "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "shortId": "8F3A21", "email": "ayse@example.org", "balance": 84, "plan": {"tier": "plus", "status": "active"}, "campaignName": "Sonbahar lansmanı", "createdAt": "2026-10-18T10:02:00Z"}, "debt": 0, "lots": [{"source": "plan", "amount": 60, "expiresAt": "2026-11-01T00:00:00Z"}, {"source": "campaign", "amount": 24, "expiresAt": "2026-12-31T23:59:59Z"}], "plan": {"tier": "plus", "period": "monthly", "status": "active", "periodEnd": "2026-11-01T00:00:00Z", "autoRenew": true, "pendingTier": null}, "movements": [{"id": "mv_01JA0N7P3K", "userId": "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "shortId": "8F3A21", "email": "ayse@example.org", "kind": "admin_adjust", "operation": null, "amount": 25, "createdAt": "2026-10-18T12:10:00Z", "by": "celal258@gmail.com", "note": "Destek talebi 4521: başarısız işlem telafisi", "campaignId": null, "projectId": null, "tier": null}, {"id": "mv_01JA0M3Q9T", "userId": "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "shortId": "8F3A21", "email": "ayse@example.org", "kind": "render", "operation": "render_mix", "amount": -12, "createdAt": "2026-10-18T10:40:00Z", "by": null, "note": null, "campaignId": null, "projectId": "3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71", "tier": null}], "nextCursor": "eyJ0IjoiMjAyNi0xMC0xOFQxMDowMjowMFoiLCJpZCI6Im12XzAxSkEwTTBBMUIifQ"}`
	exAdjustReq     = `{"amount": 25, "note": "Destek talebi 4521: başarısız işlem telafisi", "validDays": 30, "requestId": "b7e0a5d1-4c92-4f38-8e6a-1d3f5a7c9b02"}`
	exAdjusted      = `{"balance": 109, "movement": {"id": "mv_01JA0N7P3K", "userId": "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "shortId": "8F3A21", "email": "ayse@example.org", "kind": "admin_adjust", "operation": null, "amount": 25, "createdAt": "2026-10-18T12:10:00Z", "by": "celal258@gmail.com", "note": "Destek talebi 4521: başarısız işlem telafisi", "campaignId": null, "projectId": null, "tier": null}}`
	exAdjust409     = `{"error": {"code": "BALANCE_TOO_LOW", "message": "The balance is lower than the amount to take away.", "details": {"balance": 6}}}`
	exMovements     = `{"items": [{"id": "mv_01JA0M0A1B", "userId": "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "shortId": "8F3A21", "email": "ayse@example.org", "kind": "campaign_gift", "operation": null, "amount": 50, "createdAt": "2026-10-18T10:02:00Z", "by": null, "note": null, "campaignId": 101, "projectId": null, "tier": null}], "nextCursor": "eyJ0IjoiMjAyNi0xMC0xOFQxMDowMjowMFoiLCJpZCI6Im12XzAxSkEwTTBBMUIifQ"}`
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func dayOf(s string) *time.Time {
	t, err := credit.ParseDay(s)
	if err != nil {
		panic(err)
	}
	return &t
}

func ptrOf[T any](v T) *T { return &v }

func autumn(status credit.CampaignStatus, stopped *time.Time) credit.Campaign {
	return credit.Campaign{ID: 101, Name: "Sonbahar lansmanı", Quota: 500, PerUser: 50, ValidDays: 75, Joined: 312,
		EndsAt: dayOf("2026-12-31"), Status: status, StartedAt: at("2026-10-12T09:00:00Z"), StoppedAt: stopped, CreatedBy: "celal258@gmail.com"}
}

type fakeCampaigns struct {
	created    credit.NewCampaign
	createdBy  string
	createErr  error
	stopErr    error
	getErr     error
	stoppedIDs []int64
}

func (f *fakeCampaigns) Create(_ context.Context, in credit.NewCampaign, by string) (credit.Campaign, *credit.Campaign, error) {
	f.created, f.createdBy = in, by
	if f.createErr != nil {
		return credit.Campaign{}, nil, f.createErr
	}
	replaced := autumn(credit.CampaignStopped, ptrOf(at("2026-10-18T12:00:00Z")))
	return credit.Campaign{ID: 102, Name: in.Name, Quota: in.Quota, PerUser: in.PerUser, ValidDays: in.ValidDays, EndsAt: in.EndsAt,
		Status: credit.CampaignActive, StartedAt: at("2026-10-18T12:00:00Z"), CreatedBy: by}, &replaced, nil
}

func (f *fakeCampaigns) Stop(_ context.Context, id int64) (credit.Campaign, error) {
	f.stoppedIDs = append(f.stoppedIDs, id)
	if f.stopErr != nil {
		return credit.Campaign{}, f.stopErr
	}
	return autumn(credit.CampaignStopped, ptrOf(at("2026-10-18T12:00:00Z"))), nil
}

func (f *fakeCampaigns) List(context.Context) (credit.CampaignOverview, error) {
	beta := credit.Campaign{ID: 100, Name: "Beta duyurusu", Quota: 200, PerUser: 24, ValidDays: 60, Joined: 143,
		Status: credit.CampaignStopped, StartedAt: at("2026-09-20T09:00:00Z"), StoppedAt: ptrOf(at("2026-10-12T09:00:00Z")), CreatedBy: "celal258@gmail.com"}
	a := autumn(credit.CampaignActive, nil)
	return credit.CampaignOverview{Active: &a, Campaigns: []credit.Campaign{a, beta}, MonthCredits: 15600, MonthPeople: 312, QuotaLeft: 188}, nil
}

func (f *fakeCampaigns) Get(_ context.Context, id int64) (credit.CampaignDetail, error) {
	if f.getErr != nil {
		return credit.CampaignDetail{}, f.getErr
	}
	return credit.CampaignDetail{Campaign: autumn(credit.CampaignActive, nil), Distributed: 15600, Used: 4836, UsedPercent: 31,
		Recent: []credit.Participant{
			{UserID: "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", JoinedAt: at("2026-10-18T10:02:00Z")},
			{UserID: "41d7b9e2-3a50-4c86-9f1d-7e2a6b8c0d35", JoinedAt: at("2026-10-18T08:47:00Z")},
		}}, nil
}

func movementOf(id, kind string, amount int, created string) credit.AdminMovement {
	return credit.AdminMovement{ID: id, UserID: "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", ShortID: "8F3A21",
		Email: ptrOf("ayse@example.org"), Kind: kind, Amount: amount, CreatedAt: at(created)}
}

type fakeAdmin struct {
	usersQ, usersCursor string
	usersErr            error
	userID              string
	userErr             error
	adjustUser, adjBy   string
	adjustIn            credit.Adjustment
	adjustErr           error
	movQuery            credit.MovementQuery
	movErr              error
}

func (f *fakeAdmin) Users(_ context.Context, q, cursor string) (credit.AdminUserPage, error) {
	f.usersQ, f.usersCursor = q, cursor
	if f.usersErr != nil {
		return credit.AdminUserPage{}, f.usersErr
	}
	campaign := "Sonbahar lansmanı"
	return credit.AdminUserPage{Items: []credit.AdminUserItem{
		{UserID: "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", ShortID: "8F3A21", Email: "ayse@example.org", Balance: 84,
			Plan: &credit.AdminPlanBrief{Tier: credit.TierPlus, Status: "active"}, CampaignName: &campaign, CreatedAt: at("2026-10-18T10:02:00Z")},
		{UserID: "41d7b9e2-3a50-4c86-9f1d-7e2a6b8c0d35", ShortID: "41D7B9", Email: "mert@example.org", Balance: 50,
			CampaignName: &campaign, CreatedAt: at("2026-10-18T08:47:00Z")},
	}}, nil
}

func (f *fakeAdmin) User(_ context.Context, id string) (credit.AdminUserDetail, error) {
	f.userID = id
	if f.userErr != nil {
		return credit.AdminUserDetail{}, f.userErr
	}
	campaign := "Sonbahar lansmanı"
	planEnd := at("2026-11-01T00:00:00Z")
	giftEnd := at("2026-12-31T23:59:59Z")
	render := movementOf("mv_01JA0M3Q9T", "render", -12, "2026-10-18T10:40:00Z")
	render.Operation, render.ProjectID = ptrOf("render_mix"), ptrOf("3f2c8a10-5b7e-4c1d-9a2f-6e8b0c4d2a71")
	adj := movementOf("mv_01JA0N7P3K", "admin_adjust", 25, "2026-10-18T12:10:00Z")
	adj.By, adj.Note = ptrOf("celal258@gmail.com"), ptrOf("Destek talebi 4521: başarısız işlem telafisi")
	return credit.AdminUserDetail{
		User: credit.AdminUserItem{UserID: "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", ShortID: "8F3A21", Email: "ayse@example.org", Balance: 84,
			Plan: &credit.AdminPlanBrief{Tier: credit.TierPlus, Status: "active"}, CampaignName: &campaign, CreatedAt: at("2026-10-18T10:02:00Z")},
		Lots:       []credit.LotView{{Source: "plan", Amount: 60, ExpiresAt: &planEnd}, {Source: "campaign", Amount: 24, ExpiresAt: &giftEnd}},
		Plan:       &credit.Plan{Tier: credit.TierPlus, Period: "monthly", Status: "active", PeriodEnd: planEnd, AutoRenew: true},
		Movements:  []credit.AdminMovement{adj, render},
		NextCursor: ptrOf("eyJ0IjoiMjAyNi0xMC0xOFQxMDowMjowMFoiLCJpZCI6Im12XzAxSkEwTTBBMUIifQ"),
	}, nil
}

func (f *fakeAdmin) Adjust(_ context.Context, userID string, in credit.Adjustment, by string) (credit.AdjustResult, error) {
	f.adjustUser, f.adjustIn, f.adjBy = userID, in, by
	if f.adjustErr != nil {
		return credit.AdjustResult{}, f.adjustErr
	}
	m := movementOf("mv_01JA0N7P3K", "admin_adjust", 25, "2026-10-18T12:10:00Z")
	m.By, m.Note = ptrOf("celal258@gmail.com"), ptrOf("Destek talebi 4521: başarısız işlem telafisi")
	return credit.AdjustResult{Balance: 109, Movement: m}, nil
}

func (f *fakeAdmin) Movements(_ context.Context, q credit.MovementQuery) (credit.AdminMovementPage, error) {
	f.movQuery = q
	if f.movErr != nil {
		return credit.AdminMovementPage{}, f.movErr
	}
	gift := movementOf("mv_01JA0M0A1B", "campaign_gift", 50, "2026-10-18T10:02:00Z")
	gift.CampaignID = ptrOf(int64(101))
	return credit.AdminMovementPage{Items: []credit.AdminMovement{gift},
		NextCursor: ptrOf("eyJ0IjoiMjAyNi0xMC0xOFQxMDowMjowMFoiLCJpZCI6Im12XzAxSkEwTTBBMUIifQ")}, nil
}

func newAdminApp(camp *fakeCampaigns, adm *fakeAdmin) *fiber.App {
	app := fiber.New()
	g := app.Group("/api/admin", func(c *fiber.Ctx) error {
		c.Locals(middleware.LocalAdminEmail, "celal258@gmail.com")
		return c.Next()
	})
	NewAdminHandler(camp, adm).Routes(g)
	return app
}

func adminDo(t *testing.T, app *fiber.App, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Accept-Language", "en") // the examples are the English texts
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("response %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON differs\n got %s\nwant %s", got, want)
	}
}

func adminErrorCode(t *testing.T, body string) string {
	t.Helper()
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return e.Error.Code
}

func TestAdminHandler_ListCampaignsMatchesExample(t *testing.T) {
	app := newAdminApp(&fakeCampaigns{}, &fakeAdmin{})
	code, body := adminDo(t, app, http.MethodGet, "/api/admin/campaigns", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, (exListCampaigns))
}

func TestAdminHandler_CreateCampaignMatchesExample(t *testing.T) {
	camp := &fakeCampaigns{}
	app := newAdminApp(camp, &fakeAdmin{})
	code, body := adminDo(t, app, http.MethodPost, "/api/admin/campaigns", exCreateReq)
	if code != 201 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, (exCreated))
	if camp.createdBy != "celal258@gmail.com" || camp.created.Name != "Kış kampanyası" || camp.created.Quota != 300 ||
		camp.created.PerUser != 40 || camp.created.ValidDays != 60 || camp.created.EndsAt == nil || camp.created.EndsAt.Format("2006-01-02") != "2027-01-31" {
		t.Fatalf("service got %+v by %q", camp.created, camp.createdBy)
	}
	// endsAt null is a campaign without a date; the first campaign replaces nothing.
	code, body = adminDo(t, app, http.MethodPost, "/api/admin/campaigns", `{"name":"x","quota":1,"perUser":1,"validDays":1,"endsAt":null}`)
	if code != 201 || camp.created.EndsAt != nil || !strings.Contains(body, `"endsAt":null`) {
		t.Fatalf("null endsAt: %d %s %+v", code, body, camp.created)
	}
}

func TestAdminHandler_CreateCampaignRejectsBadBodies(t *testing.T) {
	app := newAdminApp(&fakeCampaigns{}, &fakeAdmin{})
	for name, body := range map[string]string{
		"empty":         ``,
		"not json":      `{`,
		"unknown key":   `{"name":"x","quota":1,"perUser":1,"validDays":1,"endsAt":null,"extra":1}`,
		"missing name":  `{"quota":1,"perUser":1,"validDays":1,"endsAt":null}`,
		"missing ends":  `{"name":"x","quota":1,"perUser":1,"validDays":1}`,
		"string quota":  `{"name":"x","quota":"1","perUser":1,"validDays":1,"endsAt":null}`,
		"bad date":      `{"name":"x","quota":1,"perUser":1,"validDays":1,"endsAt":"2027-13-01"}`,
		"date format":   `{"name":"x","quota":1,"perUser":1,"validDays":1,"endsAt":"31.01.2027"}`,
		"date and time": `{"name":"x","quota":1,"perUser":1,"validDays":1,"endsAt":"2027-01-31T00:00:00Z"}`,
		"trailing":      `{"name":"x","quota":1,"perUser":1,"validDays":1,"endsAt":null} {}`,
	} {
		code, resp := adminDo(t, app, http.MethodPost, "/api/admin/campaigns", body)
		if code != 400 || adminErrorCode(t, resp) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s, want 400 VALIDATION_ERROR", name, code, resp)
		}
	}
}

func TestAdminHandler_CreateCampaignServiceValidationIs400WithField(t *testing.T) {
	camp := &fakeCampaigns{createErr: credit.ErrCampaignInvalid}
	app := newAdminApp(camp, &fakeAdmin{})
	code, body := adminDo(t, app, http.MethodPost, "/api/admin/campaigns", exCreateReq)
	if code != 400 || adminErrorCode(t, body) != "VALIDATION_ERROR" {
		t.Fatalf("%d %s", code, body)
	}
}

func TestAdminHandler_GetCampaignMatchesExampleAndIs404(t *testing.T) {
	camp := &fakeCampaigns{}
	app := newAdminApp(camp, &fakeAdmin{})
	code, body := adminDo(t, app, http.MethodGet, "/api/admin/campaigns/"+"101", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, (exCampaign))
	for _, id := range []string{"nope", "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "0", "-1", "1.5"} {
		if code, body := adminDo(t, app, http.MethodGet, "/api/admin/campaigns/"+id, ""); code != 400 || adminErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", id, code, body)
		}
	}
	camp.getErr = credit.ErrCampaignNotFound
	if code, body := adminDo(t, app, http.MethodGet, "/api/admin/campaigns/"+"9", ""); code != 404 || adminErrorCode(t, body) != "NOT_FOUND" {
		t.Errorf("unknown: %d %s", code, body)
	}
}

func TestAdminHandler_StopCampaign(t *testing.T) {
	camp := &fakeCampaigns{}
	app := newAdminApp(camp, &fakeAdmin{})
	path := "/api/admin/campaigns/" + "101" + "/stop"
	code, body := adminDo(t, app, http.MethodPost, path, "")
	if code != 200 || len(camp.stoppedIDs) != 1 || camp.stoppedIDs[0] != 101 {
		t.Fatalf("stop: %d %s %v", code, body, camp.stoppedIDs)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte((exCreated)), &want); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil || !reflect.DeepEqual(got, want["replaced"]) {
		t.Fatalf("stopped campaign = %s, want the replaced example", body)
	}
}

// The second stop is 409 CAMPAIGN_NOT_ACTIVE, shaped like stopCampaign.409.json.
func TestAdminHandler_StopTwiceIs409(t *testing.T) {
	camp := &fakeCampaigns{stopErr: credit.ErrCampaignNotActive}
	app := newAdminApp(camp, &fakeAdmin{})
	code, body := adminDo(t, app, http.MethodPost, "/api/admin/campaigns/"+"101"+"/stop", "")
	if code != 409 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exStop409)
	camp.stopErr = credit.ErrCampaignNotFound
	if code, body := adminDo(t, app, http.MethodPost, "/api/admin/campaigns/"+"101"+"/stop", ""); code != 404 {
		t.Errorf("unknown: %d %s", code, body)
	}
	if code, _ := adminDo(t, app, http.MethodPost, "/api/admin/campaigns/not-a-number/stop", ""); code != 400 {
		t.Errorf("malformed id: %d", code)
	}
}

func TestAdminHandler_UsersMatchExample(t *testing.T) {
	adm := &fakeAdmin{}
	app := newAdminApp(&fakeCampaigns{}, adm)
	code, body := adminDo(t, app, http.MethodGet, "/api/admin/users?q=%238F3A21&cursor=abc", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exUsers)
	if adm.usersQ != "#8F3A21" || adm.usersCursor != "abc" {
		t.Fatalf("service got q=%q cursor=%q", adm.usersQ, adm.usersCursor)
	}
	adm.usersErr = credit.ErrQueryTooShort
	if code, body := adminDo(t, app, http.MethodGet, "/api/admin/users?q=ab", ""); code != 400 || adminErrorCode(t, body) != "VALIDATION_ERROR" {
		t.Errorf("short q: %d %s", code, body)
	}
	adm.usersErr = credit.ErrBadCursor
	if code, body := adminDo(t, app, http.MethodGet, "/api/admin/users?cursor=zz", ""); code != 400 || adminErrorCode(t, body) != "VALIDATION_ERROR" {
		t.Errorf("bad cursor: %d %s", code, body)
	}
}

func TestAdminHandler_UserDetailMatchesExample(t *testing.T) {
	adm := &fakeAdmin{}
	app := newAdminApp(&fakeCampaigns{}, adm)
	// The example lists 3 movements; the fake returns the two that matter for the shape.
	code, body := adminDo(t, app, http.MethodGet, "/api/admin/users/8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "")
	if code != 200 || adm.userID != "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67" {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exUser)
	adm.userErr = credit.ErrUserNotFound
	if code, body := adminDo(t, app, http.MethodGet, "/api/admin/users/"+"41d7b9e2-3a50-4c86-9f1d-7e2a6b8c0d35", ""); code != 404 || adminErrorCode(t, body) != "NOT_FOUND" {
		t.Errorf("unknown user: %d %s", code, body)
	}
}

func TestAdminHandler_AdjustMatchesExample(t *testing.T) {
	adm := &fakeAdmin{}
	app := newAdminApp(&fakeCampaigns{}, adm)
	code, body := adminDo(t, app, http.MethodPost, "/api/admin/users/8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67/adjust", exAdjustReq)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exAdjusted)
	in := adm.adjustIn
	if adm.adjustUser != "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67" || adm.adjBy != "celal258@gmail.com" || in.Amount != 25 ||
		in.ValidDays == nil || *in.ValidDays != 30 || in.RequestID != "b7e0a5d1-4c92-4f38-8e6a-1d3f5a7c9b02" ||
		in.Note != "Destek talebi 4521: başarısız işlem telafisi" {
		t.Fatalf("service got %+v for %q by %q", in, adm.adjustUser, adm.adjBy)
	}
	// validDays null.
	adminDo(t, app, http.MethodPost, "/api/admin/users/8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67/adjust",
		`{"amount":-10,"note":"geri al","validDays":null,"requestId":"0c9d8e7f-6a5b-4c3d-8e2f-1a0b9c8d7e6f"}`)
	if adm.adjustIn.ValidDays != nil || adm.adjustIn.Amount != -10 {
		t.Fatalf("service got %+v", adm.adjustIn)
	}
}

func TestAdminHandler_AdjustErrors(t *testing.T) {
	adm := &fakeAdmin{}
	app := newAdminApp(&fakeCampaigns{}, adm)
	path := "/api/admin/users/8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67/adjust"
	adm.adjustErr = &credit.BalanceTooLowError{Balance: 6}
	code, body := adminDo(t, app, http.MethodPost, path, exAdjustReq)
	if code != 409 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exAdjust409)
	adm.adjustErr = credit.ErrUserNotFound
	if code, body := adminDo(t, app, http.MethodPost, path, exAdjustReq); code != 404 || adminErrorCode(t, body) != "NOT_FOUND" {
		t.Errorf("unknown user: %d %s", code, body)
	}
	adm.adjustErr = credit.ErrAdjustInvalid
	if code, body := adminDo(t, app, http.MethodPost, path, exAdjustReq); code != 400 || adminErrorCode(t, body) != "VALIDATION_ERROR" {
		t.Errorf("invalid: %d %s", code, body)
	}
	adm.adjustErr = nil
	for name, b := range map[string]string{
		"missing amount":    `{"note":"nnn","validDays":null,"requestId":"0c9d8e7f-6a5b-4c3d-8e2f-1a0b9c8d7e6f"}`,
		"missing validDays": `{"amount":5,"note":"nnn","requestId":"0c9d8e7f-6a5b-4c3d-8e2f-1a0b9c8d7e6f"}`,
		"missing requestId": `{"amount":5,"note":"nnn","validDays":null}`,
		"missing note":      `{"amount":5,"validDays":null,"requestId":"0c9d8e7f-6a5b-4c3d-8e2f-1a0b9c8d7e6f"}`,
		"float amount":      `{"amount":5.5,"note":"nnn","validDays":null,"requestId":"0c9d8e7f-6a5b-4c3d-8e2f-1a0b9c8d7e6f"}`,
		"extra key":         `{"amount":5,"note":"nnn","validDays":null,"requestId":"0c9d8e7f-6a5b-4c3d-8e2f-1a0b9c8d7e6f","x":1}`,
	} {
		if code, resp := adminDo(t, app, http.MethodPost, path, b); code != 400 || adminErrorCode(t, resp) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, code, resp)
		}
	}
}

func TestAdminHandler_MovementsMatchExampleAndParseFilters(t *testing.T) {
	adm := &fakeAdmin{}
	app := newAdminApp(&fakeCampaigns{}, adm)
	path := "/api/admin/movements?kind=campaign_gift&campaignId=" + "7" +
		"&userId=8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67&from=2026-10-01&to=2026-10-18&cursor=c1"
	code, body := adminDo(t, app, http.MethodGet, path, "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	sameJSON(t, body, exMovements)
	q := adm.movQuery
	if q.Kind != "campaign_gift" || q.CampaignID == nil || *q.CampaignID != 7 || q.UserID != "8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67" ||
		q.Cursor != "c1" || q.From == nil || q.From.Format("2006-01-02") != "2026-10-01" || q.To == nil || q.To.Format("2006-01-02") != "2026-10-18" {
		t.Fatalf("service got %+v", q)
	}
	for _, bad := range []string{"from=2026-10-1", "to=yesterday", "from=2026-10-01T00:00:00Z", "campaignId=x", "campaignId=8f3a21c0-6b7d-4e19-a2f5-0d9c1b3e4a67", "userId=x"} {
		if code, resp := adminDo(t, app, http.MethodGet, "/api/admin/movements?"+bad, ""); code != 400 || adminErrorCode(t, resp) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", bad, code, resp)
		}
	}
	adm.movErr = credit.ErrBadFilter
	if code, resp := adminDo(t, app, http.MethodGet, "/api/admin/movements?kind=bogus", ""); code != 400 || adminErrorCode(t, resp) != "VALIDATION_ERROR" {
		t.Errorf("bad kind: %d %s", code, resp)
	}
	adm.movErr = credit.ErrBadCursor
	if code, resp := adminDo(t, app, http.MethodGet, "/api/admin/movements?cursor=zz", ""); code != 400 || adminErrorCode(t, resp) != "VALIDATION_ERROR" {
		t.Errorf("bad cursor: %d %s", code, resp)
	}
}

func TestAdminHandler_ServiceFailureIs500(t *testing.T) {
	app := newAdminApp(&fakeCampaigns{createErr: context.DeadlineExceeded}, &fakeAdmin{movErr: context.DeadlineExceeded})
	if code, body := adminDo(t, app, http.MethodPost, "/api/admin/campaigns", exCreateReq); code != 500 || adminErrorCode(t, body) != "SERVICE_ERROR" {
		t.Errorf("create: %d %s", code, body)
	}
	if code, body := adminDo(t, app, http.MethodGet, "/api/admin/movements", ""); code != 500 || adminErrorCode(t, body) != "SERVICE_ERROR" {
		t.Errorf("movements: %d %s", code, body)
	}
}

func TestAdminHandler_NonUUIDUserIDIs400(t *testing.T) {
	adm := &fakeAdmin{}
	app := newAdminApp(&fakeCampaigns{}, adm)
	for _, id := range []string{"nope", "123", "8f3a21c0"} {
		if code, body := adminDo(t, app, http.MethodGet, "/api/admin/users/"+id, ""); code != 400 || adminErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Errorf("get %q: %d %s", id, code, body)
		}
		if code, body := adminDo(t, app, http.MethodPost, "/api/admin/users/"+id+"/adjust", exAdjustReq); code != 400 || adminErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Errorf("adjust %q: %d %s", id, code, body)
		}
	}
	if adm.userID != "" {
		t.Errorf("service was called with %q", adm.userID)
	}
}
