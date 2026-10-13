package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makeasinger/api/internal/auth"
	"github.com/redis/go-redis/v9"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

const testPassword = "cokgizlisifre"

// buildClaims imitates the claims of a verified upstream id_token. This is
// exactly the input of resolveUser: signature/nonce verification is done
// BEFORE it (see Callback).
func buildClaims(sub, email string, verified bool) *auth.Claims {
	return &auth.Claims{
		UserID:        sub,
		Email:         email,
		EmailVerified: auth.FlexibleBool(verified),
		Name:          "Test Kullanici",
	}
}

// bindSession imitates what the /authorize leg does: it binds the given
// authRequestID to a browser and returns that browser's session cookie. The
// /baglama page requires the SAME session binding as Phase 1's login page.
func (o *fedEnv) bindSession(t *testing.T, authReqID string) *http.Cookie {
	t.Helper()
	session, err := randomToken()
	if err != nil {
		t.Fatalf("oturum degeri uretilemedi: %v", err)
	}
	csrf, err := randomToken()
	if err != nil {
		t.Fatalf("csrf uretilemedi: %v", err)
	}
	var binding SessionBinding
	binding.Add(authReqID, csrf)
	if err := o.sessions.WriteSession(context.Background(), session, binding, time.Minute); err != nil {
		t.Fatalf("oturum yazilamadi: %v", err)
	}
	return &http.Cookie{Name: cookieNameSession, Value: session}
}

func (o *fedEnv) linkGetRequest(pendingID string, cookie *http.Cookie) *httptest.ResponseRecorder {
	target := pathLink
	if pendingID != "" {
		target += "?" + url.Values{fieldPendingID: {pendingID}}.Encode()
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	o.mux.ServeHTTP(w, r)
	return w
}

func (o *fedEnv) linkPostRequest(value url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, pathLink, strings.NewReader(value.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	o.mux.ServeHTTP(w, r)
	return w
}

// setupPendingLink runs the REAL federation flow: with an account that has a
// PASSWORD and a conflicting email, it logs in with Google and the flow
// redirects to /baglama. Returns: the pending record's id, the CSRF token in
// the form, the browser's session cookie and the conflicting account.
func (o *fedEnv) setupPendingLink(t *testing.T) (string, string, *http.Cookie, *User) {
	t.Helper()
	o.google.configure(func(s *fakeProvider) { s.email = "cakisan@ornek.com" })
	existing, err := o.users.Create(context.Background(), "cakisan@ornek.com", "Mevcut", testPassword)
	if err != nil {
		t.Fatalf("mevcut hesap olusturulamadi: %v", err)
	}

	w := o.runFlow(t, providerGoogle, false)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302 (/baglama'ya yonlendirme), govde = %q", w.Code, w.Body.String())
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location cozumlenemedi: %v", err)
	}
	if location.Path != pathLink {
		t.Fatalf("Location = %q, beklenen %s", w.Header().Get("Location"), pathLink)
	}
	pendingID := location.Query().Get(fieldPendingID)
	if pendingID == "" {
		t.Fatal("yonlendirmede beklemeID yok")
	}
	if o.requests.subject(o.authReqID) != "" {
		t.Fatal("cakisma varken istek TAMAMLANMAMALI")
	}

	cookie := o.sessionCookie
	form := o.linkGetRequest(pendingID, cookie)
	if form.Code != http.StatusOK {
		t.Fatalf("baglama formu acilamadi: durum = %d, govde = %q", form.Code, form.Body.String())
	}
	matches := csrfPattern.FindStringSubmatch(form.Body.String())
	if matches == nil || matches[1] == "" {
		t.Fatalf("formda CSRF jetonu yok: %q", form.Body.String())
	}
	if !strings.Contains(form.Body.String(), "cakisan@ornek.com") {
		t.Error("form cakisan e-postayi gostermiyor")
	}
	return pendingID, matches[1], cookie, existing
}

// ---------------------------------------------------------------------
// Step 1: Resolution order tests
// ---------------------------------------------------------------------

//  1. A linked identity ALWAYS comes first: the user's email at Google may
//     have changed, and then a lookup by email would find ANOTHER account
//     (or none).
func TestResolveUser_LinkedIdentityBeforeEmail(t *testing.T) {
	o := newTestFederationPages(t)
	ctx := context.Background()

	a, err := o.users.Create(ctx, "a@ornek.com", "A", testPassword)
	if err != nil {
		t.Fatalf("A olusturulamadi: %v", err)
	}
	if err := o.federated.Link(ctx, providerGoogle, "sub-1", a.ID); err != nil {
		t.Fatalf("A baglanamadi: %v", err)
	}
	b, err := o.users.Create(ctx, "yeni@ornek.com", "B", testPassword)
	if err != nil {
		t.Fatalf("B olusturulamadi: %v", err)
	}

	// id_token: sub=sub-1 (linked to A) but the email belongs to B.
	user, conflict, err := o.pages.resolveUser(ctx, providerGoogle, buildClaims("sub-1", "yeni@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if conflict != nil {
		t.Fatalf("cakisma dondu: %+v (bagli kimlik e-postadan ONCE gelmeli)", conflict)
	}
	if user == nil || user.ID != a.ID {
		t.Fatalf("kullanici = %v, beklenen A (%s)", user, a.ID)
	}
	if user.ID == b.ID {
		t.Error("e-posta uzerinden B'ye dusuldu")
	}
}

//  2. The Apple private relay address is NEVER used for linking.
//     This is the only case the spec explicitly forbids.
func TestResolveUser_PrivateRelayDoesNotTriggerLink(t *testing.T) {
	t.Run("adres SIFRELI bir hesapta kayitli: baglama YOK", func(t *testing.T) {
		o := newTestFederationPages(t)
		ctx := context.Background()
		// The attacker may have created an account with a password using the
		// victim's relay address.
		if _, err := o.users.Create(ctx, "abc"+privateRelaySuffix, "Saldirgan", testPassword); err != nil {
			t.Fatalf("hesap olusturulamadi: %v", err)
		}

		user, conflict, err := o.pages.resolveUser(ctx, providerApple, buildClaims("yeni-sub", "abc"+privateRelaySuffix, true))
		if err != nil {
			t.Fatalf("kullaniciCoz: %v", err)
		}
		if user != nil {
			t.Fatalf("giris yapildi (%s): gizli aktarma adresi baglama icin kullanilamaz", user.ID)
		}
		if conflict == nil {
			t.Fatal("cakisma bekleniyordu")
		}
		if conflict.HasPassword {
			t.Error("SifreliHesap = true: gizli aktarma adresi BAGLAMA sayfasina goturulmemeli")
		}
		if conflict.PendingID != "" {
			t.Error("gizli aktarma adresi icin bekleme kaydi acilmamali")
		}
		if _, err := o.federated.UserByIdentity(ctx, providerApple, "yeni-sub"); err == nil {
			t.Error("kimlik baglandi: gizli aktarma adresinde baglama YAPILMAMALI")
		}
	})

	t.Run("adres kayitli degil: dogrudan yeni hesap", func(t *testing.T) {
		o := newTestFederationPages(t)
		ctx := context.Background()
		user, conflict, err := o.pages.resolveUser(ctx, providerApple, buildClaims("yeni-sub", "def"+privateRelaySuffix, true))
		if err != nil {
			t.Fatalf("kullaniciCoz: %v", err)
		}
		if conflict != nil {
			t.Fatalf("cakisma dondu: %+v", conflict)
		}
		if user == nil {
			t.Fatal("yeni hesap acilmadi")
		}
		if user.PasswordHash != "" {
			t.Error("federe hesabin sifresi olmamali")
		}
	})
}

// 4. Account with a password: the link page.
func TestResolveUser_EmailConflictWithPasswordLeadsToLink(t *testing.T) {
	o := newTestFederationPages(t)
	ctx := context.Background()
	existing, err := o.users.Create(ctx, "ortak@ornek.com", "Mevcut", testPassword)
	if err != nil {
		t.Fatalf("hesap olusturulamadi: %v", err)
	}

	user, conflict, err := o.pages.resolveUser(ctx, providerGoogle, buildClaims("yeni-sub", "ortak@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if user != nil {
		t.Fatalf("giris yapildi (%s): sifreli hesapta otomatik baglama YOK", user.ID)
	}
	if conflict == nil {
		t.Fatal("cakisma bekleniyordu")
	}
	if !conflict.HasPassword {
		t.Error("SifreliHesap = false, beklenen true")
	}
	if conflict.PendingID == "" {
		t.Error("BeklemeID bos")
	}
	if conflict.Email != "ortak@ornek.com" {
		t.Errorf("Eposta = %q", conflict.Email)
	}
	if _, err := o.federated.UserByIdentity(ctx, providerGoogle, "yeni-sub"); err == nil {
		t.Error("kimlik baglandi: sifre sorulmadan baglama YAPILMAMALI")
	}
	if _, err := o.users.ByID(ctx, existing.ID); err != nil {
		t.Errorf("mevcut hesap bozuldu: %v", err)
	}
}

// 5. K1: account without a password -> 409, NO link, NO new account.
func TestResolveUser_EmailConflictWithoutPasswordBlocked(t *testing.T) {
	o := newTestFederationPages(t)
	ctx := context.Background()
	// An account opened with Google that has NO PASSWORD.
	existing, err := o.federated.CreateFederatedUser(ctx, providerGoogle, "google-sub", "ortak@ornek.com", "Mevcut", true)
	if err != nil {
		t.Fatalf("federe hesap olusturulamadi: %v", err)
	}

	// It comes from Apple with the SAME email.
	user, conflict, err := o.pages.resolveUser(ctx, providerApple, buildClaims("apple-sub", "ortak@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if user != nil {
		t.Fatalf("giris yapildi (%s): K1 sifresiz hesapta baglama YOK", user.ID)
	}
	if conflict == nil {
		t.Fatal("cakisma bekleniyordu")
	}
	if conflict.HasPassword {
		t.Error("SifreliHesap = true: sifresiz hesapta sorulacak sifre yok")
	}
	if !reflect.DeepEqual(conflict.ExistingProviders, []string{providerGoogle}) {
		t.Errorf("MevcutSaglayicilar = %v, beklenen [google]", conflict.ExistingProviders)
	}
	if _, err := o.federated.UserByIdentity(ctx, providerApple, "apple-sub"); err == nil {
		t.Error("kimlik baglandi: K1 baglama YASAKLAR")
	}
	// A NEW RECORD MUST NOT BE CREATED: the same email still belongs to the first account.
	same, err := o.users.ByEmail(ctx, "ortak@ornek.com")
	if err != nil {
		t.Fatalf("mevcut hesap kayboldu: %v", err)
	}
	if same.ID != existing.ID {
		t.Errorf("e-posta %q hesabina gecti, beklenen %q (yeni hesap OLUSMAMALI)", same.ID, existing.ID)
	}
}

// The spec's concrete attack: the attacker opens an account with a password
// using the victim's address; the victim logs in with Google. It must NOT BE
// LINKED AUTOMATICALLY.
func TestResolveUser_NoSilentLinkToPrecreatedAccount(t *testing.T) {
	o := newTestFederationPages(t)
	ctx := context.Background()
	// users: kurban@ornek.com, password = the ATTACKER's password.
	attackers, err := o.users.Create(ctx, "kurban@ornek.com", "Kurban", "saldirganin-sifresi")
	if err != nil {
		t.Fatalf("hesap olusturulamadi: %v", err)
	}

	// Google: it must not link EVEN IF the email is verified -- this is the
	// real point of the test. Since WE do not do the email verification,
	// email_verified does not prove that this account belongs to the victim.
	user, conflict, err := o.pages.resolveUser(ctx, providerGoogle, buildClaims("kurban-google-sub", "kurban@ornek.com", true))
	if err != nil {
		t.Fatalf("kullaniciCoz: %v", err)
	}
	if user != nil {
		t.Fatalf("GIRIS YAPILDI (%s): sifresi saldirganda olan hesaba otomatik baglandi", user.ID)
	}
	if conflict == nil {
		t.Fatal("cakisma bekleniyordu")
	}
	if _, err := o.federated.UserByIdentity(ctx, providerGoogle, "kurban-google-sub"); err == nil {
		t.Errorf("kimlik %s hesabina SESSIZCE baglandi", attackers.ID)
	}
}

// ---------------------------------------------------------------------
// Step 2: Private relay + email uniqueness conflict
// ---------------------------------------------------------------------

// The private relay address goes straight to a new account in step 2, but
// because of UNIQUE (lower(email)) the INSERT returns ErrEmailInUse if the
// address already exists. Decision: 409 in this case too -- never a link.
func TestResolveUser_ExistingPrivateRelayReturns409(t *testing.T) {
	o := newTestFederationPages(t)
	ctx := context.Background()
	relay := "abc" + privateRelaySuffix
	// The address is already registered with ANOTHER sub (the user may have
	// deleted their account and signed up again; the Apple relay address stays
	// the same).
	if _, err := o.federated.CreateFederatedUser(ctx, providerApple, "eski-sub", relay, "Eski", true); err != nil {
		t.Fatalf("ilk hesap olusturulamadi: %v", err)
	}
	o.apple.configure(func(s *fakeProvider) {
		s.sub = "yeni-sub"
		s.email = relay
	})

	w := o.runFlow(t, providerApple, true)
	if w.Code != http.StatusConflict {
		t.Fatalf("durum = %d, beklenen 409, govde = %q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<form") {
		t.Error("409 sayfasinda sifre formu var: baglama YOK demek")
	}
	if _, err := o.federated.UserByIdentity(ctx, providerApple, "yeni-sub"); err == nil {
		t.Error("kimlik baglandi: baglama YOK")
	}
	if o.requests.subject(o.authReqID) != "" {
		t.Error("istek tamamlandi: giris OLMAMALI")
	}
}

// ---------------------------------------------------------------------
// Step 3: Link page tests
// ---------------------------------------------------------------------

// Phase 1's C1 lesson: if there is NO binding the form is NEVER RENDERED.
func TestLinkGet_WithoutBindingRejected(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, _, cookie, _ := o.setupPendingLink(t)

	statuses := []struct {
		name   string
		id     string
		cookie *http.Cookie
	}{
		{"beklemeID yok", "", cookie},
		{"uydurma beklemeID", "uydurma-bekleme-id", cookie},
		{"oturum cerezi yok", pendingID, nil},
		{"baska bir istege bagli oturum", pendingID, o.bindSession(t, "baska-istek")},
	}
	for _, d := range statuses {
		t.Run(d.name, func(t *testing.T) {
			w := o.linkGetRequest(d.id, d.cookie)
			if w.Code != http.StatusForbidden {
				t.Errorf("durum = %d, beklenen 403", w.Code)
			}
			if strings.Contains(w.Body.String(), "<form") {
				t.Errorf("baglamasiz istege FORM render edildi: %q", w.Body.String())
			}
		})
	}
}

func TestLinkPost_WrongCSRFRejected(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, _, cookie, existing := o.setupPendingLink(t)

	w := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {"yanlis-csrf"},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", w.Code)
	}
	if _, err := o.federated.UserByIdentity(context.Background(), providerGoogle, o.google.sub); err == nil {
		t.Error("CSRF yanlisken kimlik baglandi")
	}
	if o.requests.subject(o.authReqID) == existing.ID {
		t.Error("CSRF yanlisken giris yapildi")
	}
}

func TestLinkPost_CorrectPasswordLinksAndLogsIn(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, cookie, existing := o.setupPendingLink(t)

	w := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("durum = %d, beklenen 302, govde = %q", w.Code, w.Body.String())
	}
	if location := w.Header().Get("Location"); location != o.expectedCallback() {
		t.Errorf("Location = %q, beklenen %q", location, o.expectedCallback())
	}
	linked, err := o.federated.UserByIdentity(context.Background(), providerGoogle, o.google.sub)
	if err != nil {
		t.Fatalf("kimlik baglanmadi: %v", err)
	}
	if linked.ID != existing.ID {
		t.Errorf("kimlik %q hesabina baglandi, beklenen %q", linked.ID, existing.ID)
	}
	if s := o.requests.subject(o.authReqID); s != existing.ID {
		t.Errorf("Subject = %q, beklenen %q", s, existing.ID)
	}
	if got := o.requests.amrFor(o.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, beklenen [fed]", got)
	}
}

func TestLinkPost_WrongPasswordDoesNotLink(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := o.setupPendingLink(t)

	w := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {"yanlis-sifre-degeri"},
	}, cookie)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("durum = %d, beklenen 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), msgLoginFailed) {
		t.Errorf("govde hata metnini tasimiyor: %q", w.Body.String())
	}
	if _, err := o.federated.UserByIdentity(context.Background(), providerGoogle, o.google.sub); err == nil {
		t.Error("yanlis sifreyle kimlik BAGLANDI")
	}
	if o.requests.subject(o.authReqID) != "" {
		t.Error("yanlis sifreyle giris yapildi")
	}
}

// The pending link must be SINGLE-USE: after the correct password is entered
// a second link cannot be made with the same PendingID.
func TestPendingLink_SingleUse(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := o.setupPendingLink(t)
	form := url.Values{fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {testPassword}}

	first := o.linkPostRequest(form, cookie)
	if first.Code != http.StatusFound {
		t.Fatalf("ilk POST durum = %d, beklenen 302, govde = %q", first.Code, first.Body.String())
	}
	second := o.linkPostRequest(form, cookie)
	if second.Code != http.StatusForbidden {
		t.Errorf("ikinci POST durum = %d, beklenen 403 (bekleme tek kullanimlik)", second.Code)
	}
	// The form must not open any more either.
	if w := o.linkGetRequest(pendingID, cookie); w.Code != http.StatusForbidden {
		t.Errorf("tuketilmis bekleme icin GET durum = %d, beklenen 403", w.Code)
	}
}

// The attacker must not be able to complete SOMEONE ELSE's pending link in
// their own browser.
func TestLink_CannotCompleteInOtherBrowser(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, _, existing := o.setupPendingLink(t)
	// The attacker's browser: it has a session cookie bound to its own flow but
	// NOT bound to the VICTIM's request.
	attacker := o.bindSession(t, "saldirganin-istegi")

	w := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, attacker)
	if w.Code != http.StatusForbidden {
		t.Errorf("durum = %d, beklenen 403", w.Code)
	}
	if _, err := o.federated.UserByIdentity(context.Background(), providerGoogle, o.google.sub); err == nil {
		t.Error("baska tarayicida baglama TAMAMLANDI")
	}
	if o.requests.subject(o.authReqID) == existing.ID {
		t.Error("baska tarayicida giris yapildi")
	}
}

// Password attempts must not be UNLIMITED: the link page is the SECOND
// surface for password guessing and a separate endpoint from Phase 1's login
// limit.
func TestLink_RateLimitApplied(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := o.setupPendingLink(t)

	// A separate mux: the same handler, but this time wrapped with the limiter's limit.
	limiter, err := NewRateLimiter(o.store.rdb, RateLimitConfig{
		LoginIPPerMin: 1, LoginEmailPerHour: 100, SignupIPPerHour: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("hiz limiti kurulamadi: %v", err)
	}
	mux := http.NewServeMux()
	interceptor := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	o.pages.MountLink(mux, interceptor, limiter)
	o.mux = mux

	form := url.Values{fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {"yanlis-sifre-degeri"}}
	first := o.linkPostRequest(form, cookie)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("ilk deneme 429 aldi; sinir yanlis kurulmus")
	}
	second := o.linkPostRequest(form, cookie)
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("ikinci deneme durum = %d, beklenen 429", second.Code)
	}

	// The counter must be SEPARATE from the login endpoint: the login budget
	// must not have been used up.
	ctx := context.Background()
	keyList, err := o.store.rdb.Keys(ctx, rateLimitPrefix+":*").Result()
	if err != nil && err != redis.Nil {
		t.Fatalf("anahtarlar okunamadi: %v", err)
	}
	var hasBinding bool
	for _, a := range keyList {
		if strings.Contains(a, ":baglama:") {
			hasBinding = true
		}
		if strings.Contains(a, ":giris:") {
			t.Errorf("baglama denemesi GIRIS sayacini tuketti: %q", a)
		}
	}
	if !hasBinding {
		t.Errorf("baglama sayaci olusmadi; anahtarlar: %v", keyList)
	}
}

// Password attempts must be limited PER ACCOUNT too: an attacker who gets
// past the IP counter by changing IPs can repeat the federation round to
// get a new pending record on every attempt. What limits the number of
// attempts aimed at a single account is the account counter.
func TestLink_AccountRateLimitApplied(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := o.setupPendingLink(t)

	// The IP limit is GENEROUS (100/min), the account limit is 1/hour: the ONLY
	// reason for rejection must be the account counter.
	limiter, err := NewRateLimiter(o.store.rdb, RateLimitConfig{
		LoginIPPerMin: 100, LoginEmailPerHour: 1, SignupIPPerHour: 100,
	}, []byte("test-anahtari"))
	if err != nil {
		t.Fatalf("hiz limiti kurulamadi: %v", err)
	}
	mux := http.NewServeMux()
	interceptor := op.NewIssuerInterceptor(func(*http.Request) string { return fedTestIssuer })
	o.pages.Mount(mux, interceptor, limiter)
	o.pages.MountLink(mux, interceptor, limiter)
	o.mux = mux

	first := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {"yanlis-sifre-degeri"},
	}, cookie)
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("ilk deneme durum = %d, beklenen 401", first.Code)
	}

	// The attacker gets a NEW pending record (repeats the federation round):
	// the uniqueness of the record does not limit the number of attempts.
	secondFlow := o.runFlow(t, providerGoogle, false)
	if secondFlow.Code != http.StatusFound {
		t.Fatalf("ikinci akis durum = %d, beklenen 302", secondFlow.Code)
	}
	location, err := url.Parse(secondFlow.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location cozumlenemedi: %v", err)
	}
	newID := location.Query().Get(fieldPendingID)
	if newID == "" || newID == pendingID {
		t.Fatalf("yeni bekleme kaydi alinamadi: %q", newID)
	}
	newForm := o.linkGetRequest(newID, cookie)
	if newForm.Code != http.StatusOK {
		t.Fatalf("yeni form acilamadi: durum = %d", newForm.Code)
	}
	matches := csrfPattern.FindStringSubmatch(newForm.Body.String())
	if matches == nil {
		t.Fatal("yeni formda CSRF jetonu yok")
	}

	second := o.linkPostRequest(url.Values{
		fieldPendingID: {newID}, fieldCSRF: {matches[1]}, "sifre": {"yanlis-sifre-degeri"},
	}, cookie)
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("ikinci deneme durum = %d, beklenen 429 (hesap sayaci)", second.Code)
	}

	keyList, err := o.store.rdb.Keys(context.Background(), rateLimitPrefix+":*").Result()
	if err != nil {
		t.Fatalf("anahtarlar okunamadi: %v", err)
	}
	var hasAccount bool
	for _, a := range keyList {
		if strings.Contains(a, ":baglama-hesap:") {
			hasAccount = true
		}
	}
	if !hasAccount {
		t.Errorf("hesap sayaci olusmadi; anahtarlar: %v", keyList)
	}
	// The account identity must not appear in PLAIN TEXT in the Redis key.
	for _, a := range keyList {
		if strings.Contains(a, o.google.email) {
			t.Errorf("sayac anahtari e-postayi duz metin tasiyor: %q", a)
		}
	}
}

// The pending record must not be consumed BEFORE the validations: a third
// party who learns the pendingID must not be able to DESTROY the victim's
// pending link with a single cookieless POST (otherwise the victim would
// have to redo the whole federation round).
func TestPendingLink_UnboundPostCannotDestroy(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, cookie, existing := o.setupPendingLink(t)

	// The attacker: NO cookie, NO CSRF.
	attack := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID}, "sifre": {"herhangi"},
	}, nil)
	if attack.Code != http.StatusForbidden {
		t.Fatalf("saldiri durum = %d, beklenen 403", attack.Code)
	}

	// The victim must STILL be able to complete their own flow.
	w := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID}, fieldCSRF: {csrf}, "sifre": {testPassword},
	}, cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("kurbanin beklemesi yok edildi: durum = %d, govde = %q", w.Code, w.Body.String())
	}
	if s := o.requests.subject(o.authReqID); s != existing.ID {
		t.Errorf("Subject = %q, beklenen %q", s, existing.ID)
	}
	if got := o.requests.amrFor(o.authReqID); !slices.Equal(got, []string{"fed"}) {
		t.Errorf("amr = %v, beklenen [fed]", got)
	}
}

// An account without a password must NEVER come to the link page; if it
// does, 409.
func TestLink_NotOpenedForPasswordlessAccount(t *testing.T) {
	o := newTestFederationPages(t)
	ctx := context.Background()
	// resolveUser does not produce this record (the K1 branch returns 409); the
	// record is written BY HAND, so this is a defense-in-depth test.
	passwordless, err := o.federated.CreateFederatedUser(ctx, providerGoogle, "google-sub", "sifresiz@ornek.com", "Sifresiz", true)
	if err != nil {
		t.Fatalf("federe hesap olusturulamadi: %v", err)
	}
	pendingID, err := randomToken()
	if err != nil {
		t.Fatalf("id uretilemedi: %v", err)
	}
	csrf, err := randomToken()
	if err != nil {
		t.Fatalf("csrf uretilemedi: %v", err)
	}
	pending := PendingLink{
		AuthRequestID: o.authReqID,
		UserID:        passwordless.ID,
		Provider:      providerApple,
		Sub:           "apple-sub",
		Email:         "sifresiz@ornek.com",
		CSRF:          csrf,
	}
	if err := o.pages.flows.WritePendingLink(ctx, pendingID, pending, pendingLinkTTL); err != nil {
		t.Fatalf("bekleme yazilamadi: %v", err)
	}
	cookie := o.sessionCookie

	w := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code != http.StatusConflict {
		t.Errorf("durum = %d, beklenen 409", w.Code)
	}
	if _, err := o.federated.UserByIdentity(ctx, providerApple, "apple-sub"); err == nil {
		t.Error("sifresiz hesaba baglama yapildi")
	}
	if o.requests.subject(o.authReqID) != "" {
		t.Error("sifresiz hesapta giris yapildi")
	}
}

// I3, link leg: pendingLinkTTL also overlaps with the request TTL, and since
// the request counter starts at the very beginning and is never extended on
// any path, the auth request may have expired even when the user enters the
// password CORRECTLY. This is an ordinary expiry and must not produce a 500:
// the user must read what happened and that they must restart from the app.
func TestLinkPost_ExpiredRequestReturns400(t *testing.T) {
	o := newTestFederationPages(t)
	pendingID, csrf, cookie, _ := o.setupPendingLink(t)

	// The auth request expired with the TTL (since the TTL values are not
	// changed, the record is dropped by hand); the pending record and the session
	// binding are STILL valid, so the test really measures the EXPIRY path.
	o.requests.drop(o.authReqID)

	w := o.linkPostRequest(url.Values{
		fieldPendingID: {pendingID},
		fieldCSRF:      {csrf},
		"sifre":        {testPassword},
	}, cookie)
	if w.Code == http.StatusInternalServerError {
		t.Fatalf("siradan bir suresi-dolma 500 uretti: govde = %q", w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("durum = %d, beklenen 400, govde = %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), msgRequestExpired) {
		t.Errorf("kullaniciya ne oldugu soylenmemis: govde = %q", w.Body.String())
	}
	if o.requests.subject(o.authReqID) != "" {
		t.Error("dusmus bir istek TAMAMLANDI")
	}
}
